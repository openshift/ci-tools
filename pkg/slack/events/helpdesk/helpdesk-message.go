package helpdesk

import (
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack/slackevents"

	"github.com/openshift/ci-tools/pkg/slack/events"
)

const (
	channelId      = "CBN38N3MW"
	dptpHelpdeskId = "<@STSR51Q76>"
)

// Handler returns a handler that knows how to respond to new messages
// in forum-testplatform channel that mention @dptp-helpdesk.
func Handler() events.PartialHandler {
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

			return true, err
		})
}
