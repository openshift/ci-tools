package helpdesk

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	userv1 "github.com/openshift/api/user/v1"

	helpdeskfaq "github.com/openshift/ci-tools/pkg/helpdesk-faq"
	"github.com/openshift/ci-tools/pkg/slack/events"
)

type reaction string

const (
	question         = reaction("channel_faq")
	answer           = reaction("faq_answer")
	contributingInfo = reaction("information_source")
)

var questionRegex = regexp.MustCompile(`(?smi)^(.*?)_Topic:_(?P<topic>.*)_Subject:_(?P<subject>.*)_Contains Proprietary Information:_(?P<proprietary>.*)_Question:_(?P<body>.*)$`)

type slackClient interface {
	GetConversationHistory(params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error)
	GetConversationReplies(params *slack.GetConversationRepliesParameters) (msgs []slack.Message, hasMore bool, nextCursor string, err error)
	GetUserByEmail(email string) (*slack.User, error)
}

func FAQHandler(client slackClient, kubeClient ctrlruntimeclient.Client, forumChannelId string, namespace string) events.PartialHandler {
	// We only load the authorized users from the test-platform-ci-admins group on startup.
	// This will result in the tool needing to be restarted if this list membership changes,
	// but that is extremely infrequent, and the restart is likely to happen naturally in a timely manner anyway
	authorizedUsers, err := getAuthorizedUsers(client, kubeClient, logrus.WithField("handler", "faq-handler"))
	if err != nil {
		logrus.WithError(err).Fatalf("couldn't get authorized users")
	}
	return events.PartialHandlerFunc("helpdesk",
		func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
			log := logger.WithField("handler", "helpdesk-faq")
			log.Debug("checking event payload")

			if callback.Type != slackevents.CallbackEvent {
				return false, nil
			}

			cmClient := helpdeskfaq.NewCMClient(kubeClient, namespace, log)
			event, added := callback.InnerEvent.Data.(*slackevents.ReactionAddedEvent)
			if added {
				if event.Item.Channel != forumChannelId {
					log.Debugf("not in correct channel. wanted: %s, reaction was in: %s", forumChannelId, event.Item.Channel)
					return false, nil
				}
				return handleReactionAdded(event, client, &cmClient, forumChannelId, authorizedUsers, log)

			} else {
				event, removed := callback.InnerEvent.Data.(*slackevents.ReactionRemovedEvent)
				if removed {
					if event.Item.Channel != forumChannelId {
						log.Debugf("not in correct channel. wanted: %s, reaction was in: %s", forumChannelId, event.Item.Channel)
						return false, nil
					}
					return handleReactionRemoved(event, client, &cmClient, forumChannelId, authorizedUsers, log)
				} else {
					return false, nil
				}
			}
		})
}

func getAuthorizedUsers(client slackClient, groupClient ctrlruntimeclient.Client, logger *logrus.Entry) ([]string, error) {
	admins := &userv1.Group{}
	if err := groupClient.Get(context.TODO(), types.NamespacedName{Name: "test-platform-ci-admins"}, admins); err != nil {
		logger.WithError(err).Error("unable to get test-platform-ci-admins group")
		return nil, err
	}
	var slackUsers []string
	for _, admin := range admins.Users {
		email := fmt.Sprintf("%s@redhat.com", admin)
		user, err := client.GetUserByEmail(email)
		if err != nil {
			logger.WithError(err).Errorf("unable to get user for email: %s", email)
			continue
		}
		slackUsers = append(slackUsers, user.ID)
	}
	return slackUsers, nil
}

