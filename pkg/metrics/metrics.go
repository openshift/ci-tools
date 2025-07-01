package metrics

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	controllerruntime "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/prow/pkg/secretutil"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	CIOperatorMetricsJSON = "ci-operator-metrics.json"
)

// MetricsEvent is the interface that every metric event must implement.
type MetricsEvent interface {
	SetTimestamp(time.Time)
}

// MetricsAgent handles incoming events to each Plugin.
type MetricsAgent struct {
	ctx     context.Context
	events  chan MetricsEvent
	plugins map[string]Plugin

	wg sync.WaitGroup
	mu sync.Mutex
}

// NewMetricsAgent registers the built-in plugins by default.
func NewMetricsAgent(ctx context.Context, client controllerruntime.Client) *MetricsAgent {
	return &MetricsAgent{
		ctx:    ctx,
		events: make(chan MetricsEvent, 100),
		plugins: map[string]Plugin{
			InsightsPluginName: newInsightsPlugin(),
			BuildsPluginName:   newBuildPlugin(client, ctx),
		},
	}
}

// Run listens for events on the events channel until the channel is closed.
func (ma *MetricsAgent) Run() {
	ma.wg.Add(1)
	defer ma.wg.Done()
	for {
		ma.mu.Lock()
		ch := ma.events
		ma.mu.Unlock()

		select {
		case <-ma.ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			for _, p := range ma.plugins {
				p.Record(ev)
			}
		}
	}
}

// Record records an event to the MetricsAgent.
func (ma *MetricsAgent) Record(ev MetricsEvent) {
	ev.SetTimestamp(time.Now())
	ma.mu.Lock()
	defer ma.mu.Unlock()
	ma.events <- ev
}

// Stop closes the events channel and blocks until flush completes.
func (ma *MetricsAgent) Stop() {
	ma.mu.Lock()
	close(ma.events)
	ma.mu.Unlock()

	ma.wg.Wait()
	ma.flush()
}

// flush writes the accumulated events to a JSON file in the artifacts directory.
func (ma *MetricsAgent) flush() {
	output := make(map[string]any, len(ma.plugins))
	for _, p := range ma.plugins {
		output[p.Name()] = p.Events()
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		logrus.WithError(err).Error("failed to marshal metrics")
		return
	}
	var censor secretutil.Censorer = &noOpCensor{}
	if err := api.SaveArtifact(censor, CIOperatorMetricsJSON, data); err != nil {
		logrus.WithError(err).Error("failed to save metrics artifact")
	}
}

type noOpCensor struct{}

func (n *noOpCensor) Censor(data *[]byte) {}
