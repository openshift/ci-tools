package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/logrusutil"
)

type options struct {
	config            string
	roverGroupsConfig string
	slackTokenPath    string
	logLevel          string

	flagutil.GitHubOptions
}

func (o *options) validate() error {
	_, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}

	if o.config == "" {
		return fmt.Errorf("--config-path is required")
	}

	if o.roverGroupsConfig == "" {
		return fmt.Errorf("--rover-groups-config-path is required")
	}

	if o.slackTokenPath == "" {
		return fmt.Errorf("--slack-token-path is required")
	}

	return o.GitHubOptions.Validate(false)
}

func parseOptions() (options, error) {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.config, "config-path", "", "The config file location")
	fs.StringVar(&o.roverGroupsConfig, "rover-groups-config-path", "", "the sync-rover-groups config file location")
	fs.StringVar(&o.slackTokenPath, "slack-token-path", "", "Path to the file containing the Slack token to use.")
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")

	o.GitHubOptions.AddFlags(fs)
	return o, fs.Parse(os.Args[1:])
}

type config struct {
	TeamMembers []string `json:"teamMembers"`
	TeamName    string   `json:"teamName"`
	Repos       []string `json:"repos"`
}

type githubToKerberos map[string]string

type user struct {
	kerberosId string
	githubId   string
	slackId    string
	teamName   string
	prRequests []prRequest
}

func (u *user) requestedToReview(pr github.PullRequest) bool {
	for _, team := range pr.RequestedTeams {
		if u.teamName == team.Slug {
			return true
		}
	}

	for _, reviewer := range pr.RequestedReviewers {
		if u.githubId == reviewer.Login {
			return true
		}
	}

	return false
}

type prRequest struct {
	repo        string
	number      int
	url         string
	title       string
	author      string
	created     time.Time
	lastUpdated time.Time
}

func (p prRequest) link() string {
	return fmt.Sprintf("<%s|*%s#%d*>: %s - by: *%s*", p.url, p.repo, p.number, p.title, p.author)
}

const (
	recent = ":large_green_circle:"
	normal = ":large_orange_circle:"
	old    = ":red_circle:"
)

func (p prRequest) createdUpdatedMessage() string {
	var recency string
	// PRs that have been updated in the last day should be called out
	now := time.Now()
	if p.created.After(now.Add(-time.Hour * 24 * 2)) {
		recency = recent
	} else if p.created.After(now.Add(-time.Hour * 24 * 7)) {
		recency = normal
	} else {
		recency = old
	}
	return fmt.Sprintf("%s Created: %s | Updated: %s", recency, p.created.Format(time.RFC1123), p.lastUpdated.Format(time.RFC1123))
}

func main() {
	logrusutil.ComponentInit()

	o, err := parseOptions()
	if err != nil {
		logrus.WithError(err).Fatal("cannot parse args: ", os.Args[1:])
	}

	if err = o.validate(); err != nil {
		logrus.WithError(err).Fatal("validation failed")
	}

	level, _ := logrus.ParseLevel(o.logLevel)
	logrus.SetLevel(level)

	var c config
	if err = loadConfig(o.config, &c); err != nil {
		logrus.WithError(err).Fatal("failed to load config")
	}

	var gtk githubToKerberos
	if err = loadConfig(o.roverGroupsConfig, &gtk); err != nil {
		logrus.WithError(err).Fatal("failed to load rover groups config")
	}

	if err := secret.Add(o.slackTokenPath); err != nil {
		logrus.WithError(err).Fatal("failed to start secrets agent")
	}
	slackClient := slack.New(string(secret.GetSecret(o.slackTokenPath)))

	users, err := createUsers(c, gtk, slackClient)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create users")
	}

	ghClient, err := o.GitHubOptions.GitHubClient(false)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create github client")
	}

	for _, orgRepo := range c.Repos {
		split := strings.Split(orgRepo, "/")
		org, repo := split[0], split[1]

		prs, err := ghClient.GetPullRequests(org, repo)
		if err != nil {
			logrus.Errorf("failed to get pull requests: %v", err)
		}
		for _, pr := range prs {
			for i, u := range users {
				if u.requestedToReview(pr) {
					u.prRequests = append(u.prRequests, prRequest{
						repo:        orgRepo,
						number:      pr.Number,
						url:         pr.HTMLURL,
						title:       pr.Title,
						author:      pr.User.Login,
						created:     pr.CreatedAt,
						lastUpdated: pr.UpdatedAt,
					})
					users[i] = u
				}
			}

		}
	}

	for _, user := range users {
		if len(user.prRequests) > 0 {
			// sort by most recent update first
			sort.Slice(user.prRequests, func(i, j int) bool {
				return user.prRequests[i].lastUpdated.After(user.prRequests[j].lastUpdated)
			})

			if err = messageUser(user, slackClient); err != nil {
				logrus.WithError(err).Fatal("failed to message users")
			}
		}
	}

}

