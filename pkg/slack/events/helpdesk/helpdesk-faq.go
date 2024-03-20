package helpdesk

import (
	helpdeskfaq "github.com/openshift/ci-tools/pkg/helpdesk-faq"
	"k8s.io/client-go/kubernetes"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/openshift/ci-tools/pkg/slack/events"
)

const (
	questionReaction = "channel_faq"
	answerReaction   = "faq_answer"
)

type slackClient interface {
	GetConversationHistory(params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error)
	GetConversationReplies(params *slack.GetConversationRepliesParameters) (msgs []slack.Message, hasMore bool, nextCursor string, err error)
}

func FAQHandler(client slackClient, kubeClient kubernetes.Interface, forumChannelId string) events.PartialHandler {
	return events.PartialHandlerFunc("helpdesk",
		func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
			log := logger.WithField("handler", "helpdesk-faq")
			log.Debug("checking event payload")

			if callback.Type != slackevents.CallbackEvent {
				return false, nil
			}

			cmClient := helpdeskfaq.NewCMClient(kubeClient)
			event, added := callback.InnerEvent.Data.(*slackevents.ReactionAddedEvent)
			if added {
				if event.Item.Channel != forumChannelId {
					log.Debugf("not in correct channel. wanted: %s, reaction was in: %s", forumChannelId, event.Item.Channel)
					return false, nil
				}
				return handleReactionAdded(event, client, kubeClient, &cmClient, forumChannelId, log)

			} else {
				event, removed := callback.InnerEvent.Data.(*slackevents.ReactionRemovedEvent)
				if removed {
					if event.Item.Channel != forumChannelId {
						log.Debugf("not in correct channel. wanted: %s, reaction was in: %s", forumChannelId, event.Item.Channel)
						return false, nil
					}
					return handleReactionRemoved(event, client, &cmClient, forumChannelId, log)
				} else {
					return false, nil
				}
			}
		})
}

func handleReactionRemoved(event *slackevents.ReactionRemovedEvent, client slackClient, faqItemClient helpdeskfaq.FaqItemClient, forumChannelId string, logger *logrus.Entry) (bool, error) {
	logger.Debugf("%s emoji removed from message", event.Reaction)
	switch event.Reaction {
	case questionReaction:
		questionLog := logger.WithField("type", "remove-question")
		if err := faqItemClient.RemoveItem(event.Item.Timestamp); err != nil {
			questionLog.WithError(err).Error("unable to update helpdesk-faq config map")
			return false, err
		}
	case answerReaction:
		answerLog := logger.WithField("type", "remove-answer")
		messageTs := event.Item.Timestamp
		replies, _, _, err := client.GetConversationReplies(&slack.GetConversationRepliesParameters{
			ChannelID: forumChannelId,
			Timestamp: messageTs,
			Inclusive: true,
		})
		if err != nil {
			answerLog.WithError(err).Error("unable to retrieve message that reaction was added for")
			return false, err
		}
		if len(replies) == 1 {
			reply := replies[0]
			questionTs := reply.Msg.ThreadTimestamp
			faqItem, err := faqItemClient.GetFAQItemIfExists(questionTs)
			if err != nil {
				answerLog.WithError(err).Warn("unable to get faqItem")
				return false, nil //Don't return the error, because this is due to the question not having been added
			}

			index := -1
			for i, answer := range faqItem.Answers {
				if answer.Timestamp == messageTs {
					index = i
					break
				}
			}
			if index >= 0 {
				faqItem.Answers = append(faqItem.Answers[:index], faqItem.Answers[index+1:]...)
			}
			if err := faqItemClient.UpsertItem(*faqItem); err != nil {
				answerLog.WithError(err).Error("unable to update helpdesk-faq config map")
				return false, err
			}
		}
	default:
		logger.Debugf("emoji we do not care about: %s", event.Reaction)
		return false, nil
	}

	return true, nil
}

