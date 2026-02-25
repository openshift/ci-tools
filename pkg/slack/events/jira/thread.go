package jira

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

// ThreadMessage represents a single message in a Slack thread.
type ThreadMessage struct {
	User      string
	UserName  string
	Text      string
	Timestamp string
}

// GetThreadContent fetches all messages in a thread and formats them.
func GetThreadContent(client *slack.Client, channelID, threadTS string) ([]ThreadMessage, error) {
	var allMessages []ThreadMessage

	cursor := ""
	for {
		replies, hasMore, nextCursor, err := client.GetConversationReplies(&slack.GetConversationRepliesParameters{
			ChannelID: channelID,
			Timestamp: threadTS,
			Cursor:    cursor,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get conversation replies: %w", err)
		}

		for _, msg := range replies {
			if msg.User != "" && msg.Text != "" {
				user, err := client.GetUserInfo(msg.User)
				userName := "Unknown"
				if err == nil && user != nil {
					userName = user.RealName
					if userName == "" {
						userName = user.Name
					}
				}
				allMessages = append(allMessages, ThreadMessage{
					User:      msg.User,
					UserName:  userName,
					Text:      msg.Text,
					Timestamp: msg.Timestamp,
				})
			}
		}

		if !hasMore {
			break
		}
		cursor = nextCursor
	}

	return allMessages, nil
}

// FormatThreadContent formats thread messages for Jira description.
func FormatThreadContent(messages []ThreadMessage) string {
	var parts []string
	for _, msg := range messages {
		parts = append(parts, fmt.Sprintf("[%s]: %s", msg.UserName, msg.Text))
	}
	return strings.Join(parts, "\n\n")
}

// GetUniqueUsers extracts unique user IDs from thread messages.
func GetUniqueUsers(messages []ThreadMessage) []string {
	userMap := make(map[string]bool)
	var users []string
	for _, msg := range messages {
		if msg.User != "" && !userMap[msg.User] {
			userMap[msg.User] = true
			users = append(users, msg.User)
		}
	}
	return users
}
