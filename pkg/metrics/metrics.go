package metrics

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/secretutil"

	"github.com/openshift/ci-tools/pkg/api"
)

// MetricsEvent is the interface that every metric event must implement.
type MetricsEvent interface {
	// Store appends the event to the appropriate slice in the MetricsAgent.
	Store(mc *MetricsAgent)
	// Category returns the event's category.
	Category() string

	SetTimestamp(time.Time)
}

const CI_OPERATOR_METRICS_JSON = "ci-operator-metrics.json"

// MetricsAgent collects and aggregates metrics events.
type MetricsAgent struct {
	events chan MetricsEvent
	done   chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex

	insights []InsightsEvent
	builds   []BuildEvent
}

// NewMetricsAgent creates and returns a new MetricsAgent.
func NewMetricsAgent() *MetricsAgent {
	return &MetricsAgent{
		events: make(chan MetricsEvent),
		done:   make(chan struct{}),
	}
}

// Run listens for events on the events channel until the channel is closed.
// Once the events channel is closed, we flush the collected events.
func (mc *MetricsAgent) Run() {
	mc.wg.Add(1)
	defer mc.wg.Done()
	for ev := range mc.events {
		mc.mu.Lock()
		ev.Store(mc)
		mc.mu.Unlock()
	}
	mc.flush()
}

// Record records an event to the MetricsAgent.
func (mc *MetricsAgent) Record(ev MetricsEvent) {
	ev.SetTimestamp(time.Now())
	mc.events <- ev
}

// Stop closes the events channel and blocks until flush completes.
func (mc *MetricsAgent) Stop() {
	close(mc.events)
	mc.wg.Wait()
}

// flush writes the accumulated events to a JSON file in the artifacts directory.
func (mc *MetricsAgent) flush() {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	logrus.Infof("Flushing %d insights events", len(mc.insights))

	output := map[string]any{
		"test_platform_insights": mc.insights,
		"openshift_builds":       mc.builds,
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		logrus.WithError(err).Error("Failed to marshal insights")
		return
	}
	var censor secretutil.Censorer = &noOpCensor{}
	if err := api.SaveArtifact(censor, CI_OPERATOR_METRICS_JSON, data); err != nil {
		logrus.WithError(err).Error("Failed to save insights artifact")
	}
}

type noOpCensor struct{}

func (n *noOpCensor) Censor(data *[]byte) {
	// no operation
}
