package dispatcher

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/openshift/ci-tools/pkg/slack/events"
)

const mentionCommandPrefix = "tp-dispatch"

func commandTextFromMention(text string) (string, bool) {
	tokens := strings.Fields(text)
	if len(tokens) < 2 || !strings.HasPrefix(tokens[0], "<@") || !strings.HasSuffix(tokens[0], ">") || tokens[1] != mentionCommandPrefix {
		return "", false
	}
	return strings.Join(tokens[2:], " "), true
}

func eventRequestID(callback *slackevents.EventsAPIEvent, event *slackevents.AppMentionEvent) string {
	if outer, ok := callback.Data.(*slackevents.EventsAPICallbackEvent); ok && outer.EventID != "" {
		return outer.EventID
	}
	return event.EventTimeStamp.String()
}

// MentionHandler returns an app-mention route for exact tp-dispatch commands.
// Unrelated mentions fall through to the bot's existing generic mention handler.
func (h *Handler) MentionHandler() events.PartialHandler {
	return events.PartialHandlerFunc(mentionCommandPrefix, func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (bool, error) {
		if callback.Type != slackevents.CallbackEvent {
			return false, nil
		}
		event, ok := callback.InnerEvent.Data.(*slackevents.AppMentionEvent)
		if !ok {
			return false, nil
		}
		text, routed := commandTextFromMention(event.Text)
		if !routed {
			return false, nil
		}
		if event.BotID != "" || event.User == "" {
			logger.WithField("bot_id", event.BotID).Warn("ignored tp-dispatch mention without a human user")
			return true, nil
		}
		if h.options.Messenger == nil {
			return true, errors.New("dispatcher mention messenger is not configured")
		}
		threadTS := event.ThreadTimeStamp
		if threadTS == "" {
			threadTS = event.TimeStamp
		}
		command := Command{
			TeamID: callback.TeamID, ChannelID: event.Channel, UserID: event.User, Text: text,
			RequestID: eventRequestID(callback, event), ThreadTS: threadTS,
		}
		if !h.reserveRequest(command.RequestID, time.Now().UTC()) {
			logger.WithField("event_id", command.RequestID).Info("ignored duplicate tp-dispatch Slack event")
			return true, nil
		}
		result := h.Handle(context.Background(), command)
		_, _, err := h.options.Messenger.PostMessage(event.Channel, slack.MsgOptionText(result.Text, false), slack.MsgOptionTS(threadTS))
		if err != nil {
			h.releaseRequest(command.RequestID)
			return true, err
		}
		if result.override != nil {
			h.markObserved(result.override)
		}
		return true, nil
	})
}
