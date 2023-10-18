package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/labels"
	"k8s.io/test-infra/prow/logrusutil"

	"github.com/openshift/ci-tools/pkg/rover"
)

type options struct {
	config         string
	githubUsers    string
	slackTokenPath string
	validateOnly   bool
	logLevel       string

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

	if o.githubUsers == "" {
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
	fs.StringVar(&o.githubUsers, "github-users-file", "", "The GitHub users' info file location")
	fs.StringVar(&o.slackTokenPath, "slack-token-path", "", "Path to the file containing the Slack token to use.")
	fs.BoolVar(&o.validateOnly, "validate-only", false, "Run the tool in validate-only mode. This will simply validate the config.")
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")

	o.GitHubOptions.AddFlags(fs)
	return o, fs.Parse(os.Args[1:])
}

type config struct {
	Teams []team `json:"teams"`
}

// getInterestedLabels returns a set of those labels we are interested in when using the PR reminder
func getInterestedLabels() sets.Set[string] {
	var prLabels = sets.Set[string]{}
	prLabels.Insert("approved")
	prLabels.Insert("lgtm")
	prLabels.Insert("do-not-merge/hold")
	return prLabels
}

// getUnactionablePrLabels returns a set of those labels that mark a PR which can't be reviewed in its current state
func getUnactionablePrLabels() sets.Set[string] {
	var prLabels = sets.Set[string]{}
	prLabels.Insert(labels.WorkInProgress, labels.NeedsRebase)
	return prLabels
}

var orgRepoFormat = regexp.MustCompile(`\w+/\w+`)

func (c *config) validate(gtk githubToKerberos, slackClient slackClient) error {
	var errors []error
	for i, t := range c.Teams {
		if len(t.TeamMembers) == 0 {
			errors = append(errors, fmt.Errorf("teams[%d] doesn't contain any teamMembers", i))
		}

		for _, r := range t.Repos {
			if !orgRepoFormat.MatchString(r) {
				errors = append(errors, fmt.Errorf("teams[%d] has improperly formatted org/repo: %s", i, r))
			}
		}
	}

	_, err := c.createUsers(gtk, slackClient)
	if err != nil {
		errors = append(errors, err)
	}

	return kerrors.NewAggregate(errors)
}

func (c *config) createUsers(gtk githubToKerberos, slackClient slackClient) (map[string]user, error) {
	users := make(map[string]user)
	var errors []error
	for _, team := range c.Teams {
		for _, member := range team.TeamMembers {
			u, exists := users[member]
			if exists {
				u.TeamNames.Insert(team.TeamNames...)
				u.Repos.Insert(team.Repos...)
			} else {
				email := fmt.Sprintf("%s@redhat.com", member)
				slackUser, err := slackClient.GetUserByEmail(email)
				var slackId string
				if err != nil {
					// Even though we won't be able to find PRs for this user we should leave them in the list for now to determine if there is a github ID found
					errors = append(errors, fmt.Errorf("could not get slack id for: %s: %w", member, err))
				} else {
					slackId = slackUser.ID
				}
				u = user{
					KerberosId: member,
					TeamNames:  sets.New[string](team.TeamNames...),
					SlackId:    slackId,
					Repos:      sets.New[string](team.Repos...),
				}
			}
			users[member] = u
		}
	}

	for githubId, kerberosId := range gtk {
		userInfo, exists := users[kerberosId]
		if exists {
			userInfo.GithubId = githubId
			users[kerberosId] = userInfo
		}
	}

	for id, userInfo := range users {
		if userInfo.GithubId == "" {
			errors = append(errors, fmt.Errorf("no githubId found for: %v", id))
			delete(users, id)
		}
		if userInfo.SlackId == "" {
			// The error was already found and added, but we don't want to include this user
			delete(users, id)
		}
	}

	return users, kerrors.NewAggregate(errors)
}

type team struct {
	TeamMembers []string `json:"teamMembers"`
	TeamNames   []string `json:"teamNames"`
	Repos       []string `json:"repos"`
}

type githubToKerberos map[string]string

type user struct {
	KerberosId string
	GithubId   string
	SlackId    string
	TeamNames  sets.Set[string]
	Repos      sets.Set[string]
	PrRequests []prRequest
}

func (u *user) requestedToReview(pr github.PullRequest) bool {
	// only check PRs that the user is not the author of, as they could have requested their own team
	if u.GithubId != pr.User.Login {
		for _, team := range pr.RequestedTeams {
			for _, teamName := range sets.List(u.TeamNames) {
				if teamName == team.Slug {
					return true
				}
			}
		}

		for _, reviewer := range pr.RequestedReviewers {
			if u.GithubId == reviewer.Login {
				return true
			}
		}

		for _, assignee := range pr.Assignees {
			if u.GithubId == assignee.Login {
				return true
			}
		}
	}

	return false
}

type prRequest struct {
	Repo        string
	Number      int
	Url         string
	Title       string
	Author      string
	Created     time.Time
	LastUpdated time.Time
	Labels      []string
}

func (p prRequest) link() string {
	return fmt.Sprintf("<%s|*%s#%d*>: %s - by: *%s*", p.Url, p.Repo, p.Number, p.Title, p.Author)
}

func (p prRequest) createdUpdatedMessage() string {
	message := fmt.Sprintf("%s Created: %s | Updated: %s",
		p.recency(),
		p.Created.Format(time.RFC1123),
		p.LastUpdated.Format(time.RFC1123))

	if time.Since(p.LastUpdated).Hours() <= 24 {
		message = fmt.Sprintf("%s %s", newUpdate, message)
	}
	return message
}

const (
	recent    = ":large_green_circle:"
	normal    = ":large_orange_circle:"
	old       = ":red_circle:"
	newUpdate = ":new:"
	twoDays   = time.Hour * 24 * 2
	oneWeek   = time.Hour * 24 * 7
)

func (p prRequest) recency() string {
	now := time.Now()
	if p.Created.After(now.Add(-twoDays)) {
		return recent
	} else if p.Created.After(now.Add(-oneWeek)) {
		return normal
	} else {
		return old
	}
}

type prClient interface {
	GetPullRequests(org, repo string) ([]github.PullRequest, error)
}

type slackClient interface {
	GetUserByEmail(email string) (*slack.User, error)
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
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

	roverUsers := []rover.User{}
	if err = loadConfig(o.githubUsers, &roverUsers); err != nil {
		logrus.WithError(err).Fatal("failed to load rover groups config")
	}
	gtk := githubToKerberos(rover.MapGithubToKerberos(roverUsers))

	if err := secret.Add(o.slackTokenPath); err != nil {
		logrus.WithError(err).Fatal("failed to start secrets agent")
	}
	slackClient := slack.New(string(secret.GetSecret(o.slackTokenPath)))

	if o.validateOnly {
		if err := c.validate(gtk, slackClient); err != nil {
			logrus.WithError(err).Fatal("validation failed")
		} else {
			logrus.Infof("config is valid")
		}
	} else {
		users, err := c.createUsers(gtk, slackClient)
		if err != nil {
			logrus.WithError(err).Error("failed to create some users")
		}

		if len(users) > 0 {
			ghClient, err := o.GitHubOptions.GitHubClient(false)
			if err != nil {
				logrus.WithError(err).Fatal("failed to create github client")
			}

			var errs []error
			for _, user := range findPrsForUsers(users, ghClient) {
				logrus.Infof("%d PRs were found for user: %s", len(user.PrRequests), user.KerberosId)
				if len(user.PrRequests) > 0 {
					// sort by most recent update first
					sort.Slice(user.PrRequests, func(i, j int) bool {
						return user.PrRequests[i].LastUpdated.After(user.PrRequests[j].LastUpdated)
					})

					if err = messageUser(user, slackClient); err != nil {
						logrus.WithField("kerberosId", user.KerberosId).WithError(err).Error("failed to message user")
						errs = append(errs, err)
					}
				}
			}
			if len(errs) > 0 {
				logrus.WithError(kerrors.NewAggregate(errs)).Fatal("Failed to message users")
			}
		}
	}
}

func findPrsForUsers(users map[string]user, ghClient prClient) map[string]user {
	repos := sets.New[string]()
	for _, u := range users {
		repos.Insert(sets.List(u.Repos)...)
	}

	logrus.Infof("finding PRs for %d users in %d repos", len(users), len(repos))

	repoToPRs := make(map[string][]github.PullRequest, len(repos))
	for _, orgRepo := range sets.List(repos) {
		split := strings.Split(orgRepo, "/")
		org, repo := split[0], split[1]

		prs, err := ghClient.GetPullRequests(org, repo)
		if err != nil {
			logrus.Errorf("failed to get pull requests for: %s: %v", repo, err)
		}
		repoToPRs[orgRepo] = prs
	}

	for i, u := range users {
		for _, repo := range sets.List(u.Repos) {
			for _, pr := range repoToPRs[repo] {
				if !hasUnactionableLabels(pr.Labels) && u.requestedToReview(pr) {
					u.PrRequests = append(u.PrRequests, prRequest{
						Repo:        repo,
						Number:      pr.Number,
						Url:         pr.HTMLURL,
						Title:       pr.Title,
						Author:      pr.User.Login,
						Created:     pr.CreatedAt,
						LastUpdated: pr.UpdatedAt,
						Labels:      filterLabels(pr.Labels, getInterestedLabels()),
					})
					users[i] = u
				}
			}
		}
	}

	return users
}

// filterLabels filters out those labels from the PR we are not interested in
// and returns only those that are included in the interestedLabels set
func filterLabels(labels []github.Label, interestedLabels sets.Set[string]) []string {
	var result []string
	for _, label := range labels {
		if interestedLabels.Has(label.Name) {
			result = append(result, label.Name)
		}
	}
	sort.Strings(result)
	return result
}

func loadConfig(filename string, config interface{}) error {
	configData, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}
	if err = yaml.Unmarshal(configData, &config); err != nil {
		return fmt.Errorf("failed to unmarshall config: %w", err)
	}
	return nil
}

