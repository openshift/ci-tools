package router

import (
	"cloud.google.com/go/storage"
	"github.com/slack-go/slack"

	"k8s.io/test-infra/prow/config"

	"github.com/openshift/ci-tools/pkg/slack/events"
	"github.com/openshift/ci-tools/pkg/slack/events/helpdesk"
	"github.com/openshift/ci-tools/pkg/slack/events/joblink"
	"github.com/openshift/ci-tools/pkg/slack/events/mention"
)

// ForEvents returns a Handler that appropriately routes
// event callbacks for the handlers we know about
func ForEvents(client *slack.Client, config config.Getter, gcsClient *storage.Client) events.Handler {
	return events.MultiHandler(
		helpdesk.Handler(client),
		mention.Handler(client),
		joblink.Handler(client, joblink.NewJobGetter(config), gcsClient),
	)
}
