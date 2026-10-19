package main

import (
	"fmt"

	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
)

// planMetrics compares predicted demand without unbounded job or group labels.
type planMetrics struct {
	normalizedLoad *prometheus.GaugeVec
	movedDemand    *prometheus.GaugeVec
	evaluatedAt    prometheus.Gauge
}

// newPlanMetrics registers predictions separately from measured runtime utilization.
func newPlanMetrics(registry prometheus.Registerer) (*planMetrics, error) {
	metrics := &planMetrics{
		normalizedLoad: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "prow_job_dispatcher_predicted_normalized_load", Help: "Predicted demand divided by cluster capacity."}, []string{"role", "cluster"}),
		movedDemand:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "prow_job_dispatcher_predicted_moved_demand", Help: "Predicted demand changing cluster from the prior assignment."}, []string{"role"}),
		evaluatedAt:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "prow_job_dispatcher_plan_evaluated_timestamp_seconds", Help: "Time of the last successful selected plan evaluation."}),
	}
	for _, collector := range []prometheus.Collector{metrics.normalizedLoad, metrics.movedDemand, metrics.evaluatedAt} {
		if err := registry.Register(collector); err != nil {
			return nil, fmt.Errorf("registering plan metrics: %w", err)
		}
	}
	return metrics, nil
}

// planPrediction compares placement policies using the same demand and inventory.
type planPrediction struct {
	Order          string             `json:"order"`
	NormalizedLoad map[string]float64 `json:"normalized_load"`
	MovedDemand    float64            `json:"moved_demand"`
}

// evaluateDispatchPlan freezes file contents before evaluating either policy.
func evaluateDispatchPlan(o options, config *dispatcher.Config, volumes map[string]float64, blocked sets.Set[string], distribution map[string]float64, inventory dispatcher.ClusterMap, previous map[string]dispatcher.ProwJobData) (map[string]dispatcher.ProwJobData, error) {
	files, err := loadDispatchFiles(o.prowJobConfigDir)
	if err != nil {
		return nil, err
	}
	return evaluateDispatchFiles(o, config, volumes, blocked, distribution, inventory, previous, files)
}

// evaluateDispatchFiles publishes the selected plan even if the shadow evaluation fails.
func evaluateDispatchFiles(o options, config *dispatcher.Config, volumes map[string]float64, blocked sets.Set[string], distribution map[string]float64, inventory dispatcher.ClusterMap, previous map[string]dispatcher.ProwJobData, files []fileSizeInfo) (map[string]dispatcher.ProwJobData, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	// Dispatch replaces BuildFarm and only reads the other configuration fields.
	selectedConfig, shadowConfig := *config, *config
	selected, err := dispatchJobsFromFiles(o.prowJobConfigDir, &selectedConfig, volumes, blocked, distribution, inventory, previous, o.rebalanceHysteresisPercent, o.movementBudgetPercent, o.MinimumJobDemand, files, o.groupOrder)
	if err != nil {
		return nil, err
	}
	selectedReport := predictPlan(o.groupOrder, selected, previous, inventory, blocked)
	var shadowReport *planPrediction
	if o.shadowGroupOrder != "" && o.shadowGroupOrder != "disabled" {
		shadow, err := dispatchJobsFromFiles(o.prowJobConfigDir, &shadowConfig, volumes, blocked, distribution, inventory, previous, o.rebalanceHysteresisPercent, o.movementBudgetPercent, o.MinimumJobDemand, files, o.shadowGroupOrder)
		if err != nil {
			logrus.WithError(err).WithField("order", o.shadowGroupOrder).Error("failed to evaluate shadow placement")
		} else {
			prediction := predictPlan(o.shadowGroupOrder, shadow, previous, inventory, blocked)
			shadowReport = &prediction
		}
	}
	config.BuildFarm = selectedConfig.BuildFarm
	logrus.WithFields(logrus.Fields{"selected": selectedReport, "shadow": shadowReport}).Info("predicted baseline placement")
	if o.planMetrics != nil {
		o.planMetrics.normalizedLoad.Reset()
		o.planMetrics.movedDemand.Reset()
		o.planMetrics.evaluatedAt.SetToCurrentTime()
		o.planMetrics.record("selected", selectedReport)
		if shadowReport != nil {
			o.planMetrics.record("shadow", *shadowReport)
		}
	}
	return selected, nil
}

// predictPlan includes idle eligible clusters so the prediction exposes unused capacity.
func predictPlan(order string, assignments, previous map[string]dispatcher.ProwJobData, inventory dispatcher.ClusterMap, blocked sets.Set[string]) planPrediction {
	result := planPrediction{Order: order, NormalizedLoad: make(map[string]float64)}
	for cluster, info := range inventory {
		if info.Capacity > 0 && !blocked.Has(cluster) {
			result.NormalizedLoad[cluster] = 0
		}
	}
	for _, name := range sortedStringKeys(assignments) {
		job := assignments[name]
		if _, eligible := result.NormalizedLoad[job.Cluster]; eligible {
			result.NormalizedLoad[job.Cluster] += job.Demand / float64(inventory[job.Cluster].Capacity)
		}
		if prior, exists := previous[name]; exists && prior.Cluster != job.Cluster {
			result.MovedDemand += job.Demand
		}
	}
	return result
}

// record keeps metrics bounded by the configured cluster inventory and two plan roles.
func (metrics *planMetrics) record(role string, report planPrediction) {
	for cluster, load := range report.NormalizedLoad {
		metrics.normalizedLoad.WithLabelValues(role, cluster).Set(load)
	}
	metrics.movedDemand.WithLabelValues(role).Set(report.MovedDemand)
}
