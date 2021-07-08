package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/PagerDuty/go-pagerduty"
	jiraapi "github.com/andygrunwald/go-jira"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	jirautil "k8s.io/test-infra/prow/jira"
	"k8s.io/test-infra/prow/logrusutil"

	"github.com/openshift/ci-tools/pkg/jira"
	"github.com/openshift/ci-tools/pkg/pagerdutyutil"
)

type options struct {
	logLevel string

	jiraOptions      prowflagutil.JiraOptions
	pagerDutyOptions pagerdutyutil.Options

	slackTokenPath string
}

func (o *options) Validate() error {
	_, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}

	if o.slackTokenPath == "" {
		return fmt.Errorf("--slack-token-path is required")
	}

	for _, group := range []flagutil.OptionGroup{&o.jiraOptions, &o.pagerDutyOptions} {
		if err := group.Validate(false); err != nil {
			return err
		}
	}

	return nil
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	var o options
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")

	for _, group := range []flagutil.OptionGroup{&o.jiraOptions, &o.pagerDutyOptions} {
		group.AddFlags(fs)
	}

	fs.StringVar(&o.slackTokenPath, "slack-token-path", "", "Path to the file containing the Slack token to use.")

	if err := fs.Parse(args); err != nil {
		logrus.WithError(err).Fatal("Could not parse args.")
	}
	return o
}

func main() {
	logrusutil.ComponentInit()

	o := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}
	level, _ := logrus.ParseLevel(o.logLevel)
	logrus.SetLevel(level)

	secretAgent := &secret.Agent{}
	if err := secretAgent.Start([]string{o.slackTokenPath}); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	var blocks []slack.Block

	slackClient := slack.New(string(secretAgent.GetSecret(o.slackTokenPath)))
	pagerDutyClient, err := o.pagerDutyOptions.Client(secretAgent)
	if err != nil {
		logrus.WithError(err).Fatal("Could not initialize PagerDuty client.")
	}
	userIdsByRole, err := users(pagerDutyClient, slackClient)
	if err != nil {
		logrus.WithError(err).Fatal("Could not get rotating roles from PagerDuty.")
	}
	blocks = append(blocks, getPagerDutyBlocks(userIdsByRole)...)

	prowJiraClient, err := o.jiraOptions.Client(secretAgent)
	if err != nil {
		logrus.WithError(err).Fatal("Could not initialize Jira client.")
	}
	jiraClient := prowJiraClient.JiraClient()
	if approvalBlocks, err := getIssuesNeedingApproval(jiraClient); err != nil {
		logrus.WithError(err).Fatal("Could not get issues needing approval.")
	} else {
		blocks = append(blocks, approvalBlocks...)
	}

	if err := postBlocks(slackClient, blocks); err != nil {
		logrus.WithError(err).Fatal("Could not post team digest to Slack.")
	}

	if err := sendIntakeDigest(slackClient, jiraClient, userIdsByRole[roleIntake]); err != nil {
		logrus.WithError(err).Fatal("Could not post @dptp-intake digest to Slack.")
	}
}

const (
	primaryOnCallQuery     = "DPTP Primary On-Call"
	secondaryUSOnCallQuery = "DPTP Secondary On-Call (US)"
	secondaryEUOnCallQuery = "DPTP Secondary On-Call (EU)"
	roleTriagePrimary      = "@dptp-triage Primary"
	roleTriageSecondaryUS  = "@dptp-triage Secondary (US)"
	roleTriageSecondaryEU  = "@dptp-triage Secondary (EU)"
	roleHelpdesk           = "@dptp-helpdesk"
	roleIntake             = "@dptp-intake"
)

func getPagerDutyBlocks(userIdsByRole map[string]string) []slack.Block {
	var fields []*slack.TextBlockObject
	for _, role := range []string{roleTriagePrimary, roleTriageSecondaryUS, roleTriageSecondaryEU, roleHelpdesk, roleIntake} {
		fields = append(fields, &slack.TextBlockObject{
			Type: slack.PlainTextType,
			Text: role,
		}, &slack.TextBlockObject{
			Type: slack.MarkdownType,
			Text: fmt.Sprintf("<@%s>", userIdsByRole[role]),
		})
	}

	blocks := []slack.Block{
		&slack.HeaderBlock{
			Type: slack.MBTHeader,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "Today's Rotating Positions",
			},
		},
		&slack.SectionBlock{
			Type:   slack.MBTSection,
			Fields: fields,
		},
	}

	return blocks
}

