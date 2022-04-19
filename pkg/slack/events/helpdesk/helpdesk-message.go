package helpdesk

import (
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/openshift/ci-tools/pkg/slack/events"
)

const (
	channelId       = "CBN38N3MW"
	helpdeskMention = "@dptp-helpdesk"
)

type messagePoster interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
}

// Handler returns a handler that knows how to respond to new messages
// in forum-testplatform channel that mention @dptp-helpdesk.
func Handler(client messagePoster) events.PartialHandler {
	return events.PartialHandlerFunc("helpdesk",
		func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
			log := logger.WithField("handler", "helpdesk-message")
			log.Debugf("checking event payload")

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
				log.Debugf("not in correct channel. wanted: %s, message was in: %s", channelId, event.Channel)
				return false, nil
			}
			if !strings.Contains(event.Text, helpdeskMention) {
				log.Debugf("dptp-helpdesk not mentioned in message: %s", event.Text)
				return false, nil
			}
			log.Info("Handling response in forum-testplatform channel...")

			timestamp := event.TimeStamp
			if event.ThreadTimeStamp != "" {
				timestamp = event.ThreadTimeStamp
			}

			responseChannel, responseTimestamp, err := client.PostMessage(event.Channel, slack.MsgOptionBlocks(getResponse()...), slack.MsgOptionTS(timestamp))
			if err != nil {
				log.WithError(err).Warn("Failed to post a response")
			} else {
				log.Infof("Posted response in a new thread in channel %s to user %s at %s", responseChannel, event.User, responseTimestamp)
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
