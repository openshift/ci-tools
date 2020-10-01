package router

import (
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"github.com/openshift/ci-tools/pkg/jira"
	"github.com/openshift/ci-tools/pkg/slack/interactions"
	"github.com/openshift/ci-tools/pkg/slack/modals"
	"github.com/openshift/ci-tools/pkg/slack/modals/bug"
	"github.com/openshift/ci-tools/pkg/slack/modals/consultation"
	"github.com/openshift/ci-tools/pkg/slack/modals/enhancement"
	"github.com/openshift/ci-tools/pkg/slack/modals/helpdesk"
	"github.com/openshift/ci-tools/pkg/slack/modals/incident"
	"github.com/openshift/ci-tools/pkg/slack/modals/triage"
)

// ForModals returns a Handler that appropriately routes
// interaction callbacks for the modals we know about
func ForModals(filer jira.IssueFiler, client *slack.Client) interactions.Handler {
	router := &modalRouter{
		slackClient:         client,
		viewsById:           map[modals.Identifier]slack.ModalViewRequest{},
		handlersByIdAndType: map[modals.Identifier]map[slack.InteractionType]interactions.Handler{},
	}

	toRegister := []*modals.FlowWithViewAndFollowUps{
		bug.Register(filer, client),
		consultation.Register(filer, client),
		enhancement.Register(filer, client),
		helpdesk.Register(filer, client),
		incident.Register(filer, client),
		triage.Register(filer, client),
	}

	for _, entry := range toRegister {
		router.viewsById[entry.Identifier] = entry.View
		router.handlersByIdAndType[entry.Identifier] = entry.FollowUps
	}

	return router
}

type modalRouter struct {
	slackClient slackClient

	// viewsById maps callback IDs to modal flows, for triggering
	// modals as a response to short-cut interaction events
	viewsById map[modals.Identifier]slack.ModalViewRequest
	// handlersByIdAndType holds handlers for different types of
	// interaction payloads, further mapping to identifiers we
	// store in private metadata for routing
	handlersByIdAndType map[modals.Identifier]map[slack.InteractionType]interactions.Handler
}

// Handle routes the interaction callback to the appropriate handler
func (r *modalRouter) Handle(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
	switch callback.Type {
	case slack.InteractionTypeShortcut:
		return r.openView(callback, logger)
	default:
		return r.delegate(callback, logger)
	}
}

type slackClient interface {
	OpenView(triggerID string, view slack.ModalViewRequest) (*slack.ViewResponse, error)
}

// openView reacts to the original shortcut action from the user
// to open the first modal view for them
func (r *modalRouter) openView(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
	id := modals.Identifier(callback.CallbackID)
	logger = logger.WithField("view_id", id)
	logger.Infof("Opening modal view %s.", id)
	view, exists := r.viewsById[id]
	if id != "" && !exists {
		logger.Debug("Unknown callback ID.")
		return nil, nil
	}

	response, err := r.slackClient.OpenView(callback.TriggerID, view)
	if err != nil {
		logger.WithError(err).Warn("Failed to open a modal flow.")
	}
	logger.WithField("response", response).Trace("Got a modal response.")
	return nil, nil
}

// delegate routes the interaction callback to the appropriate handler
func (r *modalRouter) delegate(callback *slack.InteractionCallback, logger *logrus.Entry) (output []byte, err error) {
	id := modals.Identifier(callback.View.PrivateMetadata)
	logger = logger.WithField("view_id", id)
	handlersForId, registered := r.handlersByIdAndType[id]
	if !registered {
		logger.Debugf("Received a callback ID (%s) for which no handlers were registered.", id)
		return nil, nil
	}
	handler, exists := handlersForId[callback.Type]
	if !exists {
		logger.Debugf("Received a callback ID (%s) and type (%s) for which no handlers were registered.", callback.Type, id)
		return nil, nil
	}
	return handler.Handle(callback, logger)
}

func (r *modalRouter) Identifier() string {
	return "modal"
}
