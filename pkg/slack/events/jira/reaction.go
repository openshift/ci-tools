package jira

import (
	"slices"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/openshift/ci-tools/pkg/jira"
	"github.com/openshift/ci-tools/pkg/slack/events"
)

const (
	createJiraEmoji = "open_jira_dptp"
	closeJiraEmoji  = "close_jira_dptp"
)

// ReactionHandler handles emoji reactions for Jira integration.
func ReactionHandler(
	client *slack.Client,
	issueFiler jira.IssueFiler,
	threadMapping *ThreadJiraMapping,
	forumChannelID string,
	authorizedUsers []string,
) events.PartialHandler {
	return events.PartialHandlerFunc("jira-reaction",
		func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
			log := logger.WithField("handler", "jira-reaction")
			log.Debug("checking event payload")

			if callback.Type != slackevents.CallbackEvent {
				return false, nil
			}

			event, ok := callback.InnerEvent.Data.(*slackevents.ReactionAddedEvent)
			if !ok {
				return false, nil
			}

			if event.Item.Channel != forumChannelID {
				log.Debugf("not in correct channel. wanted: %s, reaction was in: %s", forumChannelID, event.Item.Channel)
				return false, nil
			}

			if !slices.Contains(authorizedUsers, event.User) {
				log.Infof("user with ID: %s is not a testplatform team member, ignoring emoji reaction", event.User)
				// Silently ignore - don't process emoji from non-team members
				return false, nil
			}

			emoji := strings.Trim(event.Reaction, ":")
			threadTS := event.Item.Timestamp

			if emoji == createJiraEmoji {
				return handleCreateJira(client, issueFiler, threadMapping, forumChannelID, threadTS, event.User, log)
			}
			log.Tracef("emoji we do not care about: %s", emoji)
			return false, nil
		})
}
