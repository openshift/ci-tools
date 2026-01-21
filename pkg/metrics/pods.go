package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type PodLifecycleMetricsEvent struct {
	PodName        string     `json:"pod_name,omitempty"`
	Namespace      string     `json:"namespace,omitempty"`
	CreationTime   *time.Time `json:"creation_time,omitempty"`
	StartTime      *time.Time `json:"start_time,omitempty"`
	CompletionTime *time.Time `json:"completion_time,omitempty"`
	CIWorkload     string     `json:"ci_workload,omitempty"`

	ConditionTransitionTimes map[string]time.Time `json:"condition_transition_times,omitempty"`

	SchedulingLatency     *time.Duration `json:"scheduling_latency,omitempty"`
	InitializationLatency *time.Duration `json:"initialization_latency,omitempty"`
	ReadyLatency          *time.Duration `json:"ready_latency,omitempty"`
	CompletionLatency     *time.Duration `json:"completion_latency,omitempty"`

	PodPhase               corev1.PodPhase `json:"pod_phase,omitempty"`
	InitContainerRestarts  int             `json:"init_container_restarts,omitempty"`
	InitContainerLastError string          `json:"init_container_last_error,omitempty"`
	Timestamp              time.Time       `json:"timestamp,omitempty"`
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
	e, ok := ev.(*PodLifecycleMetricsEvent)
	if !ok {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	pod := &corev1.Pod{}
	if err := p.client.Get(p.ctx, ctrlruntimeclient.ObjectKey{Namespace: e.Namespace, Name: e.PodName}, pod); err != nil {
		p.logger.WithError(err).Errorf("Failed to get pod %s/%s for lifecycle metrics", e.Namespace, e.PodName)
		return
	}

	e.CreationTime = &pod.CreationTimestamp.Time
	e.StartTime = &pod.Status.StartTime.Time
	e.CompletionTime = getPodCompletionTime(pod)
	e.CIWorkload = pod.Labels["ci-workload"]

	// Only set pod phase if not already set by caller (preserves success/failure determination)
	if e.PodPhase == "" {
		e.PodPhase = pod.Status.Phase
	}
	e.ConditionTransitionTimes = make(map[string]time.Time)
	for _, cond := range pod.Status.Conditions {
		e.ConditionTransitionTimes[string(cond.Type)] = cond.LastTransitionTime.Time
	}

	if scheduledTime, ok := e.ConditionTransitionTimes[string(corev1.PodScheduled)]; ok && e.CreationTime != nil {
		d := scheduledTime.Sub(*e.CreationTime)
		e.SchedulingLatency = &d
	}

	if scheduledTime, ok := e.ConditionTransitionTimes[string(corev1.PodScheduled)]; ok {
		if initializedTime, ok := e.ConditionTransitionTimes[string(corev1.PodInitialized)]; ok {
			d := initializedTime.Sub(scheduledTime)
			e.InitializationLatency = &d
		}
	}

	if readyTime, ok := e.ConditionTransitionTimes[string(corev1.PodReady)]; ok && e.CreationTime != nil {
		d := readyTime.Sub(*e.CreationTime)
		e.ReadyLatency = &d
	}

	if e.CreationTime != nil && e.CompletionTime != nil {
		d := e.CompletionTime.Sub(*e.CreationTime)
		e.CompletionLatency = &d
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

func (p *PodLifecyclePlugin) Events() []MetricsEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]MetricsEvent, len(p.events))
	for i := range p.events {
		out[i] = &p.events[i]
	}
	return out
}

func getPodCompletionTime(pod *corev1.Pod) *time.Time {
	var end metav1.Time
	for _, status := range pod.Status.ContainerStatuses {
		if s := status.State.Terminated; s != nil {
			if end.IsZero() || s.FinishedAt.Time.After(end.Time) {
				end = s.FinishedAt
			}
		}
	}
	if end.IsZero() {
		for _, status := range pod.Status.InitContainerStatuses {
			if s := status.State.Terminated; s != nil && s.ExitCode != 0 {
				if end.IsZero() || s.FinishedAt.Time.After(end.Time) {
					end = s.FinishedAt
				}
			}
		}
	}
	if end.IsZero() {
		return nil
	}
	return &end.Time
}
