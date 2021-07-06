package main

import (
	"flag"
	"fmt"
	"os"

	jiraapi "github.com/andygrunwald/go-jira"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	jirautil "k8s.io/test-infra/prow/jira"
	"k8s.io/test-infra/prow/logrusutil"

	"github.com/openshift/ci-tools/pkg/jira"
)

type options struct {
	logLevel string

	jiraOptions prowflagutil.JiraOptions

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

	for _, group := range []flagutil.OptionGroup{&o.jiraOptions} {
		if err := group.Validate(false); err != nil {
			return err
		}
	}

	return nil
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	var o options
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")

	for _, group := range []flagutil.OptionGroup{&o.jiraOptions} {
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

	prowJiraClient, err := o.jiraOptions.Client(secretAgent)
	if err != nil {
		logrus.WithError(err).Fatal("Could not initialize Jira client.")
	}
	jiraClient := prowJiraClient.JiraClient()
	slackClient := slack.New(string(secretAgent.GetSecret(o.slackTokenPath)))

	if err := postIssuesNeedingApproval(slackClient, jiraClient); err != nil {
		logrus.WithError(err).Fatal("Could not post issues needing approval.")
	}
}

const dptpTeamChannel = "team-dp-testplatform"

func postIssuesNeedingApproval(slackClient *slack.Client, jiraClient *jiraapi.Client) error {
	issues, response, err := jiraClient.Issue.Search(fmt.Sprintf(`project=%s AND status="QE Review"`, jira.ProjectDPTP), nil)
	if err := jirautil.JiraError(response, err); err != nil {
		return fmt.Errorf("could not find Jira project %s: %w", jira.ProjectDPTP, err)
	}

	if len(issues) == 0 {
		return nil
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
		// we really don't want these things to line wrap, so truncate the summary
		cutoff := 85
		summary := issue.Fields.Summary
		if len(summary) > cutoff {
			summary = summary[0:cutoff-3] + "..."
		}
		blocksByUser[issue.Fields.Assignee.DisplayName] = append(blocksByUser[issue.Fields.Assignee.DisplayName], &slack.ContextBlock{
			Type: slack.MBTContext,
			ContextElements: slack.ContextElements{
				Elements: []slack.MixedElement{
					&slack.TextBlockObject{
						Type: slack.MarkdownType,
						Text: fmt.Sprintf("<%s|*%s*>: %s", issue.Self, issue.Key, summary),
					},
				},
			},
		})
	}

	for user, id := range idByUser {
		blocks = append(blocks, id)
		blocks = append(blocks, blocksByUser[user]...)
		blocks = append(blocks, &slack.DividerBlock{
			Type: slack.MBTDivider,
		})
	}

	var channelID, cursor string
	for {
		conversations, nextCursor, err := slackClient.GetConversations(&slack.GetConversationsParameters{Cursor: cursor})
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
		logrus.Infof("Posted response to app mention in channel %s at %s", responseChannel, responseTimestamp)
	}
	return nil
}
