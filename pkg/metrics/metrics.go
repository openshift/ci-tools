package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/client-go/rest"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/prow/pkg/secretutil"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/lease"
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
	ctx    context.Context
	events chan MetricsEvent
	logger *logrus.Entry

	insightsPlugin *insightsPlugin
	buildPlugin    *buildPlugin
	nodesPlugin    *nodesMetricsPlugin
	leasePlugin    *leasesPlugin
	podPlugin      *PodLifecyclePlugin

	wg sync.WaitGroup
	mu sync.Mutex
}

// NewMetricsAgent registers the built-in plugins by default.
func NewMetricsAgent(ctx context.Context, clusterConfig *rest.Config) (*MetricsAgent, error) {
	nodesCh := make(chan string, 100)

	client, err := ctrlruntimeclient.New(clusterConfig, ctrlruntimeclient.Options{})
	if err != nil {
		return nil, err
	}
	metricsClient, err := metricsclient.NewForConfig(clusterConfig)
	if err != nil {
		return nil, err
	}

	logger := logrus.WithField("component", "metricsAgent")
	return &MetricsAgent{
		ctx:            ctx,
		events:         make(chan MetricsEvent, 100),
		insightsPlugin: newInsightsPlugin(logger),
		buildPlugin:    newBuildPlugin(ctx, logger, client),
		nodesPlugin:    newNodesMetricsPlugin(ctx, logger, client, metricsClient, nodesCh),
		leasePlugin:    newLeasesPlugin(logger),
		podPlugin:      NewPodLifecyclePlugin(ctx, logger, client),
		logger:         logger,
	}, nil
}

// Run listens for events on the events channel until the channel is closed.
func (ma *MetricsAgent) Run() {
	ma.wg.Add(1)
	defer ma.wg.Done()

	go ma.nodesPlugin.Run(ma.ctx)

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
			// Record the event to all plugins
			ma.insightsPlugin.Record(ev)
			ma.buildPlugin.Record(ev)
			ma.nodesPlugin.Record(ev)
			ma.leasePlugin.Record(ev)
			ma.podPlugin.Record(ev)
			ma.logger.WithField("event_type", fmt.Sprintf("%T", ev)).Debug("Recorded metrics event")
		}
	}
}

// Record records an event to the MetricsAgent
func (ma *MetricsAgent) Record(ev MetricsEvent) {
	if ma == nil {
		return
	}
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
	output := make(map[string]any, 5)
	output[ma.insightsPlugin.Name()] = ma.insightsPlugin.Events()
	output[ma.buildPlugin.Name()] = ma.buildPlugin.Events()
	output[ma.nodesPlugin.Name()] = ma.nodesPlugin.Events()
	output[ma.leasePlugin.Name()] = ma.leasePlugin.Events()
	output[ma.podPlugin.Name()] = ma.podPlugin.Events()

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

// AddNodeWorkload tracks a workload's pod and the node it runs on for metrics collection
func (ma *MetricsAgent) AddNodeWorkload(ctx context.Context, namespace, podName, workloadName string, podClient ctrlruntimeclient.Client) {
	if ma == nil {
		return
	}
	go ma.nodesPlugin.ExtractPodNode(ctx, namespace, podName, workloadName, podClient)
}

// RemoveNodeWorkload removes a workload from any node it's running on
func (ma *MetricsAgent) RemoveNodeWorkload(workloadName string) {
	if ma == nil {
		return
	}
	ma.nodesPlugin.RemoveWorkload(workloadName)
}

// RegisterLeaseClient provides the lease client to the agent after initialization.
func (ma *MetricsAgent) RegisterLeaseClient(client lease.Client) {
	if ma == nil || ma.leasePlugin == nil {
		return
	}
	ma.leasePlugin.SetClient(client)
}

func (ma *MetricsAgent) StorePodLifecycleMetrics(name, namespace string) {
	if ma == nil || ma.podPlugin == nil {
		return
	}
	event := PodLifecycleMetricsEvent{
		PodName:   name,
		Namespace: namespace,
	}
	ma.Record(&event)
}
