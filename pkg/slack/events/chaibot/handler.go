package chaibot

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/openshift/ci-tools/pkg/chaibot"
	"github.com/openshift/ci-tools/pkg/slack/events"
)

type handler struct {
	client            *slack.Client
	analyzer          *chaibot.Analyzer
	monitoredChannels map[string]bool
}

func (h *handler) Handle(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
	if callback.Type != slackevents.CallbackEvent {
		return false, nil
	}

	event, ok := callback.InnerEvent.Data.(*slackevents.MessageEvent)
	if !ok {
		return false, nil
	}

	// Ignore bot messages to prevent loops
	if event.BotID != "" {
		return false, nil
	}

	// Only monitor configured channels
	if !h.monitoredChannels[event.Channel] {
		return false, nil
	}

	// Check for Prow URL
	prowURL := chaibot.ExtractProwURL(event.Text)
	if prowURL == "" {
		return false, nil
	}

	logger = logger.WithFields(logrus.Fields{
		"channel": event.Channel,
		"url":     prowURL,
	})
	logger.Info("Chaibot detected Prow failure URL")

	// Analyze async (can take 30-60s)
	go h.analyzeAndRespond(event, prowURL, logger)

	return true, nil
}

func (h *handler) Identifier() string {
	return "chaibot"
}

func (h *handler) analyzeAndRespond(event *slackevents.MessageEvent, prowURL string, logger *logrus.Entry) {
	result, err := h.analyzer.AnalyzeFailure(context.Background(), prowURL)
	if err != nil {
		logger.WithError(err).Error("Chaibot analysis failed")
		return
	}

	blocks := chaibot.FormatSlackResponse(result)

	_, _, err = h.client.PostMessage(
		event.Channel,
		slack.MsgOptionBlocks(blocks...),
		slack.MsgOptionTS(event.TimeStamp),
	)

	if err != nil {
		logger.WithError(err).Error("Failed to post Chaibot response")
	} else {
		logger.WithField("duration", result.Duration).Info("Chaibot analysis posted successfully")
	}
}

// Handler creates a new Chaibot event handler
func Handler(client *slack.Client, analyzer *chaibot.Analyzer, monitoredChannels []string) events.PartialHandler {
	channelMap := make(map[string]bool)
	for _, ch := range monitoredChannels {
		channelMap[ch] = true
	}

	return &handler{
		client:            client,
		analyzer:          analyzer,
		monitoredChannels: channelMap,
	}
}
