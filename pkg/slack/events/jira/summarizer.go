package jira

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

// GetShadowbotSummary retrieves Shadowbot's thread summary if available.
// Shadowbot is a Red Hat internal bot with thread summary capabilities.
func GetShadowbotSummary(client *slack.Client, channelID, threadTS string) (string, bool) {
	// Get all messages in thread
	replies, _, _, err := client.GetConversationReplies(&slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTS,
	})
	if err != nil {
		return "", false
	}

	// Find Shadowbot's summary message
	// Shadowbot posts summaries in threads - identify by bot user ID or message pattern
	for _, msg := range replies {
		// Option 1: Check by bot user ID (most reliable)
		// Get Shadowbot's bot ID from Slack workspace and use it here
		// if msg.BotID == "SHADOWBOT_BOT_ID" || msg.User == "SHADOWBOT_USER_ID" {
		//     return msg.Text, true
		// }

		// Option 2: Check by bot name or username
		// Shadowbot may post with a specific username or app name
		// if msg.Username == "Shadowbot" || msg.AppID == "SHADOWBOT_APP_ID" {
		//     return msg.Text, true
		// }

		// Option 3: Check by message pattern (fallback)
		// Shadowbot summaries may contain specific keywords or formatting
		lowerText := strings.ToLower(msg.Text)
		if (strings.Contains(lowerText, "summary") ||
			strings.Contains(lowerText, "thread summary") ||
			strings.Contains(lowerText, "private thread summary")) &&
			(msg.BotID != "" || strings.ToLower(msg.Username) == "shadowbot") {
			// Likely Shadowbot's summary - return it
			return msg.Text, true
		}
	}

	return "", false
}

// GetSummary creates a title and description, using Shadowbot summary if available.
func GetSummary(client *slack.Client, channelID, threadTS string, messages []ThreadMessage) (title, description string) {
	if len(messages) == 0 {
		return "Thread Discussion", "No messages found in thread."
	}

	// Try to get Shadowbot's summary first
	shadowbotSummary, hasSummary := GetShadowbotSummary(client, channelID, threadTS)

	// Title: First 100 chars of first message
	firstText := messages[0].Text
	title = firstText
	if len(title) > 100 {
		title = title[:100] + "..."
	}

	// Description: Use Shadowbot summary if available, otherwise use full thread content
	if hasSummary {
		description = fmt.Sprintf("Thread Summary (from Shadowbot):\n\n%s\n\n---\n\nFull Thread Content:\n\n%s",
			shadowbotSummary, FormatThreadContent(messages))
	} else {
		// Fallback: Full thread content
		description = FormatThreadContent(messages)
		description = fmt.Sprintf("Thread Discussion from #forum-ocp-testplatform\n\n%s", description)
	}

	return title, description
}
