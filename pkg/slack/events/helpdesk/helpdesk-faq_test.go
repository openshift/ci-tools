package helpdesk

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	helpdeskfaq "github.com/openshift/ci-tools/pkg/helpdesk-faq"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

type fakeSlackClient struct {
	conversationHistory *slack.GetConversationHistoryResponse // We only have one existing thread
	replies             []slack.Message                       // replies can be reused for all question threads
	usersByEmail        map[string]*slack.User
}

func (c *fakeSlackClient) GetConversationHistory(params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
	return c.conversationHistory, nil
}

func (c *fakeSlackClient) GetConversationReplies(params *slack.GetConversationRepliesParameters) (msgs []slack.Message, hasMore bool, nextCursor string, err error) {
	for _, reply := range c.replies {
		if reply.Timestamp == params.Timestamp {
			return []slack.Message{reply}, false, "", nil
		}
	}

	//If none of the replies matched the param timestamp, return them all as they relate to the thread
	return c.replies, false, "", nil
}

func (c *fakeSlackClient) GetUserByEmail(email string) (*slack.User, error) {
	return c.usersByEmail[email], nil
}

func (c *fakeSlackClient) GetPermalink(params *slack.PermalinkParameters) (string, error) {
	return fmt.Sprintf("https://workspace.slack.com/%s/%s", params.Channel, params.Ts), nil
}

type fakeFaqItemClient struct {
	itemsByTimestamp map[string]*helpdeskfaq.FaqItem
	itemsAdded       []helpdeskfaq.FaqItem
	itemsRemoved     []helpdeskfaq.FaqItem
	itemsModified    []helpdeskfaq.FaqItem
}

func (c *fakeFaqItemClient) GetSerializedFAQItems() ([]string, error) {
	// We don't need this one
	return []string{}, nil
}

func (c *fakeFaqItemClient) GetFAQItemIfExists(timestamp string) (*helpdeskfaq.FaqItem, error) {
	return c.itemsByTimestamp[timestamp], nil
}

func (c *fakeFaqItemClient) UpsertItem(item helpdeskfaq.FaqItem) error {
	_, exists := c.itemsByTimestamp[item.Timestamp]
	if exists {
		c.itemsModified = append(c.itemsModified, item)
	} else {
		c.itemsAdded = append(c.itemsAdded, item)
	}

	return nil
}

func (c *fakeFaqItemClient) RemoveItem(timestamp string) error {
	c.itemsRemoved = append(c.itemsRemoved, *c.itemsByTimestamp[timestamp])
	return nil
}