func users(client *pagerduty.Client, slackClient *slack.Client) (map[string]string, error) {
	now := time.Now()
	userIdsByRole := map[string]string{}
	for _, item := range []struct {
		role         string
		query        string
		since, until time.Time
	}{
		{
			role:  roleTriagePrimary,
			query: primaryOnCallQuery,
			since: now.Add(-1 * time.Second),
			until: now,
		},
		{
			role:  roleTriageSecondaryUS,
			query: secondaryUSOnCallQuery,
			since: now.Add(-24 * time.Hour),
			until: now,
		},
		{
			role:  roleTriageSecondaryEU,
			query: secondaryEUOnCallQuery,
			since: now.Add(-24 * time.Hour),
			until: now,
		},
		{
			role:  roleHelpdesk,
			query: primaryOnCallQuery,
			since: time.Now().Add(-7 * 24 * time.Hour).Add(-1 * time.Second),
			until: time.Now().Add(-7 * 24 * time.Hour),
		},
		{
			role:  roleIntake,
			query: primaryOnCallQuery,
			since: time.Now().Add(-2 * 7 * 24 * time.Hour).Add(-1 * time.Second),
			until: time.Now().Add(-2 * 7 * 24 * time.Hour),
		},
	} {
		pagerDutyUser, err := userOnCallDuring(client, item.query, item.since, item.until)
		if err != nil {
			return nil, fmt.Errorf("could not get PagerDuty user for %s: %w", item.role, err)
		}
		slackUser, err := slackClient.GetUserByEmail(pagerDutyUser.Email)
		if err != nil {
			return nil, fmt.Errorf("could not get slack user for %s: %w", pagerDutyUser.Name, err)
		}
		userIdsByRole[item.role] = slackUser.ID
	}
	return userIdsByRole, nil
}

func userOnCallDuring(client *pagerduty.Client, query string, since, until time.Time) (*pagerduty.User, error) {
	scheduleResponse, err := client.ListSchedules(pagerduty.ListSchedulesOptions{Query: query})
	if err != nil {
		return nil, fmt.Errorf("could not query PagerDuty for the %s on-call schedule: %w", query, err)
	}
	if len(scheduleResponse.Schedules) != 1 {
		return nil, fmt.Errorf("did not get exactly one schedule when querying PagerDuty for the %s on-call schedule: %v", query, scheduleResponse.Schedules)
	}

	users, err := client.ListOnCallUsers(scheduleResponse.Schedules[0].ID, pagerduty.ListOnCallUsersOptions{
		Since: since.String(),
		Until: until.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("could not query PagerDuty for the %s on-call: %w", query, err)
	}
	if len(users) != 1 {
		return nil, fmt.Errorf("did not get exactly one user when querying PagerDuty for the %s on-call: %v", query, users)
	}
	return &users[0], nil
}

func getIssuesNeedingApproval(jiraClient *jiraapi.Client) ([]slack.Block, error) {
	issues, response, err := jiraClient.Issue.Search(fmt.Sprintf(`project=%s AND status="QE Review"`, jira.ProjectDPTP), nil)
	if err := jirautil.JiraError(response, err); err != nil {
		return nil, fmt.Errorf("could not query for Jira issues: %w", err)
	}

	if len(issues) == 0 {
		return nil, nil
	}

	blocks := []slack.Block{
		&slack.HeaderBlock{
			Type: slack.MBTHeader,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "Cards Awaiting Acceptance",
			},
		},
		&slack.SectionBlock{
			Type: slack.MBTSection,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "The following issues are ready for acceptance on the DPTP board:",
			},
		},
	}
	idByUser := map[string]slack.Block{}
	blocksByUser := map[string][]slack.Block{}
	for _, issue := range issues {
		if _, recorded := idByUser[issue.Fields.Assignee.DisplayName]; !recorded {
			idByUser[issue.Fields.Assignee.DisplayName] = &slack.ContextBlock{
				Type: slack.MBTContext,
				ContextElements: slack.ContextElements{
					Elements: []slack.MixedElement{
						&slack.ImageBlockElement{
							Type:     slack.METImage,
							ImageURL: issue.Fields.Assignee.AvatarUrls.Four8X48,
							AltText:  issue.Fields.Assignee.DisplayName,
						},
						&slack.TextBlockObject{
							Type: slack.MarkdownType,
							Text: issue.Fields.Assignee.DisplayName,
						},
					},
				},
			}
		}
		blocksByUser[issue.Fields.Assignee.DisplayName] = append(blocksByUser[issue.Fields.Assignee.DisplayName], blockForIssue(issue))
	}

	for user, id := range idByUser {
		blocks = append(blocks, id)
		blocks = append(blocks, blocksByUser[user]...)
		blocks = append(blocks, &slack.DividerBlock{
			Type: slack.MBTDivider,
		})
	}
	return blocks, nil
}