func loadConfig(filename string, config interface{}) error {
	configData, err := ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}
	if err = yaml.Unmarshal(configData, &config); err != nil {
		return fmt.Errorf("failed to unmarshall config: %v", err)
	}
	return nil
}

func createUsers(config config, gtk githubToKerberos, slackClient *slack.Client) (map[string]user, error) {
	users := make(map[string]user, len(config.TeamMembers))
	for _, member := range config.TeamMembers {
		email := fmt.Sprintf("%s@redhat.com", member)
		slackUser, err := slackClient.GetUserByEmail(email)
		if err != nil {
			return nil, fmt.Errorf("could not get slack user for %s: %w", member, err)
		}
		users[member] = user{
			kerberosId: member,
			teamName:   config.TeamName,
			slackId:    slackUser.ID,
		}
	}

	for githubId, kerberosId := range gtk {
		userInfo, exists := users[kerberosId]
		if exists {
			userInfo.githubId = githubId
			users[kerberosId] = userInfo
		}
	}

	var usersMissingGithubId []string
	for _, userInfo := range users {
		if userInfo.githubId == "" {
			usersMissingGithubId = append(usersMissingGithubId, userInfo.kerberosId)
		}
	}
	if len(usersMissingGithubId) > 0 {
		return nil, fmt.Errorf("no githubId found for user(s): %v", usersMissingGithubId)
	}

	return users, nil
}

func messageUser(user user, slackClient *slack.Client) error {
	var errors []error
	message := []slack.Block{
		&slack.HeaderBlock{
			Type: slack.MBTHeader,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "PR Review Reminders",
			},
		},
		&slack.SectionBlock{
			Type: slack.MBTSection,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: fmt.Sprintf("You have %d PR(s) to review:", len(user.prRequests)),
			},
		},
		&slack.ContextBlock{
			Type: slack.MBTContext,
			ContextElements: slack.ContextElements{
				Elements: []slack.MixedElement{
					&slack.TextBlockObject{
						Type: slack.MarkdownType,
						Text: fmt.Sprintf("%s: created in the last 2 days", recent),
					},
					&slack.TextBlockObject{
						Type: slack.MarkdownType,
						Text: fmt.Sprintf("%s: created in the last week", normal),
					},
					&slack.TextBlockObject{
						Type: slack.MarkdownType,
						Text: fmt.Sprintf("%s: created more than a week ago", old),
					},
				},
			},
		},
	}

	for _, pr := range user.prRequests {
		message = append(message, &slack.ContextBlock{
			Type: slack.MBTContext,
			ContextElements: slack.ContextElements{
				Elements: []slack.MixedElement{
					&slack.TextBlockObject{
						Type: slack.MarkdownType,
						Text: pr.link(),
					},
					&slack.TextBlockObject{
						Type: slack.MarkdownType,
						Text: pr.createdUpdatedMessage(),
					},
				},
			},
		})
	}

	responseChannel, responseTimestamp, err := slackClient.PostMessage(user.slackId,
		slack.MsgOptionText("PR Review Reminders.", false),
		slack.MsgOptionBlocks(message...))
	if err != nil {
		errors = append(errors, fmt.Errorf("failed to message userId: %s about PR review reminder: %w", user.slackId, err))
	} else {
		logrus.Infof("Posted PR review reminder in channel %s at %s", responseChannel, responseTimestamp)
	}

	return kerrors.NewAggregate(errors)
}
