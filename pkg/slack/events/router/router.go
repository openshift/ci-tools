package router

import (
	"cloud.google.com/go/storage"
	"github.com/slack-go/slack"

	"k8s.io/test-infra/prow/config"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/slack/events"
	"github.com/openshift/ci-tools/pkg/slack/events/helpdesk"
	"github.com/openshift/ci-tools/pkg/slack/events/joblink"
	"github.com/openshift/ci-tools/pkg/slack/events/mention"
)

// ForEvents returns a Handler that appropriately routes
// event callbacks for the handlers we know about
func ForEvents(client *slack.Client, kubeClient ctrlruntimeclient.Client, config config.Getter, gcsClient *storage.Client, keywordsConfig helpdesk.KeywordsConfig, helpdeskAlias, forumChannelId, reviewRequestWorkflowID, namespace string, requireWorkflowsInForum bool) events.Handler {
	return events.MultiHandler(
		helpdesk.MessageHandler(client, keywordsConfig, helpdeskAlias, forumChannelId, reviewRequestWorkflowID, requireWorkflowsInForum),
		helpdesk.FAQHandler(client, kubeClient, forumChannelId, namespace),
		mention.Handler(client),
		joblink.Handler(client, joblink.NewJobGetter(config), gcsClient),
	)
}
