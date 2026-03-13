package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	autoscalingv1beta1 "github.com/openshift/cluster-autoscaler-operator/pkg/apis/autoscaling/v1beta1"
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
	Workload      string            `json:"workload"`
	TotalMachines int               `json:"total_machines"`
	MachineSets   []MachineSetCount `json:"machine_sets"`
	Timestamp     time.Time         `json:"timestamp"`
}

func (e *MachinesEvent) SetTimestamp(ts time.Time) {
	e.Timestamp = ts
}

type MachinesPlugin struct {
	ctx                     context.Context
	logger                  *logrus.Entry
	mu                      sync.Mutex
	events                  []MachinesEvent
	client                  ctrlruntimeclient.Client
	autoscalers             []autoscalingv1beta1.MachineAutoscaler
	lastSnapshotPerWorkload map[string]time.Time
}

func NewMachinesPlugin(ctx context.Context, logger *logrus.Entry, client ctrlruntimeclient.Client, autoscalers []autoscalingv1beta1.MachineAutoscaler) *MachinesPlugin {
	return &MachinesPlugin{
		ctx:                     ctx,
		logger:                  logger.WithField("plugin", "machines"),
		client:                  client,
		autoscalers:             autoscalers,
		lastSnapshotPerWorkload: make(map[string]time.Time),
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

	if e.Workload == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	lastSnapshot := p.lastSnapshotPerWorkload[e.Workload]
	if now.Sub(lastSnapshot) < 5*time.Minute {
		return
	}

	snapshot := p.captureWorkloadSnapshot(e.Workload)
	p.lastSnapshotPerWorkload[e.Workload] = now
	p.logger.WithField("event", snapshot).WithField("workload", e.Workload).Debug("Recorded machines snapshot")
	p.events = append(p.events, snapshot)
}

func (p *MachinesPlugin) captureWorkloadSnapshot(workload string) MachinesEvent {
	machineSetList := &machinev1beta1.MachineSetList{}
	if err := p.client.List(p.ctx, machineSetList); err != nil {
		p.logger.WithError(err).Warn("Failed to list MachineSets")
		return MachinesEvent{Workload: workload, Timestamp: time.Now()}
	}

	var totalMachines int
	var machineSets []MachineSetCount

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

		totalMachines += current
		machineSets = append(machineSets, MachineSetCount{
			Name:       ms.Name,
			Current:    current,
			Autoscaler: autoscaler,
			Machines:   machines,
		})
	}

	return MachinesEvent{
		Workload:      workload,
		TotalMachines: totalMachines,
		MachineSets:   machineSets,
		Timestamp:     time.Now(),
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
