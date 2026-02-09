package router

import (
	"context"
	"fmt"

	"cloud.google.com/go/storage"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/prow/pkg/config"

	userv1 "github.com/openshift/api/user/v1"

	"github.com/openshift/ci-tools/pkg/jira"
	"github.com/openshift/ci-tools/pkg/slack/events"
	"github.com/openshift/ci-tools/pkg/slack/events/helpdesk"
	jiraevents "github.com/openshift/ci-tools/pkg/slack/events/jira"
	"github.com/openshift/ci-tools/pkg/slack/events/joblink"
	"github.com/openshift/ci-tools/pkg/slack/events/mention"
)

// ForEvents returns a Handler that appropriately routes
// event callbacks for the handlers we know about
func ForEvents(client *slack.Client, kubeClient ctrlruntimeclient.Client, config config.Getter, gcsClient *storage.Client, keywordsConfig helpdesk.KeywordsConfig, helpdeskAlias, forumChannelId, reviewRequestWorkflowID, namespace string, requireWorkflowsInForum bool, issueFiler jira.IssueFiler) events.Handler {
	// Get testplatform team members for Jira integration authorization
	// Reuse the same pattern as FAQ handler
	authorizedUsers, err := getAuthorizedUsersForJira(client, kubeClient)
	if err != nil {
		// Log warning but continue - Jira handler will just not work for anyone
		// This matches the pattern where FAQ handler would fatal, but we want to be more lenient
		// since Jira integration is optional
		authorizedUsers = []string{}
	}

	// Create thread-jira mapping storage
	threadMapping := jiraevents.NewThreadJiraMapping(kubeClient, namespace, "slack-thread-jira-mapping")

	return events.MultiHandler(
		helpdesk.MessageHandler(client, keywordsConfig, helpdeskAlias, forumChannelId, reviewRequestWorkflowID, requireWorkflowsInForum),
		helpdesk.FAQHandler(client, kubeClient, forumChannelId, namespace),
		mention.Handler(client),
		joblink.Handler(client, joblink.NewJobGetter(config), gcsClient),
		jiraevents.ReactionHandler(client, issueFiler, threadMapping, forumChannelId, authorizedUsers),
	)
}

// getAuthorizedUsersForJira retrieves authorized users from the test-platform-ci-admins group.
// This is similar to the getAuthorizedUsers function in helpdesk-faq.go.
func getAuthorizedUsersForJira(client *slack.Client, groupClient ctrlruntimeclient.Client) ([]string, error) {
	logger := logrus.WithField("handler", "jira-router")
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
