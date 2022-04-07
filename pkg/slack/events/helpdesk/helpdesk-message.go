package helpdesk

import (
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/openshift/ci-tools/pkg/slack/events"
)

const (
	channelId      = "CBN38N3MW"
	dptpHelpdeskId = "<@STSR51Q76>"
)

type ephemeralMessagePoster interface {
	PostEphemeral(channelID, userID string, options ...slack.MsgOption) (string, error)
}

// Handler returns a handler that knows how to respond to new messages
// in forum-testplatform channel that mention @dptp-helpdesk.
func Handler(client ephemeralMessagePoster) events.PartialHandler {
	return events.PartialHandlerFunc("helpdesk",
		func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {

			if callback.Type != slackevents.CallbackEvent {
				return false, nil
			}

			event, ok := callback.InnerEvent.Data.(*slackevents.MessageEvent)
			if !ok {
				return false, nil
			}
			if event.ChannelType != "channel" {
				return false, nil
			}
			if event.Channel != channelId {
				return false, nil
			}
			if !strings.Contains(event.Text, dptpHelpdeskId) {
				return false, nil
			}
			logger.Info("Handling response in forum-testplatform channel...")

			timestamp := event.TimeStamp
			if event.ThreadTimeStamp != "" {
				timestamp = event.ThreadTimeStamp
			}

			responseTimestamp, err := client.PostEphemeral(event.Channel, event.User, slack.MsgOptionBlocks(getResponse()...), slack.MsgOptionTS(timestamp))
			if err != nil {
				logger.WithError(err).Warn("Failed to post ephemeral response")
			} else {
				logger.Infof("Posted ephemeral message in channel %s to user %s at %s", event.Channel, event.User, responseTimestamp)
			}

			return true, err
		})
}

func getResponse() []slack.Block {
	sections := []string{
		":wave: You have reached the Test Platform Help Desk. An assigned engineer will respond in several hours during their working hours.",
		"Please see if our documentation can be of use: https://docs.ci.openshift.org/docs/",
		"If this is an urgent CI outage, please ping `(@)dptp-triage`",
	}

	var blocks []slack.Block

	for _, section := range sections {
		blocks = append(blocks, &slack.SectionBlock{
			Type: slack.MBTSection,
			Text: &slack.TextBlockObject{
				Type: slack.MarkdownType,
				Text: section,
			},
		})
	}

	return blocks
}
