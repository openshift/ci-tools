package jira

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"github.com/openshift/ci-tools/pkg/jira"
)

func handleCreateJira(
	client *slack.Client,
	issueFiler jira.IssueFiler,
	threadMapping *ThreadJiraMapping,
	channelID, threadTS, userID string,
	logger *logrus.Entry,
) (bool, error) {
	log := logger.WithField("action", "create-jira")

	// Check if thread already has Jira issue
	ctx := context.Background()
	if _, exists := threadMapping.Get(ctx, threadTS); exists {
		log.Info("thread already has associated Jira issue")
		_, _, err := client.PostMessage(channelID,
			slack.MsgOptionText("This thread already has an associated Jira issue.", false),
			slack.MsgOptionTS(threadTS))
		return true, err
	}

	// Get thread content
	messages, err := GetThreadContent(client, channelID, threadTS)
	if err != nil {
		log.WithError(err).Error("failed to get thread content")
		return false, err
	}

	if len(messages) == 0 {
		log.Warn("thread has no messages")
		return false, nil
	}

	// Generate title and description (uses Shadowbot summary if available)
	title, description := GetSummary(client, channelID, threadTS, messages)

	// Create Jira issue
	issue, err := issueFiler.FileIssue("Task", title, description, userID, log)
	if err != nil {
		log.WithError(err).Error("failed to create Jira issue")
		_, _, postErr := client.PostMessage(channelID,
			slack.MsgOptionText(fmt.Sprintf("Failed to create Jira issue: %v", err), false),
			slack.MsgOptionTS(threadTS))
		return true, postErr
	}

	// Store mapping
	if err := threadMapping.Store(ctx, threadTS, issue.Key); err != nil {
		log.WithError(err).Warn("failed to store thread-jira mapping")
		// Continue anyway - issue was created
	}

	// Post confirmation
	jiraURL := fmt.Sprintf("https://issues.redhat.com/browse/%s", issue.Key)
	message := fmt.Sprintf("✅ Created Jira issue: <%s|%s>", jiraURL, issue.Key)
	_, _, err = client.PostMessage(channelID,
		slack.MsgOptionText(message, false),
		slack.MsgOptionTS(threadTS))
	if err != nil {
		log.WithError(err).Warn("failed to post confirmation message")
	}

	log.Infof("Created Jira issue %s for thread %s", issue.Key, threadTS)
	return true, nil
}
