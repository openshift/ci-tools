package router

import (
	"cloud.google.com/go/storage"
	"github.com/slack-go/slack"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/chaibot"
	"github.com/openshift/ci-tools/pkg/jira"
	"github.com/openshift/ci-tools/pkg/slack/events"
	chaibothandler "github.com/openshift/ci-tools/pkg/slack/events/chaibot"
	"github.com/openshift/ci-tools/pkg/slack/events/helpdesk"
	"github.com/openshift/ci-tools/pkg/slack/events/joblink"
	"github.com/openshift/ci-tools/pkg/slack/events/mention"
	"github.com/openshift/ci-tools/pkg/slack/events/supportrequest"
)

// ForEvents returns a Handler that appropriately routes
// event callbacks for the handlers we know about
func ForEvents(client *slack.Client, filer jira.IssueFiler, kubeClient ctrlruntimeclient.Client, config config.Getter, gcsClient *storage.Client, keywordsConfig helpdesk.KeywordsConfig, helpdeskAlias, forumChannelId, reviewRequestWorkflowID, namespace, supportRequestChannelID string, supportRequestThreadMessageThreshold int, requireWorkflowsInForum bool, chaibotAnalyzer *chaibot.Analyzer, chaibotChannels []string) events.Handler {
	handlers := []events.PartialHandler{
		helpdesk.MessageHandler(client, keywordsConfig, helpdeskAlias, forumChannelId, reviewRequestWorkflowID, requireWorkflowsInForum),
		helpdesk.FAQHandler(client, kubeClient, forumChannelId, namespace),
		supportrequest.HandlerWithLock(client, filer, supportRequestChannelID, supportRequestThreadMessageThreshold, supportrequest.NewConfigMapLockClient(kubeClient, namespace)),
		mention.Handler(client),
		joblink.Handler(client, joblink.NewJobGetter(config), gcsClient),
	}

	// Add chaibot handler if configured
	if chaibotAnalyzer != nil && len(chaibotChannels) > 0 {
		handlers = append(handlers, chaibothandler.Handler(client, chaibotAnalyzer, chaibotChannels))
	}

	return events.MultiHandler(handlers...)
}
