package metrics

import (
	"sync"
	"time"
)

// InsightsEvent defines a test platform insight event.
type InsightsEvent struct {
	Name              string         `json:"name"`
	AdditionalContext map[string]any `json:"additional_context,omitempty"`
	Timestamp         time.Time      `json:"timestamp"`
}

// SetTimestamp sets the timestamp of the event.
func (ie *InsightsEvent) SetTimestamp(t time.Time) {
	ie.Timestamp = t
}

// insightsPlugin collects and manages the insights events.
type insightsPlugin struct {
	mu     sync.Mutex
	events []MetricsEvent
}

func newInsightsPlugin() *insightsPlugin { return &insightsPlugin{} }

func (p *insightsPlugin) Name() string { return InsightsPluginName }

func (p *insightsPlugin) Record(ev MetricsEvent) {
	pe, ok := ev.(*InsightsEvent)
	if !ok {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, pe)
}

func (p *insightsPlugin) Events() []MetricsEvent {
	return p.events
}