func TestHandleReactionAdded(t *testing.T) {
	newQuestionTS := "123468.1256"
	existingQuestionTS := "123456.1234"
	answerTs := "123469.1256"
	contributingInfoTs := "123473.1256"

	sc := fakeSlackClient{
		conversationHistory: &slack.GetConversationHistoryResponse{
			Messages: []slack.Message{
				{
					Msg: slack.Msg{
						Text: "@dptp-helpdesk, \n@userc\n has asked a question:\n_Topic:_\nOther\n_Subject:_\nSome Subject\n_Contains Proprietary Information:_\nNo (for most questions)\n_Question:_\n this is the body",
						User: "userc",
					},
				},
			},
		},
		replies: []slack.Message{
			{
				Msg: slack.Msg{
					Text:            "this is an answer",
					Reactions:       []slack.ItemReaction{{Name: string(answer)}},
					User:            "usera",
					Timestamp:       answerTs,
					ThreadTimestamp: existingQuestionTS,
				},
			},
			{
				Msg: slack.Msg{
					Text:            "this is some contributing info",
					Reactions:       []slack.ItemReaction{{Name: string(contributingInfo)}},
					User:            "userc",
					Timestamp:       contributingInfoTs,
					ThreadTimestamp: existingQuestionTS,
				},
			},
			{
				Msg: slack.Msg{
					Text:            "..and this is nothing",
					User:            "usera",
					ThreadTimestamp: existingQuestionTS,
				},
			},
		},
	}

	testCases := []struct {
		name                  string
		event                 slackevents.ReactionAddedEvent
		authorizedUsers       []string
		expectedToActUpon     bool
		expectedErr           error
		expectedAddedItems    []helpdeskfaq.FaqItem
		expectedModifiedItems []helpdeskfaq.FaqItem
	}{
		{
			name: "add a new faq-item",
			event: slackevents.ReactionAddedEvent{
				User:           "usera",
				Reaction:       string(question),
				EventTimestamp: newQuestionTS,
				Item: slackevents.Item{
					Timestamp: newQuestionTS,
				},
			},
			authorizedUsers:   []string{"usera", "userb"},
			expectedToActUpon: true,
			expectedAddedItems: []helpdeskfaq.FaqItem{{
				Question: helpdeskfaq.Question{
					Author:  "userc",
					Topic:   "Other",
					Subject: "Some Subject",
					Body:    "this is the body",
				},
				Timestamp:  newQuestionTS,
				ThreadLink: fmt.Sprintf("https://workspace.slack.com/helpdesk/%s", newQuestionTS),
				Answers: []helpdeskfaq.Reply{{
					Author:    "usera",
					Timestamp: answerTs,
					Body:      "this is an answer",
				}},
				ContributingInfo: []helpdeskfaq.Reply{{
					Author:    "userc",
					Timestamp: contributingInfoTs,
					Body:      "this is some contributing info",
				}},
			}},
		},
		{
			name: "unauthorized user attempts to add an item",
			event: slackevents.ReactionAddedEvent{
				User:           "userc",
				Reaction:       string(question),
				EventTimestamp: newQuestionTS,
				Item: slackevents.Item{
					Timestamp: newQuestionTS,
				},
			},
			authorizedUsers:   []string{"usera", "userb"},
			expectedToActUpon: false,
		},
		{
			name: "attempts to add an item that already exists",
			event: slackevents.ReactionAddedEvent{
				User:           "usera",
				Reaction:       string(question),
				EventTimestamp: existingQuestionTS,
				Item: slackevents.Item{
					Timestamp: existingQuestionTS,
				},
			},
			authorizedUsers:   []string{"usera", "userb"},
			expectedToActUpon: false,
		},
		{
			name: "add answer to existing item",
			event: slackevents.ReactionAddedEvent{
				User:           "usera",
				Reaction:       string(answer),
				EventTimestamp: answerTs,
				Item: slackevents.Item{
					Timestamp: answerTs,
				},
			},
			authorizedUsers:   []string{"usera", "userb"},
			expectedToActUpon: true,
			expectedModifiedItems: []helpdeskfaq.FaqItem{{
				Question: helpdeskfaq.Question{
					Author:  "SOMEONE",
					Topic:   "Something",
					Subject: "A Subject",
					Body:    "This is an existing question",
				},
				Timestamp:  existingQuestionTS,
				ThreadLink: fmt.Sprintf("https://workspace.slack.com/helpdesk/%s", existingQuestionTS),
				Answers: []helpdeskfaq.Reply{{
					Author:    "usera",
					Timestamp: answerTs,
					Body:      "this is an answer",
				}},
			}},
		},
		{
			name: "unauthorized user attempts to add answer",
			event: slackevents.ReactionAddedEvent{
				User:           "userc",
				Reaction:       string(answer),
				EventTimestamp: answerTs,
				Item: slackevents.Item{
					Timestamp: answerTs,
				},
			},
			authorizedUsers:   []string{"usera", "userb"},
			expectedToActUpon: false,
		},
		{
			name: "add contributing info to existing item",
			event: slackevents.ReactionAddedEvent{
				User:           "usera",
				Reaction:       string(contributingInfo),
				EventTimestamp: contributingInfoTs,
				Item: slackevents.Item{
					Timestamp: contributingInfoTs,
				},
			},
			authorizedUsers:   []string{"usera", "userb"},
			expectedToActUpon: true,
			expectedModifiedItems: []helpdeskfaq.FaqItem{{
				Question: helpdeskfaq.Question{
					Author:  "SOMEONE",
					Topic:   "Something",
					Subject: "A Subject",
					Body:    "This is an existing question",
				},
				Timestamp:  existingQuestionTS,
				ThreadLink: fmt.Sprintf("https://workspace.slack.com/helpdesk/%s", existingQuestionTS),
				ContributingInfo: []helpdeskfaq.Reply{{
					Author:    "userc",
					Timestamp: contributingInfoTs,
					Body:      "this is some contributing info",
				}},
			}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fic := fakeFaqItemClient{
				itemsByTimestamp: map[string]*helpdeskfaq.FaqItem{
					"123456.1234": {
						Question: helpdeskfaq.Question{
							Author:  "SOMEONE",
							Topic:   "Something",
							Subject: "A Subject",
							Body:    "This is an existing question",
						},
						Timestamp:  existingQuestionTS,
						ThreadLink: fmt.Sprintf("https://workspace.slack.com/helpdesk/%s", existingQuestionTS),
					},
				},
			}
			actedUpon, err := handleReactionAdded(&tc.event, &sc, &fic, "helpdesk", tc.authorizedUsers, logrus.NewEntry(logrus.StandardLogger()))
			if actedUpon != tc.expectedToActUpon {
				t.Fatalf("actedUpon doesn't match expected")
			}
			if diff := cmp.Diff(err, tc.expectedErr, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error doesn't match expectedErr, diff: %s", diff)
			}
			if diff := cmp.Diff(fic.itemsAdded, tc.expectedAddedItems); diff != "" {
				t.Fatalf("actual added items doesn't match expectedAddedItems, diff: %s", diff)
			}
			if diff := cmp.Diff(fic.itemsModified, tc.expectedModifiedItems); diff != "" {
				t.Fatalf("actual modified items doesn't match expectedModifiedItems, diff: %s", diff)
			}
		})
	}
}

