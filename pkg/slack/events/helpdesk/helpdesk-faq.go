package helpdesk

import (
	"context"
	"encoding/json"
	"fmt"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/openshift/ci-tools/pkg/slack/events"
)

const (
	questionReaction = "channel_faq"
	answerReaction   = "faq_answer"
	faqConfigMap     = "helpdesk-faq"
	ci               = "ci"
)

type helpdeskFAQClient interface {
	GetConversationHistory(params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error)
	GetConversationReplies(params *slack.GetConversationRepliesParameters) (msgs []slack.Message, hasMore bool, nextCursor string, err error)
}

type FaqItem struct {
	Question  string   `json:"question"`
	Timestamp string   `json:"timestamp"`
	Author    string   `json:"author"`
	Answers   []Answer `json:"answers"`
}

//TODO(sgoeddel): We probably need a "contributing info" emoji and section as well for when the question isn't entirely summarized in one prompt
//TODO(sgoeddel): It would also be good to link to the original full thread for additional context

type Answer struct {
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
	Body      string `json:"body"`
}

func FAQHandler(client helpdeskFAQClient, kubeClient kubernetes.Interface, forumChannelId string) events.PartialHandler {
	return events.PartialHandlerFunc("helpdesk",
		func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
			log := logger.WithField("handler", "helpdesk-faq")
			log.Debug("checking event payload")

			if callback.Type != slackevents.CallbackEvent {
				return false, nil
			}

			event, added := callback.InnerEvent.Data.(*slackevents.ReactionAddedEvent)
			if added {
				if event.Item.Channel != forumChannelId {
					log.Debugf("not in correct channel. wanted: %s, reaction was in: %s", forumChannelId, event.Item.Channel)
					return false, nil
				}
				return handleReactionAdded(event, client, kubeClient, forumChannelId, log)

			} else {
				event, removed := callback.InnerEvent.Data.(*slackevents.ReactionRemovedEvent)
				if removed {
					if event.Item.Channel != forumChannelId {
						log.Debugf("not in correct channel. wanted: %s, reaction was in: %s", forumChannelId, event.Item.Channel)
						return false, nil
					}
					return handleReactionRemoved(event, client, kubeClient, forumChannelId, log)
				} else {
					return false, nil
				}
			}
		})
}

func handleReactionRemoved(event *slackevents.ReactionRemovedEvent, client helpdeskFAQClient, kubeClient kubernetes.Interface, forumChannelId string, logger *logrus.Entry) (bool, error) {
	logger.Debugf("%s emoji removed from message", event.Reaction)
	switch event.Reaction {
	case questionReaction:
		questionLog := logger.WithField("type", "remove-question")
		configMap, err := getConfigMap(kubeClient)
		if err != nil {
			questionLog.WithError(err).Error("unable to get helpdesk-faq config map for modification")
			return false, err
		}
		messageTs := event.Item.Timestamp
		delete(configMap.Data, messageTs)
		_, err = kubeClient.CoreV1().ConfigMaps(ci).Update(context.TODO(), configMap, metav1.UpdateOptions{})
		if err != nil {
			questionLog.WithError(err).Error("unable to update helpdesk-faq config map")
			return false, fmt.Errorf("unable to update helpdesk-faq config map: %w", err)
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

			configMap, err := getConfigMap(kubeClient)
			if err != nil {
				answerLog.WithError(err).Error("unable to get helpdesk-faq config map for modification")
				return false, err
			}
			rawFaqItem := configMap.Data[questionTs]
			if rawFaqItem == "" {
				answerLog.Info("requested answer doesn't belong to an existing question, ignoring")
				return false, nil
			}

			faqItem := &FaqItem{}
			if err = json.Unmarshal([]byte(rawFaqItem), faqItem); err != nil {
				answerLog.WithError(err).Error("unable to unmarshal faqItem")
				return false, err
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
			if err := updateItemInConfigMap(kubeClient, configMap, *faqItem); err != nil {
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

func handleReactionAdded(event *slackevents.ReactionAddedEvent, client helpdeskFAQClient, kubeClient kubernetes.Interface, forumChannelId string, logger *logrus.Entry) (bool, error) {
	logger.Debugf("%s emoji added to message", event.Reaction)
	switch event.Reaction {
	case questionReaction:
		questionLog := logger.WithField("type", "add-question")
		messageTs := event.Item.Timestamp

		configMap, err := getConfigMap(kubeClient)
		if err != nil {
			questionLog.WithError(err).Error("unable to get helpdesk-faq config map for modification")
			return false, err
		}

		if configMap.Data[messageTs] != "" {
			questionLog.Info("we already have a question for this faqItem, ignoring")
			return false, nil
		}

		message, err := getTopLevelMessage(client, forumChannelId, messageTs, questionLog)
		if err != nil {
			questionLog.WithError(err).Error("unable to get top-level message")
			return false, err
		}
		if message != nil {
			faqItem := FaqItem{
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
							faqItem.Answers = append(faqItem.Answers, Answer{
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

			if err := updateItemInConfigMap(kubeClient, configMap, faqItem); err != nil {
				questionLog.WithError(err).Error("unable to update helpdesk-faq config map")
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

			configMap, err := getConfigMap(kubeClient)
			if err != nil {
				answerLog.WithError(err).Error("unable to get helpdesk-faq config map for modification")
				return false, err
			}
			rawFaqItem := configMap.Data[questionTs]
			if rawFaqItem == "" {
				answerLog.Info("requested answer doesn't belong to an existing question, ignoring")
				return false, nil
			}

			faqItem := &FaqItem{}
			if err = json.Unmarshal([]byte(rawFaqItem), faqItem); err != nil {
				answerLog.WithError(err).Error("unable to unmarshal faqItem")
				return false, err
			}

			for _, answer := range faqItem.Answers {
				if answer.Timestamp == messageTs {
					answerLog.Debug("answer already exists, ignoring")
					return false, nil
				}
			}
			faqItem.Answers = append(faqItem.Answers, Answer{
				Author:    reply.User,
				Timestamp: messageTs,
				Body:      reply.Msg.Text,
			})
			if err := updateItemInConfigMap(kubeClient, configMap, *faqItem); err != nil {
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

func getTopLevelMessage(client helpdeskFAQClient, forumChannelId string, messageTs string, logger *logrus.Entry) (*slack.Message, error) {
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

func getConfigMap(kubeClient kubernetes.Interface) (*v1.ConfigMap, error) {
	configMap, err := kubeClient.CoreV1().ConfigMaps(ci).Get(context.TODO(), faqConfigMap, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}

	return configMap, nil
}

func updateItemInConfigMap(kubeClient kubernetes.Interface, configMap *v1.ConfigMap, item FaqItem) error {
	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("unable to marshal faqItem to json: %w", err)
	}
	configMap.Data[item.Timestamp] = string(data)
	_, err = kubeClient.CoreV1().ConfigMaps(ci).Update(context.TODO(), configMap, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("unable to update helpdesk-faq config map: %w", err)
	}

	return nil
}