func handleReactionRemoved(event *slackevents.ReactionRemovedEvent, client slackClient, faqItemClient helpdeskfaq.FaqItemClient, forumChannelId string, authorizedUsers []string, logger *logrus.Entry) (bool, error) {
	logger.Debugf("%s emoji removed from message", event.Reaction)
	reactionType := reaction(event.Reaction)
	switch reactionType {
	case question:
		questionLog := logger.WithField("type", "remove-question")
		if !slices.Contains(authorizedUsers, event.User) {
			questionLog.Infof("user with ID: %s is not authorized", event.User)
			return false, nil
		}
		if err := faqItemClient.RemoveItem(event.Item.Timestamp); err != nil {
			questionLog.WithError(err).Error("unable to update helpdesk-faq config map")
			return false, err
		}
	case answer, contributingInfo:
		replyLog := logger.WithField("type", fmt.Sprintf("remove-%s", event.Reaction))
		if !slices.Contains(authorizedUsers, event.User) {
			replyLog.Infof("user with ID: %s is not authorized", event.User)
			return false, nil
		}
		messageTs := event.Item.Timestamp
		reply, err := getReply(client, forumChannelId, messageTs, logger)
		if err != nil || reply == nil {
			logger.WithError(err).Error("unable to get slack reply")
			return false, nil
		}
		questionTs := reply.Msg.ThreadTimestamp
		faqItem, err := faqItemClient.GetFAQItemIfExists(questionTs)
		if err != nil {
			replyLog.WithError(err).Debug("unable to get faqItem, it is likely the question has not been added yet")
			return false, nil //Don't return the error, because this is due to the question not having been added
		}

		if reactionType == answer {
			faqItem.Answers = removeReply(messageTs, faqItem.Answers, replyLog)
		} else {
			faqItem.ContributingInfo = removeReply(messageTs, faqItem.ContributingInfo, replyLog)
		}
		if err := faqItemClient.UpsertItem(*faqItem); err != nil {
			replyLog.WithError(err).Error("unable to update helpdesk-faq config map")
			return false, err
		}
	default:
		logger.Tracef("emoji we do not care about: %s", event.Reaction)
		return false, nil
	}

	return true, nil
}

func removeReply(messageTs string, replies []helpdeskfaq.Reply, logger *logrus.Entry) []helpdeskfaq.Reply {
	if len(replies) == 0 {
		logger.Debug("no replies of the proper type exist on faqItem")
		return replies
	}

	index := -1
	for i, r := range replies {
		if r.Timestamp == messageTs {
			index = i
			break
		}
	}
	if index >= 0 {
		replies = append(replies[:index], replies[index+1:]...)
	}

	return replies
}

