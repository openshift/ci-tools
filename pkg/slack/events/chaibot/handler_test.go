package chaibot

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/openshift/ci-tools/pkg/chaibot"
)

func TestHandler_IgnoresBotMessages(t *testing.T) {
	analyzer := chaibot.NewAnalyzer("http://test", "token", "template")
	h := Handler(slack.New("test-token"), analyzer, []string{"C12345"})

	event := &slackevents.EventsAPIEvent{
		Type: slackevents.CallbackEvent,
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: slackevents.MessageEvent,
			Data: &slackevents.MessageEvent{
				Channel: "C12345",
				BotID:   "B12345",
				Text:    "https://prow.ci.openshift.org/view/gs/test/123",
			},
		},
	}

	handled, err := h.Handle(event, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if handled {
		t.Error("should not handle bot messages")
	}
}

func TestHandler_IgnoresNonMonitoredChannels(t *testing.T) {
	analyzer := chaibot.NewAnalyzer("http://test", "token", "template")
	h := Handler(slack.New("test-token"), analyzer, []string{"C12345"})

	event := &slackevents.EventsAPIEvent{
		Type: slackevents.CallbackEvent,
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: slackevents.MessageEvent,
			Data: &slackevents.MessageEvent{
				Channel: "C99999", // Different channel
				Text:    "https://prow.ci.openshift.org/view/gs/test/123",
			},
		},
	}

	handled, err := h.Handle(event, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if handled {
		t.Error("should not handle non-monitored channels")
	}
}

func TestHandler_IgnoresMessagesWithoutProwURL(t *testing.T) {
	analyzer := chaibot.NewAnalyzer("http://test", "token", "template")
	h := Handler(slack.New("test-token"), analyzer, []string{"C12345"})

	event := &slackevents.EventsAPIEvent{
		Type: slackevents.CallbackEvent,
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: slackevents.MessageEvent,
			Data: &slackevents.MessageEvent{
				Channel: "C12345",
				Text:    "Just a normal message",
			},
		},
	}

	handled, err := h.Handle(event, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if handled {
		t.Error("should not handle messages without Prow URLs")
	}
}

func TestHandler_Identifier(t *testing.T) {
	analyzer := chaibot.NewAnalyzer("http://test", "token", "template")
	h := Handler(slack.New("test-token"), analyzer, []string{"C12345"})

	if h.Identifier() != "chaibot" {
		t.Errorf("expected identifier 'chaibot', got %s", h.Identifier())
	}
}
