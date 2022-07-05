package helpdesk

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/openshift/ci-tools/pkg/slack/events"
)

type messagePoster interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
}

type KeywordsConfig struct {
	KeywordList []KeywordsListItem `json:"keyword_list"`
}

type KeywordsListItem struct {
	Name     string   `json:"name"`
	Keywords []string `json:"keywords"`
	Link     string   `json:"link"`
}

// Handler returns a handler that knows how to respond to new messages
// in forum-testplatform channel that mention @dptp-helpdesk.
func Handler(client messagePoster, keywordsConfig KeywordsConfig, helpdeskAlias, forumChannelId string, requireWorkflowsInForum bool) events.PartialHandler {
	return events.PartialHandlerFunc("helpdesk",
		func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
			log := logger.WithField("handler", "helpdesk-message")
			log.Debug("checking event payload")

			if callback.Type != slackevents.CallbackEvent {
				return false, nil
			}

			event, ok := callback.InnerEvent.Data.(*slackevents.MessageEvent)
			if !ok {
				return false, nil
			}
			log = log.WithFields(logrus.Fields{"user": event.User, "bot_id": event.BotID})
			if event.ChannelType != "channel" {
				return false, nil
			}
			if event.Channel != forumChannelId {
				log.Debugf("not in correct channel. wanted: %s, message was in: %s", forumChannelId, event.Channel)
				return false, nil
			}

			var response []slack.Block
			notifyToUseWorkflow := requireWorkflowsInForum && event.ThreadTimeStamp == "" && event.BotID == ""
			if notifyToUseWorkflow {
				log.Debug("Top level message not from a workflow, notifying user")
				response = getTopLevelDirectMessageResponse(event.User)
			} else if strings.Contains(event.Text, helpdeskAlias) {
				log.Info("Handling response in forum-testplatform channel...")
				response = getContactedHelpdeskResponse()
			} else {
				log.Debugf("dptp-helpdesk not mentioned in message: %s", event.Text)
				return false, nil
			}

			timestamp := event.TimeStamp
			if event.ThreadTimeStamp != "" {
				timestamp = event.ThreadTimeStamp
			}
			responseChannel, responseTimestamp, err := client.PostMessage(event.Channel, slack.MsgOptionBlocks(response...), slack.MsgOptionTS(timestamp))
			if err != nil {
				log.WithError(err).Warn("Failed to post a response")
			} else {
				log.Infof("Posted response in a new thread in channel %s to user %s at %s", responseChannel, event.User, responseTimestamp)
			}
			if notifyToUseWorkflow {
				return true, nil
			}

			if keywords := getPresentKeywords(event.Text, keywordsConfig); len(keywords) > 0 {
				responseChannel, responseTimestamp, err = client.PostMessage(event.Channel, slack.MsgOptionBlocks(getDocsLinks(keywords)...), slack.MsgOptionTS(timestamp), slack.MsgOptionDisableLinkUnfurl())
				if err != nil {
					log.WithError(err).Warn("Failed to post links to ci docs")
				} else {
					log.Infof("Posted links to ci docs in %s at %s", responseChannel, responseTimestamp)
				}
			}

			return true, err
		})
}

func getTopLevelDirectMessageResponse(user string) []slack.Block {
	return []slack.Block{&slack.SectionBlock{
		Type: slack.MBTSection,
		Text: &slack.TextBlockObject{
			Type: slack.MarkdownType,
			Text: fmt.Sprintf("<@%s>, Test Platform responds to questions and requests via workflows located in this channel. "+
				"Please click the :heavy_plus_sign: on the bottom left of the message box, and look for the :zap: to utilize the appropriate workflow.", user),
		},
	}}
}

func getContactedHelpdeskResponse() []slack.Block {
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

func getDocsLinks(keywords map[string]string) []slack.Block {
	var blocks []slack.Block
	docLinks := ":bulb: It looks like you are asking about a few known topics. Have you checked these pages:"

	for name, link := range keywords {
		docLinks += fmt.Sprintf("\n• <%s|%s>", link, name)
	}

	blocks = append(blocks, &slack.SectionBlock{
		Type: slack.MBTSection,
		Text: &slack.TextBlockObject{
			Type: slack.MarkdownType,
			Text: docLinks,
		},
	})

	return blocks
}

func getPresentKeywords(message string, keywordsConfig KeywordsConfig) map[string]string {
	keywordMap := make(map[string]string)
	message = strings.ToLower(message)

	for _, item := range keywordsConfig.KeywordList {
		for _, keyword := range item.Keywords {
			if strings.Contains(message, keyword) {
				keywordMap[item.Name] = item.Link
				break
			}
		}
	}

	return keywordMap
}