func TestHandleReactionRemoved(t *testing.T) {
	existingQuestionTS := "123456.1234"
	answerTs := "123469.1256"
	contributingInfoTs := "123473.1256"

	sc := fakeSlackClient{
		conversationHistory: &slack.GetConversationHistoryResponse{
			Messages: []slack.Message{
				{
					Msg: slack.Msg{
						Text: "@dptp-helpdesk, \n@userc\n has asked a question:\n_Topic:_\nOther\n_Subject:_\nSome Subject\n_Contains Proprietary Information:_\nNo (for most questions)\n_Question:_\n this is the body",
						User: "userc",
					},
				},
			},
		},
		replies: []slack.Message{
			{
				Msg: slack.Msg{
					Text:            "this is an answer",
					Reactions:       []slack.ItemReaction{{Name: string(answer)}},
					User:            "usera",
					Timestamp:       answerTs,
					ThreadTimestamp: existingQuestionTS,
				},
			},
			{
				Msg: slack.Msg{
					Text:            "this is some contributing info",
					Reactions:       []slack.ItemReaction{{Name: string(contributingInfo)}},
					User:            "userc",
					Timestamp:       contributingInfoTs,
					ThreadTimestamp: existingQuestionTS,
				},
			},
			{
				Msg: slack.Msg{
					Text:            "..and this is nothing",
					User:            "usera",
					ThreadTimestamp: existingQuestionTS,
				},
			},
		},
	}
	testCases := []struct {
		name                  string
		event                 slackevents.ReactionRemovedEvent
		authorizedUsers       []string
		expectedToActUpon     bool
		expectedErr           error
		expectedRemovedItems  []helpdeskfaq.FaqItem
		expectedModifiedItems []helpdeskfaq.FaqItem
	}{
		{
			name: "item removed",
			event: slackevents.ReactionRemovedEvent{
				User:           "usera",
				Reaction:       string(question),
				EventTimestamp: existingQuestionTS,
				Item: slackevents.Item{
					Timestamp: existingQuestionTS,
				},
			},
			authorizedUsers:   []string{"usera", "userb"},
			expectedToActUpon: true,
			expectedRemovedItems: []helpdeskfaq.FaqItem{{
				Question: helpdeskfaq.Question{
					Author:  "SOMEONE",
					Topic:   "Something",
					Subject: "A Subject",
					Body:    "This is an existing question",
				},
				Timestamp:  existingQuestionTS,
				ThreadLink: fmt.Sprintf("https://workspace.slack.com/helpdesk/%s", existingQuestionTS),
				Answers: []helpdeskfaq.Reply{
					{
						Body:      "this is an answer",
						Author:    "USER",
						Timestamp: answerTs,
					},
					{
						Body:      "this is another part",
						Author:    "USER",
						Timestamp: "123459.1234",
					},
				},
				ContributingInfo: []helpdeskfaq.Reply{
					{
						Body:      "this is contributing info",
						Author:    "SOMEONE",
						Timestamp: contributingInfoTs,
					},
					{
						Body:      "this is another part",
						Author:    "SOMEONE",
						Timestamp: "123462.1234",
					},
				},
			}},
		},
		{
			name: "unauthorized user attempts to remove item",
			event: slackevents.ReactionRemovedEvent{
				User:           "userc",
				Reaction:       string(question),
				EventTimestamp: existingQuestionTS,
				Item: slackevents.Item{
					Timestamp: existingQuestionTS,
				},
			},
			authorizedUsers:   []string{"usera", "userb"},
			expectedToActUpon: false,
		},
		{
			name: "answer removed",
			event: slackevents.ReactionRemovedEvent{
				User:           "usera",
				Reaction:       string(answer),
				EventTimestamp: answerTs,
				Item: slackevents.Item{
					Timestamp: answerTs,
				},
			},
			authorizedUsers:   []string{"usera", "userb"},
			expectedToActUpon: true,
			expectedModifiedItems: []helpdeskfaq.FaqItem{{
				Question: helpdeskfaq.Question{
					Author:  "SOMEONE",
					Topic:   "Something",
					Subject: "A Subject",
					Body:    "This is an existing question",
				},
				Timestamp:  existingQuestionTS,
				ThreadLink: fmt.Sprintf("https://workspace.slack.com/helpdesk/%s", existingQuestionTS),
				Answers: []helpdeskfaq.Reply{
					{
						Body:      "this is another part",
						Author:    "USER",
						Timestamp: "123459.1234",
					},
				},
				ContributingInfo: []helpdeskfaq.Reply{
					{
						Body:      "this is contributing info",
						Author:    "SOMEONE",
						Timestamp: contributingInfoTs,
					},
					{
						Body:      "this is another part",
						Author:    "SOMEONE",
						Timestamp: "123462.1234",
					},
				},
			}},
		},
		{
			name: "attempt to remove reply by unauthorized user",
			event: slackevents.ReactionRemovedEvent{
				User:           "userc",
				Reaction:       string(answer),
				EventTimestamp: answerTs,
				Item: slackevents.Item{
					Timestamp: answerTs,
				},
			},
			authorizedUsers:   []string{"usera", "userb"},
			expectedToActUpon: false,
		},
		{
			name: "contributing info removed",
			event: slackevents.ReactionRemovedEvent{
				User:           "usera",
				Reaction:       string(contributingInfo),
				EventTimestamp: contributingInfoTs,
				Item: slackevents.Item{
					Timestamp: contributingInfoTs,
				},
			},
			authorizedUsers:   []string{"usera", "userb"},
			expectedToActUpon: true,
			expectedModifiedItems: []helpdeskfaq.FaqItem{{
				Question: helpdeskfaq.Question{
					Author:  "SOMEONE",
					Topic:   "Something",
					Subject: "A Subject",
					Body:    "This is an existing question",
				},
				Timestamp:  existingQuestionTS,
				ThreadLink: fmt.Sprintf("https://workspace.slack.com/helpdesk/%s", existingQuestionTS),
				Answers: []helpdeskfaq.Reply{
					{
						Body:      "this is an answer",
						Author:    "USER",
						Timestamp: answerTs,
					},
					{
						Body:      "this is another part",
						Author:    "USER",
						Timestamp: "123459.1234",
					},
				},
				ContributingInfo: []helpdeskfaq.Reply{
					{
						Body:      "this is another part",
						Author:    "SOMEONE",
						Timestamp: "123462.1234",
					},
				},
			}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fic := fakeFaqItemClient{
				itemsByTimestamp: map[string]*helpdeskfaq.FaqItem{
					"123456.1234": {
						Question: helpdeskfaq.Question{
							Author:  "SOMEONE",
							Topic:   "Something",
							Subject: "A Subject",
							Body:    "This is an existing question",
						},
						Timestamp:  existingQuestionTS,
						ThreadLink: fmt.Sprintf("https://workspace.slack.com/helpdesk/%s", existingQuestionTS),
						Answers: []helpdeskfaq.Reply{
							{
								Body:      "this is an answer",
								Author:    "USER",
								Timestamp: answerTs,
							},
							{
								Body:      "this is another part",
								Author:    "USER",
								Timestamp: "123459.1234",
							},
						},
						ContributingInfo: []helpdeskfaq.Reply{
							{
								Body:      "this is contributing info",
								Author:    "SOMEONE",
								Timestamp: contributingInfoTs,
							},
							{
								Body:      "this is another part",
								Author:    "SOMEONE",
								Timestamp: "123462.1234",
							},
						},
					},
				},
			}
			actedUpon, err := handleReactionRemoved(&tc.event, &sc, &fic, "helpdesk", tc.authorizedUsers, logrus.NewEntry(logrus.StandardLogger()))
			if actedUpon != tc.expectedToActUpon {
				t.Fatalf("actedUpon doesn't match expected")
			}
			if diff := cmp.Diff(err, tc.expectedErr, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error doesn't match expectedErr, diff: %s", diff)
			}
			if diff := cmp.Diff(fic.itemsRemoved, tc.expectedRemovedItems); diff != "" {
				t.Fatalf("actual removed items doesn't match expectedRemovedItems, diff: %s", diff)
			}
			if diff := cmp.Diff(fic.itemsModified, tc.expectedModifiedItems); diff != "" {
				t.Fatalf("actual modified items doesn't match expectedModifiedItems, diff: %s", diff)
			}
		})
	}
}

