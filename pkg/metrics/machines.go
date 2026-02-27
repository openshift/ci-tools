package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	autoscalingv1beta1 "github.com/openshift/cluster-autoscaler-operator/pkg/apis/autoscaling/v1beta1"
)

type MachinesEventType string

const (
	PodCreation   MachinesEventType = "pod_creation"
	PodCompletion MachinesEventType = "pod_completion"
)

type MachineInfo struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
}

type AutoscalerInfo struct {
	Name string `json:"name"`
	Min  int    `json:"min"`
	Max  int    `json:"max"`
}

type MachineSetCount struct {
	Name       string          `json:"name"`
	Current    int             `json:"current"`
	Autoscaler *AutoscalerInfo `json:"autoscaler,omitempty"`
	Machines   []MachineInfo   `json:"machines,omitempty"`
}

type WorkloadNodeCount struct {
	Workload      string            `json:"workload"`
	TotalMachines int               `json:"total_machines"`
	MachineSets   []MachineSetCount `json:"machine_sets"`
}

type MachinesEvent struct {
	Type             MachinesEventType `json:"type"`
	PodName          string            `json:"pod_name"`
	Namespace        string            `json:"namespace"`
	Workload         string            `json:"workload"`
	WorkloadCapacity WorkloadNodeCount `json:"workload_capacity"`
	Timestamp        time.Time         `json:"timestamp"`
}

func (e *MachinesEvent) SetTimestamp(ts time.Time) {
	e.Timestamp = ts
}

type MachinesPlugin struct {
	ctx         context.Context
	logger      *logrus.Entry
	mu          sync.Mutex
	events      []MachinesEvent
	client      ctrlruntimeclient.Client
	autoscalers []autoscalingv1beta1.MachineAutoscaler
}

func NewMachinesPlugin(ctx context.Context, logger *logrus.Entry, client ctrlruntimeclient.Client, autoscalers []autoscalingv1beta1.MachineAutoscaler) *MachinesPlugin {
	return &MachinesPlugin{
		ctx:         ctx,
		logger:      logger.WithField("plugin", "machines"),
		client:      client,
		autoscalers: autoscalers,
	}
}

func (p *MachinesPlugin) Name() string {
	return "machines"
}

func (p *MachinesPlugin) Record(ev MetricsEvent) {
	e, ok := ev.(*MachinesEvent)
	if !ok {
		return
	}

	if e.Type == PodCreation && e.Workload == "" {
		workload, err := p.waitForWorkloadLabel(e.Namespace, e.PodName)
		if err != nil {
			p.logger.WithError(err).Warnf("Failed to get workload label for pod %s/%s", e.Namespace, e.PodName)
			return
		}
		e.Workload = workload
	}

	e.WorkloadCapacity = p.getWorkloadCounts(e.Workload)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.logger.WithField("event", e).Debug("Recorded machines event")
	p.events = append(p.events, *e)
}

func (p *MachinesPlugin) waitForWorkloadLabel(namespace, podName string) (string, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	timeout := time.After(time.Minute)
	for {
		select {
		case <-p.ctx.Done():
			return "", fmt.Errorf("context cancelled")
		case <-timeout:
			return "", fmt.Errorf("timed out waiting for ci-workload label")
		case <-ticker.C:
			pod := &corev1.Pod{}
			if err := p.client.Get(p.ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: podName}, pod); err != nil {
				p.logger.WithError(err).Debugf("Failed to get pod %s/%s while waiting for workload label", namespace, podName)
				continue
			}

			workload := pod.Labels[CIWorkloadLabel]
			if workload != "" {
				return workload, nil
			}
		}
	}
}

func (p *MachinesPlugin) Events() []MetricsEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]MetricsEvent, len(p.events))
	for i := range p.events {
		out[i] = &p.events[i]
	}
	return out
}

func (p *MachinesPlugin) getAutoscaler(machineSetName string) *AutoscalerInfo {
	for _, autoscaler := range p.autoscalers {
		if autoscaler.Spec.ScaleTargetRef.Name == machineSetName {
			return &AutoscalerInfo{
				Name: autoscaler.Name,
				Min:  int(autoscaler.Spec.MinReplicas),
				Max:  int(autoscaler.Spec.MaxReplicas),
			}
		}
	}
	return nil
}

func (p *MachinesPlugin) getWorkloadCounts(workload string) WorkloadNodeCount {
	ret := WorkloadNodeCount{Workload: workload}
	machineSetList := &machinev1beta1.MachineSetList{}
	if err := p.client.List(p.ctx, machineSetList, ctrlruntimeclient.InNamespace(MachineAPINamespace)); err != nil {
		p.logger.WithError(err).Warn("Failed to list MachineSets")
		return WorkloadNodeCount{}
	}

	for _, ms := range machineSetList.Items {
		msWorkload := ms.Spec.Template.Spec.ObjectMeta.Labels[CIWorkloadLabel]
		if msWorkload != workload {
			continue
		}

		current := int(ms.Status.Replicas)
		autoscaler := p.getAutoscaler(ms.Name)

		machineList := &machinev1beta1.MachineList{}
		if err := p.client.List(p.ctx, machineList,
			ctrlruntimeclient.InNamespace(MachineAPINamespace),
			ctrlruntimeclient.MatchingLabels{MachineSetLabel: ms.Name}); err != nil {
			p.logger.WithError(err).Warnf("Failed to list Machines for MachineSet %s", ms.Name)
			continue
		}

		var machines []MachineInfo
		for _, machine := range machineList.Items {
			phase := "Unknown"
			if machine.Status.Phase != nil {
				phase = *machine.Status.Phase
			}
			machines = append(machines, MachineInfo{
				Name:  machine.Name,
				Phase: phase,
			})
		}

		ret.TotalMachines += current
		ret.MachineSets = append(ret.MachineSets, MachineSetCount{
			Name:       ms.Name,
			Current:    current,
			Autoscaler: autoscaler,
			Machines:   machines,
		})
	}

	return ret
}