const dptpTeamChannel = "team-dp-testplatform"

func postBlocks(slackClient *slack.Client, blocks []slack.Block) error {
	var channelID, cursor string
	for {
		conversations, nextCursor, err := slackClient.GetConversations(&slack.GetConversationsParameters{Cursor: cursor, Types: []string{"private_channel"}})
		if err != nil {
			return fmt.Errorf("could not query Slack for channel ID: %w", err)
		}
		for _, conversation := range conversations {
			if conversation.Name == dptpTeamChannel {
				channelID = conversation.ID
				break
			}
		}
		if channelID != "" || nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	if channelID == "" {
		return fmt.Errorf("could not find Slack channel %s", dptpTeamChannel)
	}

	responseChannel, responseTimestamp, err := slackClient.PostMessage(channelID, slack.MsgOptionText("Jira card digest.", false), slack.MsgOptionBlocks(blocks...))
	if err != nil {
		return fmt.Errorf("failed to post to channel: %w", err)
	} else {
		logrus.Infof("Posted team digest in channel %s at %s", responseChannel, responseTimestamp)
	}
	return nil
}

func sendIntakeDigest(slackClient *slack.Client, jiraClient *jiraapi.Client, userId string) error {
	issues, response, err := jiraClient.Issue.Search(fmt.Sprintf(`project=%s AND (labels is EMPTY OR NOT labels=ready) AND created >= startOfWeek()`, jira.ProjectDPTP), nil)
	if err := jirautil.JiraError(response, err); err != nil {
		return fmt.Errorf("could not query for Jira issues: %w", err)
	}

	if len(issues) == 0 {
		return nil
	}

	blocks := []slack.Block{
		&slack.HeaderBlock{
			Type: slack.MBTHeader,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "Cards Awaiting Intake",
			},
		},
		&slack.SectionBlock{
			Type: slack.MBTSection,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "The following issues need to be reviewed as part of the intake process:",
			},
		},
	}
	for _, issue := range issues {
		blocks = append(blocks, blockForIssue(issue))
	}
	responseChannel, responseTimestamp, err := slackClient.PostMessage(userId, slack.MsgOptionText("Jira card digest.", false), slack.MsgOptionBlocks(blocks...))
	if err != nil {
		return fmt.Errorf("failed to message @dptp-intake: %w", err)
	} else {
		logrus.Infof("Posted intake digest in channel %s at %s", responseChannel, responseTimestamp)
	}
	return nil
}

func blockForIssue(issue jiraapi.Issue) slack.Block {
	// we really don't want these things to line wrap, so truncate the summary
	cutoff := 85
	summary := issue.Fields.Summary
	if len(summary) > cutoff {
		summary = summary[0:cutoff-3] + "..."
	}
	return &slack.ContextBlock{
		Type: slack.MBTContext,
		ContextElements: slack.ContextElements{
			Elements: []slack.MixedElement{
				&slack.TextBlockObject{
					Type: slack.MarkdownType,
					Text: fmt.Sprintf("<%s|*%s*>: %s", issue.Self, issue.Key, summary),
				},
			},
		},
	}
}