func handleReactionAdded(event *slackevents.ReactionAddedEvent, client slackClient, faqItemClient helpdeskfaq.FaqItemClient, forumChannelId string, authorizedUsers []string, logger *logrus.Entry) (bool, error) {
	logger.Debugf("%s emoji added to message", event.Reaction)
	reactionType := reaction(event.Reaction)
	switch reactionType {
	case question:
		questionLog := logger.WithField("type", "add-question")
		if !slices.Contains(authorizedUsers, event.User) {
			questionLog.Infof("user with ID: %s is not authorized", event.User)
			return false, nil
		}
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
			var topic, subject, body string
			for _, match := range questionRegex.FindAllStringSubmatch(message.Text, -1) {
				topic = match[questionRegex.SubexpIndex("topic")]
				subject = match[questionRegex.SubexpIndex("subject")]
				body = match[questionRegex.SubexpIndex("body")]
			}
			if topic == "" || subject == "" || body == "" {
				questionLog.Errorf("expected to find: topic, subject, and body in question, but some values were missing")
				return false, nil
			}
			faqItem := helpdeskfaq.FaqItem{
				Question: helpdeskfaq.Question{
					Author:  message.User,
					Topic:   formatItemField(topic),
					Subject: formatItemField(subject),
					Body:    formatItemField(body),
				},
				Timestamp: messageTs,
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
					for _, r := range reply.Reactions {
						if reaction(r.Name) == answer {
							questionLog.Debugf("adding pre-marked answer with timestamp: %s", reply.Timestamp)
							faqItem.Answers = append(faqItem.Answers, helpdeskfaq.Reply{
								Author:    reply.User,
								Timestamp: reply.Timestamp,
								Body:      reply.Msg.Text,
							})
						} else if reaction(r.Name) == contributingInfo {
							questionLog.Debugf("adding pre-marked contributing-info with timestamp: %s", reply.Timestamp)
							faqItem.ContributingInfo = append(faqItem.ContributingInfo, helpdeskfaq.Reply{
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
	case answer, contributingInfo:
		replyLog := logger.WithField("type", fmt.Sprintf("add-%s", reactionType))
		if !slices.Contains(authorizedUsers, event.User) {
			replyLog.Infof("user with ID: %s is not authorized", event.User)
			return false, nil
		}
		return addReplyToExistingFaqItem(event.Item.Timestamp, reactionType, client, faqItemClient, forumChannelId, replyLog)
	default:
		logger.Tracef("emoji we do not care about: %s", event.Reaction)
		return false, nil
	}

	return true, nil
}

func addReplyToExistingFaqItem(messageTs string, reactionType reaction, client slackClient, faqItemClient helpdeskfaq.FaqItemClient, forumChannelId string, logger *logrus.Entry) (bool, error) {
	reply, err := getReply(client, forumChannelId, messageTs, logger)
	if err != nil || reply == nil {
		logger.WithError(err).Error("unable to get slack reply")
		return false, nil
	}
	questionTs := reply.Msg.ThreadTimestamp
	faqItem, err := faqItemClient.GetFAQItemIfExists(questionTs)
	if err != nil {
		logger.WithError(err).Error("unable to get faq item")
		return false, err
	}
	if faqItem == nil {
		logger.Info("requested answer doesn't belong to an existing question, ignoring")
		return false, nil
	}

	if faqItem.ReplyExists(messageTs) {
		logger.Debug("reply already exists, ignoring")
		return false, nil
	}

	switch reactionType {
	case answer:
		faqItem.Answers = append(faqItem.Answers, helpdeskfaq.Reply{
			Author:    reply.User,
			Timestamp: messageTs,
			Body:      formatItemField(reply.Msg.Text),
		})
	case contributingInfo:
		faqItem.ContributingInfo = append(faqItem.ContributingInfo, helpdeskfaq.Reply{
			Author:    reply.User,
			Timestamp: messageTs,
			Body:      formatItemField(reply.Msg.Text),
		})
	default:
		logger.Errorf("attempted to add reply for emoji we do not care about: %s", reactionType)
	}

	if err := faqItemClient.UpsertItem(*faqItem); err != nil {
		logger.WithError(err).Error("unable to update helpdesk-faq item")
		return false, err
	}

	return false, nil
}

// formatItemField removes some known special chars that slack inserts into messages in the workflows,
// and trims the field of spaces
func formatItemField(field string) string {
	field = strings.TrimSpace(field)
	field = strings.ReplaceAll(field, "\u0026gt;", "") // This "&>" is found at the beginning of many lines due to Slack workflow formatting
	// "<" and ">" are slack special formatting, see https://api.slack.com/reference/surfaces/formatting#escaping
	field = strings.ReplaceAll(field, "\u003C", "")
	field = strings.ReplaceAll(field, "\u003E", "")

	return strings.TrimSpace(field) // With the removal, there could be extra space
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

func getReply(client slackClient, forumChannelId string, messageTs string, logger *logrus.Entry) (*slack.Message, error) {
	replies, _, _, err := client.GetConversationReplies(&slack.GetConversationRepliesParameters{
		ChannelID: forumChannelId,
		Timestamp: messageTs,
		Inclusive: true,
	})
	if err != nil {
		logger.WithError(err).Error("unable to retrieve message that reaction was added for")
		return nil, err
	}
	if len(replies) == 1 {
		return &replies[0], nil
	}

	return nil, nil
}
