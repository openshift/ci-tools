package metrics

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	InsightsPluginName = "test_platform_insights"
)

type InsightEventName string

const (
	InsightStarted              InsightEventName = "started"
	InsightConfiguration        InsightEventName = "configuration"
	InsightNamespaceCreated     InsightEventName = "namespace_created"
	InsightExecutionStarted     InsightEventName = "execution_started"
	InsightExecutionCompleted   InsightEventName = "execution_completed"
	InsightStepStarted          InsightEventName = "step_started"
	InsightStepCompleted        InsightEventName = "step_completed"
	InsightSecretCreated        InsightEventName = "secret_created"
	InsightNamespaceInitialized InsightEventName = "namespace_initialized"
	InsightNamespaceArtifacts   InsightEventName = "namespace_artifacts"
	InsightLeaseCredentials     InsightEventName = "lease_credentials"
	InsightLeaseReleased        InsightEventName = "lease_released"
)

// InsightsEvent defines a test platform insight event.
type InsightsEvent struct {
	Name              string    `json:"name"`
	AdditionalContext Context   `json:"additional_context,omitempty"`
	Timestamp         time.Time `json:"timestamp"`
}

type Context map[string]any

func NewInsightsEvent(name InsightEventName, additionalContext Context) *InsightsEvent {
	return &InsightsEvent{
		Name:              string(name),
		AdditionalContext: additionalContext,
	}
}

// SetTimestamp sets the timestamp of the event.
func (ie *InsightsEvent) SetTimestamp(t time.Time) {
	ie.Timestamp = t
}

// insightsPlugin collects and manages the insights events.
type insightsPlugin struct {
	mu     sync.Mutex
	logger *logrus.Entry
	events []MetricsEvent
}

func newInsightsPlugin(logger *logrus.Entry) *insightsPlugin {
	return &insightsPlugin{logger: logger.WithField("plugin", InsightsPluginName)}
}

func (p *insightsPlugin) Name() string { return InsightsPluginName }

func (p *insightsPlugin) Record(ev MetricsEvent) {
	pe, ok := ev.(*InsightsEvent)
	if !ok {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logger.WithField("event", pe).Debug("Recording insights event")
	p.events = append(p.events, pe)
}

func (p *insightsPlugin) Events() []MetricsEvent {
	return p.events
}