func handleReactionAdded(event *slackevents.ReactionAddedEvent, client slackClient, kubeClient kubernetes.Interface, faqItemClient helpdeskfaq.FaqItemClient, forumChannelId string, logger *logrus.Entry) (bool, error) {
	logger.Debugf("%s emoji added to message", event.Reaction)
	//TODO: we will need a list of users whom reactions we actually care about, everyone else will be ignored
	switch event.Reaction {
	case questionReaction:
		questionLog := logger.WithField("type", "add-question")
		messageTs := event.Item.Timestamp
		item, err := faqItemClient.GetFAQItemIfExists(messageTs)
		if err != nil {
			questionLog.WithError(err).Error("unable to get faq item")
			return false, err
		}
		if item != nil {
			questionLog.Info("we already have a question for this faqItem, ignoring")
			return false, nil
		}

		message, err := getTopLevelMessage(client, forumChannelId, messageTs, questionLog)
		if err != nil {
			questionLog.WithError(err).Error("unable to get top-level message")
			return false, err
		}
		if message != nil {
			faqItem := helpdeskfaq.FaqItem{
				Question:  message.Text,
				Timestamp: messageTs,
				Author:    message.User,
			}

			var cursor string
			var hasMore bool
			var replies []slack.Message
			for {
				replies, hasMore, cursor, err = client.GetConversationReplies(&slack.GetConversationRepliesParameters{
					ChannelID: forumChannelId,
					Timestamp: messageTs,
					Inclusive: true,
					Cursor:    cursor,
				})
				if err != nil {
					questionLog.WithError(err).Error("unable to get replies for top-level message")
					return false, err
				}

				for _, reply := range replies {
					for _, reaction := range reply.Reactions {
						if reaction.Name == answerReaction {
							questionLog.Debugf("adding pre-marked answer with timestamp: %s", reply.Timestamp)
							faqItem.Answers = append(faqItem.Answers, helpdeskfaq.Answer{
								Author:    reply.User,
								Timestamp: reply.Timestamp,
								Body:      reply.Msg.Text,
							})
						}
					}
				}

				if !hasMore {
					break
				}
			}

			if err := faqItemClient.UpsertItem(faqItem); err != nil {
				questionLog.WithError(err).Error("unable to create helpdesk-faq item")
				return false, err
			}
		}
	case answerReaction:
		answerLog := logger.WithField("type", "add-answer")
		messageTs := event.Item.Timestamp
		replies, _, _, err := client.GetConversationReplies(&slack.GetConversationRepliesParameters{
			ChannelID: forumChannelId,
			Timestamp: messageTs,
			Inclusive: true,
		})
		if err != nil {
			answerLog.WithError(err).Error("unable to retrieve message that reaction was added for")
			return false, err
		}
		if len(replies) == 1 {
			reply := replies[0]
			questionTs := reply.Msg.ThreadTimestamp
			faqItem, err := faqItemClient.GetFAQItemIfExists(questionTs)
			if err != nil {
				answerLog.WithError(err).Error("unable to get faq item")
				return false, err
			}
			if faqItem == nil {
				answerLog.Info("requested answer doesn't belong to an existing question, ignoring")
				return false, nil
			}

			for _, answer := range faqItem.Answers {
				if answer.Timestamp == messageTs {
					answerLog.Debug("answer already exists, ignoring")
					return false, nil
				}
			}
			faqItem.Answers = append(faqItem.Answers, helpdeskfaq.Answer{
				Author:    reply.User,
				Timestamp: messageTs,
				Body:      reply.Msg.Text,
			})
			if err := faqItemClient.UpsertItem(*faqItem); err != nil {
				answerLog.WithError(err).Error("unable to update helpdesk-faq item")
				return false, err
			}

		}
	default:
		logger.Debugf("emoji we do not care about: %s", event.Reaction)
		return false, nil
	}

	return true, nil
}

func getTopLevelMessage(client slackClient, forumChannelId string, messageTs string, logger *logrus.Entry) (*slack.Message, error) {
	conversationHistory, err := client.GetConversationHistory(&slack.GetConversationHistoryParameters{
		ChannelID: forumChannelId,
		Inclusive: true,
		Latest:    messageTs,
		Limit:     1,
		Oldest:    messageTs,
	})
	if err != nil || len(conversationHistory.Messages) == 0 {
		if err != nil {
			logger.WithError(err).Error("unable to retrieve message that reaction was added for")
		} else {
			logger.Warn("unable to retrieve message, it is likely the reaction was not on a top-level thread")
		}
		return nil, err
	}
	return &conversationHistory.Messages[0], nil
}
