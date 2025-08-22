package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type PodLifecycleMetricsEvent struct {
	PodName                  string               `json:"pod_name,omitempty"`
	Namespace                string               `json:"namespace,omitempty"`
	StartTime                *time.Time           `json:"start_time,omitempty"`
	PodScheduledTime         *time.Time           `json:"pod_scheduled_time,omitempty"`
	InitializedTime          *time.Time           `json:"initialized_time,omitempty"`
	ReadyToStartTime         *time.Time           `json:"ready_to_start_time,omitempty"`
	ContainersReadyTime      *time.Time           `json:"containers_ready_time,omitempty"`
	ReadyTime                *time.Time           `json:"ready_time,omitempty"`
	ConditionTransitionTimes map[string]time.Time `json:"condition_transition_times,omitempty"`

	SchedulingLatency     *time.Duration `json:"scheduling_latency,omitempty"`
	InitializationLatency *time.Duration `json:"initialization_latency,omitempty"`
	ReadyLatency          *time.Duration `json:"ready_latency,omitempty"`

	InitContainerRestarts  int       `json:"init_container_restarts,omitempty"`
	InitContainerLastError string    `json:"init_container_last_error,omitempty"`
	Timestamp              time.Time `json:"timestamp,omitempty"`
}

func (e *PodLifecycleMetricsEvent) SetTimestamp(ts time.Time) {
	e.Timestamp = ts
}

type PodLifecyclePlugin struct {
	ctx    context.Context
	logger *logrus.Entry
	mu     sync.Mutex
	events []PodLifecycleMetricsEvent
	client ctrlruntimeclient.Client
}

func NewPodLifecyclePlugin(ctx context.Context, logger *logrus.Entry, client ctrlruntimeclient.Client) *PodLifecyclePlugin {
	return &PodLifecyclePlugin{ctx: ctx, logger: logger.WithField("plugin", "pods"), client: client}
}

func (p *PodLifecyclePlugin) Name() string {
	return "pods"
}

func (p *PodLifecyclePlugin) Record(ev MetricsEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := ev.(*PodLifecycleMetricsEvent); ok {
		pod := &corev1.Pod{}
		if err := p.client.Get(p.ctx, ctrlruntimeclient.ObjectKey{Namespace: e.Namespace, Name: e.PodName}, pod); err != nil {
			p.logger.WithError(err).Errorf("Failed to get pod %s/%s for lifecycle metrics", e.Namespace, e.PodName)
			return
		}
		if pod.Status.StartTime != nil {
			e.StartTime = &pod.Status.StartTime.Time
		}

		e.ConditionTransitionTimes = make(map[string]time.Time)
		for _, cond := range pod.Status.Conditions {
			e.ConditionTransitionTimes[string(cond.Type)] = cond.LastTransitionTime.Time

			switch cond.Type {
			case corev1.PodScheduled:
				t := cond.LastTransitionTime.Time
				e.PodScheduledTime = &t
			case corev1.PodInitialized:
				t := cond.LastTransitionTime.Time
				e.InitializedTime = &t
			case corev1.ContainersReady:
				t := cond.LastTransitionTime.Time
				e.ContainersReadyTime = &t
			case corev1.PodReady:
				t := cond.LastTransitionTime.Time
				e.ReadyTime = &t
			case corev1.PodReadyToStartContainers:
				t := cond.LastTransitionTime.Time
				e.ReadyToStartTime = &t
			}
		}

		if e.StartTime != nil && e.PodScheduledTime != nil {
			d := e.PodScheduledTime.Sub(*e.StartTime)
			e.SchedulingLatency = &d
		}
		if e.PodScheduledTime != nil && e.InitializedTime != nil {
			d := e.InitializedTime.Sub(*e.PodScheduledTime)
			e.InitializationLatency = &d
		}
		if e.StartTime != nil && e.ReadyTime != nil {
			d := e.ReadyTime.Sub(*e.StartTime)
			e.ReadyLatency = &d
		}

		for _, status := range pod.Status.InitContainerStatuses {
			e.InitContainerRestarts += int(status.RestartCount)
			if status.LastTerminationState.Terminated != nil && status.LastTerminationState.Terminated.ExitCode != 0 {
				e.InitContainerLastError = status.LastTerminationState.Terminated.Reason
			}
		}

		p.logger.WithField("event", e).Debug("Recorded pod lifecycle metrics event")
		p.events = append(p.events, *e)
	}
}

func (p *PodLifecyclePlugin) Events() []MetricsEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]MetricsEvent, len(p.events))
	for i := range p.events {
		out[i] = &p.events[i]
	}
	return out
}
