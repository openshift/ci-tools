package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	autoscalingv1beta1 "github.com/openshift/cluster-autoscaler-operator/pkg/apis/autoscaling/v1beta1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/secrets"
)

func init() {
	if err := machinev1beta1.AddToScheme(scheme.Scheme); err != nil {
		logrus.WithError(err).Error("failed to add machinev1beta1 scheme")
	}
	if err := autoscalingv1beta1.SchemeBuilder.AddToScheme(scheme.Scheme); err != nil {
		logrus.WithError(err).Error("failed to add autoscalingv1beta1 scheme")
	}
}

const (
	CIOperatorMetricsJSON = "ci-operator-metrics.json"
	CIWorkloadLabel       = "ci-workload"
	MachineAPINamespace   = "openshift-machine-api"
	MachineSetLabel       = "machine.openshift.io/cluster-api-machineset"
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
	client ctrlruntimeclient.Client
	censor *secrets.DynamicCensor

	insightsPlugin *insightsPlugin
	eventsPlugin   *eventsPlugin
	buildPlugin    *buildPlugin
	nodesPlugin    *nodesMetricsPlugin
	leasePlugin    *leasesPlugin
	podPlugin      *PodLifecyclePlugin
	machinesPlugin *MachinesPlugin
	imagesPlugin   *imagesPlugin

	wg sync.WaitGroup
	mu sync.Mutex
}

// NewMetricsAgent registers the built-in plugins by default.
func NewMetricsAgent(ctx context.Context, clusterConfig *rest.Config, censor *secrets.DynamicCensor) (*MetricsAgent, error) {
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

	autoscalerList := &autoscalingv1beta1.MachineAutoscalerList{}
	if err := client.List(ctx, autoscalerList, ctrlruntimeclient.InNamespace(MachineAPINamespace)); err != nil {
		logger.WithError(err).Warn("Failed to list MachineAutoscalers at initialization")
	}

	return &MetricsAgent{
		ctx:            ctx,
		events:         make(chan MetricsEvent, 100),
		logger:         logger,
		client:         client,
		censor:         censor,
		insightsPlugin: newInsightsPlugin(logger),
		eventsPlugin:   newEventsPlugin(logger),
		buildPlugin:    newBuildPlugin(ctx, logger, client),
		nodesPlugin:    newNodesMetricsPlugin(ctx, logger, client, metricsClient, nodesCh),
		leasePlugin:    newLeasesPlugin(logger),
		podPlugin:      NewPodLifecyclePlugin(ctx, logger, client),
		machinesPlugin: NewMachinesPlugin(ctx, logger, client, autoscalerList.Items),
		imagesPlugin:   newImagesPlugin(ctx, logger, client),
	}, nil
}

// Run listens for events on the events channel until the channel is closed.
func (ma *MetricsAgent) Run() {
	ma.wg.Add(1)
	defer ma.wg.Done()

	if ma.nodesPlugin != nil {
		go ma.nodesPlugin.Run(ma.ctx)
	}

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
			ma.eventsPlugin.Record(ev)
			ma.buildPlugin.Record(ev)
			ma.nodesPlugin.Record(ev)
			ma.leasePlugin.Record(ev)
			ma.podPlugin.Record(ev)
			ma.machinesPlugin.Record(ev)
			ma.imagesPlugin.Record(ev)
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
	output := map[string]any{
		ma.insightsPlugin.Name(): ma.insightsPlugin.Events(),
		ma.eventsPlugin.Name():   ma.eventsPlugin.Events(),
		ma.buildPlugin.Name():    ma.buildPlugin.Events(),
		ma.nodesPlugin.Name():    ma.nodesPlugin.Events(),
		ma.leasePlugin.Name():    ma.leasePlugin.Events(),
		ma.imagesPlugin.Name():   ma.imagesPlugin.Events(),
		ma.podPlugin.Name():      ma.podPlugin.Events(),
		ma.machinesPlugin.Name(): ma.machinesPlugin.Events(),
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		logrus.WithError(err).Error("failed to marshal metrics")
		return
	}

	if err := api.SaveArtifact(ma.censor, CIOperatorMetricsJSON, data); err != nil {
		logrus.WithError(err).Error("failed to save metrics artifact")
	}
}

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

func (ma *MetricsAgent) StorePodLifecycleMetrics(name, namespace string, phase corev1.PodPhase) {
	if ma == nil || ma.podPlugin == nil {
		return
	}
	event := PodLifecycleMetricsEvent{
		PodName:   name,
		Namespace: namespace,
		PodPhase:  phase,
	}
	ma.Record(&event)
}

func (ma *MetricsAgent) StoreMachinesEvent(eventType MachinesEventType, obj ctrlruntimeclient.Object) {
	if ma == nil || ma.machinesPlugin == nil || obj == nil {
		return
	}
	event := MachinesEvent{
		Type:      eventType,
		PodName:   obj.GetName(),
		Namespace: obj.GetNamespace(),
		Workload:  obj.GetLabels()[CIWorkloadLabel],
	}
	ma.Record(&event)
}

// RecordStepEvent records a single Finished event for a step, including objects metadata.
func (ma *MetricsAgent) RecordStepEvent(step api.Step, objects []ctrlruntimeclient.Object, start, finish time.Time, runErr error) {
	if ma == nil {
		return
	}
	level := EventLevelInfo
	success := true
	cause := ""
	if runErr != nil {
		level = EventLevelError
		success = false
		cause = runErr.Error()
	}
	keys := map[string]any{"stepName": step.Name()}
	keys["objects"] = BuildObjectRefs(objects)

	ma.Record(&Event{
		Level:   level,
		Source:  strings.TrimPrefix(fmt.Sprintf("%T", step), "*"),
		Locator: EventLocator{Type: "Step", Name: step.Name(), Keys: keys},
		Message: EventMessage{Reason: "Finished", Cause: cause, HumanMessage: step.Description(), Annotations: map[string]any{
			"success":          success,
			"duration_seconds": finish.Sub(start).Seconds(),
		}},
		From: start,
		To:   finish,
	})
}

// RecordConfigurationInsight records configuration insight
func (ma *MetricsAgent) RecordConfigurationInsight(targets []string, promote bool, org, repo, branch, variant, baseNamespace, consoleHost, nodeName string, clusterProfiles []ClusterProfileForTarget) {
	if ma == nil {
		return
	}

	clusterID := "unknown"
	clusterVersion := &configv1.ClusterVersion{}
	if err := ma.client.Get(ma.ctx, ctrlruntimeclient.ObjectKey{Name: "version"}, clusterVersion); err != nil {
		ma.logger.WithError(err).Warn("Failed to get ClusterVersion for cluster ID")
	} else {
		clusterID = string(clusterVersion.Spec.ClusterID)
	}

	configData := Context{
		"targets":        targets,
		"promote":        promote,
		"org":            org,
		"repo":           repo,
		"branch":         branch,
		"variant":        variant,
		"base_namespace": baseNamespace,
		"cluster_info": Context{
			"console_host":     consoleHost,
			"node_name":        nodeName,
			"cluster_profiles": clusterProfiles,
			"cluster_id":       clusterID,
		},
	}

	ma.Record(NewInsightsEvent(InsightConfiguration, configData))
}

type ClusterProfileForTarget struct {
	Target      string `json:"target,omitempty"`
	ProfileName string `json:"profile_name,omitempty"`
}
