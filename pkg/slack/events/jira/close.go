package jira

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"github.com/openshift/ci-tools/pkg/jira"
)

func handleCloseJira(
	client *slack.Client,
	issueFiler jira.IssueFiler,
	threadMapping *ThreadJiraMapping,
	channelID, threadTS, userID string,
	logger *logrus.Entry,
) (bool, error) {
	log := logger.WithField("action", "close-jira")

	// Check if thread has associated Jira issue
	ctx := context.Background()
	jiraKey, exists := threadMapping.Get(ctx, threadTS)
	if !exists {
		log.Info("thread does not have associated Jira issue")
		_, _, err := client.PostMessage(channelID,
			slack.MsgOptionText("This thread does not have an associated Jira issue.", false),
			slack.MsgOptionTS(threadTS))
		return true, err
	}

	// Get thread content for final summary
	messages, err := GetThreadContent(client, channelID, threadTS)
	if err != nil {
		log.WithError(err).Error("failed to get thread content")
		return false, err
	}

	// Generate final summary
	_, description := GetSummary(client, channelID, threadTS, messages)
	finalSummary := fmt.Sprintf("Thread resolved. Final summary:\n\n%s", description)

	// Cast to IssueUpdater to access AddComment and TransitionIssue methods
	updater, ok := issueFiler.(jira.IssueUpdater)
	if !ok {
		log.Error("issueFiler does not support AddComment and TransitionIssue methods")
		_, _, postErr := client.PostMessage(channelID,
			slack.MsgOptionText("Failed to close Jira issue: issueFiler does not support required methods", false),
			slack.MsgOptionTS(threadTS))
		return true, postErr
	}

	// Add comment with final summary (private for Red Hat employees)
	if err := updater.AddComment(jiraKey, finalSummary, log); err != nil {
		log.WithError(err).Error("failed to add comment to Jira issue")
		_, _, postErr := client.PostMessage(channelID,
			slack.MsgOptionText(fmt.Sprintf("Failed to add comment to Jira issue: %v", err), false),
			slack.MsgOptionTS(threadTS))
		return true, postErr
	}

	// Transition issue to "Done"
	if err := updater.TransitionIssue(jiraKey, "Done", log); err != nil {
		log.WithError(err).Error("failed to transition Jira issue")
		_, _, postErr := client.PostMessage(channelID,
			slack.MsgOptionText(fmt.Sprintf("Failed to transition Jira issue: %v", err), false),
			slack.MsgOptionTS(threadTS))
		return true, postErr
	}

	// Post confirmation
	jiraURL := fmt.Sprintf("https://issues.redhat.com/browse/%s", jiraKey)
	message := fmt.Sprintf("✅ Closed Jira issue: <%s|%s>", jiraURL, jiraKey)
	_, _, err = client.PostMessage(channelID,
		slack.MsgOptionText(message, false),
		slack.MsgOptionTS(threadTS))
	if err != nil {
		log.WithError(err).Warn("failed to post confirmation message")
	}

	log.Infof("Closed Jira issue %s for thread %s", jiraKey, threadTS)
	return true, nil
}