func messageUser(user user, slackClient slackClient) error {
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
				Text: fmt.Sprintf("You have %d PR(s) to review:", len(user.PrRequests)),
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
					&slack.TextBlockObject{
						Type: slack.MarkdownType,
						Text: fmt.Sprintf("%s: updated in the last 24 hours", newUpdate),
					},
				},
			},
		},
	}

	message = append(message, &slack.DividerBlock{Type: slack.MBTDivider})

	for _, pr := range user.PrRequests {
		prBlock := &slack.ContextBlock{
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
		}
		if len(pr.Labels) > 0 {
			prBlock.ContextElements.Elements = append(prBlock.ContextElements.Elements, &slack.TextBlockObject{
				Type: slack.MarkdownType,
				Text: getLabelMessage(pr.Labels),
			})
		}
		message = append(message, prBlock)
	}

	responseChannel, responseTimestamp, err := slackClient.PostMessage(user.SlackId,
		slack.MsgOptionText("PR Review Reminders.", false),
		slack.MsgOptionBlocks(message...))
	if err != nil {
		logrus.WithError(err).WithField("kerberosId", user.KerberosId).WithField("message", message).Debug("Failed to message user about PR review reminder")
		errors = append(errors, fmt.Errorf("failed to message user: %s about PR review reminder: %w", user.KerberosId, err))
	} else {
		logrus.Infof("Posted PR review reminder for user: %s in channel: %s at: %s", user.KerberosId, responseChannel, responseTimestamp)
	}

	return kerrors.NewAggregate(errors)
}

// getLabelMessage returns a string listing te PR's labels
func getLabelMessage(labels []string) string {
	return fmt.Sprintf(":label: labeled: *%v*", strings.Join(labels[:], ", "))
}

// hasUnactionableLabels returns whether a PR has any labels which mark a PR
// that can't be reviewed in its current state
func hasUnactionableLabels(labels []github.Label) bool {
	unactionableLabels := getUnactionablePrLabels()
	for _, label := range labels {
		if unactionableLabels.Has(label.Name) {
			return true
		}
	}
	return false
}