func TestFormatItemField(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "trim and remove beginning formatting",
			input:    " &gt; This is a question...any tips? ",
			expected: "This is a question...any tips?",
		},
		{
			name:     "multi-line question",
			input:    " &gt; This is a question\n&gt;...any tips? ",
			expected: "This is a question\n...any tips?",
		},
		{
			name:     "slack link formatting removed",
			input:    " &gt; This is a question containing a link: <https://github.com/openshift/release/pull/1234> ",
			expected: "This is a question containing a link: https://github.com/openshift/release/pull/1234",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := formatItemField(tc.input)
			if diff := cmp.Diff(result, tc.expected); diff != "" {
				t.Fatalf("result doesn't match expected, diff: %s", diff)
			}
		})
	}
}

func TestRemoveReply(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		messageTs string
		replies   []helpdeskfaq.Reply
		expected  []helpdeskfaq.Reply
	}{
		{
			name:      "reply doesn't exist",
			messageTs: "2024-06-13T14:00:00Z",
			replies: []helpdeskfaq.Reply{
				{
					Author:    "U1234",
					Timestamp: "2024-06-14T14:00:00Z",
					Body:      "this is the only reply",
				},
			},
			expected: []helpdeskfaq.Reply{
				{
					Author:    "U1234",
					Timestamp: "2024-06-14T14:00:00Z",
					Body:      "this is the only reply",
				},
			},
		},
		{
			name:      "reply exists",
			messageTs: "2024-06-13T14:00:00Z",
			replies: []helpdeskfaq.Reply{
				{
					Author:    "U1234",
					Timestamp: "2024-06-13T14:00:00Z",
					Body:      "this is a reply",
				},
				{
					Author:    "U1234",
					Timestamp: "2024-06-14T14:00:00Z",
					Body:      "and another one",
				},
			},
			expected: []helpdeskfaq.Reply{
				{
					Author:    "U1234",
					Timestamp: "2024-06-14T14:00:00Z",
					Body:      "and another one",
				},
			},
		},
		{
			name:      "no replies",
			messageTs: "2024-06-13T14:00:00Z",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := removeReply(tc.messageTs, tc.replies, logrus.NewEntry(logrus.New()))
			if diff := cmp.Diff(result, tc.expected); diff != "" {
				t.Fatalf("result doesn't match expected, diff: %s", diff)
			}
		})
	}
}
