package main

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
)

// planMetrics reports predictions from the last complete comparison without job or group labels.
type planMetrics struct{ values *prometheus.GaugeVec }

// newPlanMetrics registers an inventory-bounded collector on the supplied registry.
func newPlanMetrics(registry prometheus.Registerer) (*planMetrics, error) {
	values := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "prow_job_dispatcher_predicted_plan", Help: "Predicted baseline placement measurements, not measured runtime or queue savings."}, []string{"role", "provider", "cluster", "measure"})
	if err := registry.Register(values); err != nil {
		return nil, fmt.Errorf("registering plan metrics: %w", err)
	}
	return &planMetrics{values: values}, nil
}

// providerPrediction describes capacity-normalized demand over available inventory.
type providerPrediction struct {
	Provider       string             `json:"provider"`
	ClusterDemand  map[string]float64 `json:"cluster_demand"`
	NormalizedMax  float64            `json:"normalized_max"`
	NormalizedMean float64            `json:"normalized_mean"`
	MaxMeanSkew    float64            `json:"max_mean_skew"`
}

// planMovement counts assignment changes relative to the persisted generation.
type planMovement struct {
	MovedDemand float64 `json:"moved_demand"`
	MovedJobs   int     `json:"moved_jobs"`
	MovedGroups int     `json:"moved_groups"`
}

// planCoverage identifies demand with incomplete classification or outside inventory.
type planCoverage struct {
	UnknownJobs        int     `json:"unknown_jobs"`
	UnknownDemand      float64 `json:"unknown_demand"`
	UnclassifiedJobs   int     `json:"unclassified_jobs"`
	UnclassifiedDemand float64 `json:"unclassified_demand"`
}

// planEligibility exposes how much predicted demand is constrained by durable affinity.
type planEligibility struct {
	AffinityBoundJobs        int     `json:"affinity_bound_jobs"`
	AffinityBoundDemand      float64 `json:"affinity_bound_demand"`
	UnboundRelocatableJobs   int     `json:"unbound_relocatable_jobs"`
	UnboundRelocatableDemand float64 `json:"unbound_relocatable_demand"`
	SampledJobs              int     `json:"sampled_jobs"`
	FallbackDemandJobs       int     `json:"fallback_demand_jobs"`
}

// planPrediction summarizes a plan relative to the same prior assignments.
type planPrediction struct {
	Order     string               `json:"order"`
	Providers []providerPrediction `json:"providers"`
	planMovement
	SplitGroups int `json:"split_groups"`
	planCoverage
	planEligibility
}

// cloneDispatchConfig isolates mutable filename assignments, retaining compiled path expressions.
func cloneDispatchConfig(input *dispatcher.Config) *dispatcher.Config {
	copy := *input
	copy.CloudMapping = maps.Clone(input.CloudMapping)
	copy.KVM = slices.Clone(input.KVM)
	copy.NoBuilds = slices.Clone(input.NoBuilds)
	copy.ManualClusters = slices.Clone(input.ManualClusters)
	copy.Groups = make(dispatcher.JobGroups, len(input.Groups))
	for cluster, group := range input.Groups {
		group.Jobs = slices.Clone(group.Jobs)
		group.Paths = slices.Clone(group.Paths)
		group.PathREs = slices.Clone(group.PathREs)
		copy.Groups[cluster] = group
	}
	copy.BuildFarmCloud = make(map[api.Cloud][]string, len(input.BuildFarmCloud))
	for provider, clusters := range input.BuildFarmCloud {
		copy.BuildFarmCloud[provider] = slices.Clone(clusters)
	}
	copy.BuildFarm = make(map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig, len(input.BuildFarm))
	for provider, clusters := range input.BuildFarm {
		copy.BuildFarm[provider] = make(map[api.Cluster]*dispatcher.BuildFarmConfig, len(clusters))
		for cluster, assignment := range clusters {
			if assignment == nil {
				copy.BuildFarm[provider][cluster] = nil
				continue
			}
			copy.BuildFarm[provider][cluster] = &dispatcher.BuildFarmConfig{FilenamesRaw: slices.Clone(assignment.FilenamesRaw), Filenames: assignment.Filenames.Clone()}
		}
	}
	return &copy
}

// evaluateDispatchPlan publishes only the selected configuration after both plans succeed.
func evaluateDispatchPlan(o options, config *dispatcher.Config, volumes map[string]float64, blocked sets.Set[string], distribution map[string]float64, inventory dispatcher.ClusterMap, previous map[string]dispatcher.ProwJobData) (map[string]dispatcher.ProwJobData, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	files, err := loadDispatchFiles(o.prowJobConfigDir)
	if err != nil {
		return nil, err
	}
	return evaluateDispatchFiles(o, config, volumes, blocked, distribution, inventory, previous, files)
}

// evaluateDispatchFiles keeps affinity preparation and both algorithms on one input revision.
func evaluateDispatchFiles(o options, config *dispatcher.Config, volumes map[string]float64, blocked sets.Set[string], distribution map[string]float64, inventory dispatcher.ClusterMap, previous map[string]dispatcher.ProwJobData, files []fileSizeInfo) (map[string]dispatcher.ProwJobData, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	selectedConfig := cloneDispatchConfig(config)
	shadowConfig := cloneDispatchConfig(config)
	selected, err := dispatchJobsFromFiles(o.prowJobConfigDir, selectedConfig, volumes, blocked, distribution, inventory, previous, o.rebalanceHysteresisPercent, o.movementBudgetPercent, o.MinimumJobDemand, files, o.groupOrder)
	if err != nil {
		return nil, err
	}
	selectedReport := predictPlan(o.groupOrder, selected, previous, inventory, blocked)
	recordDemandCoverage(&selectedReport, selected, volumes)
	var shadowReport *planPrediction
	if o.shadowGroupOrder != "" && o.shadowGroupOrder != "disabled" {
		shadow, err := dispatchJobsFromFiles(o.prowJobConfigDir, shadowConfig, volumes, blocked, distribution, inventory, previous, o.rebalanceHysteresisPercent, o.movementBudgetPercent, o.MinimumJobDemand, files, o.shadowGroupOrder)
		if err != nil {
			return nil, fmt.Errorf("evaluating shadow plan: %w", err)
		}
		prediction := predictPlan(o.shadowGroupOrder, shadow, previous, inventory, blocked)
		recordDemandCoverage(&prediction, shadow, volumes)
		shadowReport = &prediction
	}
	config.BuildFarm = selectedConfig.BuildFarm
	logrus.WithFields(logrus.Fields{"selected": selectedReport, "shadow": shadowReport, "prediction_only": true}).Info("baseline ordering comparison")
	if o.planMetrics != nil {
		o.planMetrics.values.Reset()
		o.planMetrics.values.WithLabelValues("selected", "", "", "evaluated_timestamp_seconds").Set(float64(time.Now().Unix()))
		o.planMetrics.record("selected", selectedReport)
		if shadowReport != nil {
			o.planMetrics.record("shadow", *shadowReport)
		}
	}
	return selected, nil
}

// recordDemandCoverage distinguishes observed samples from minimum-demand fallback.
func recordDemandCoverage(report *planPrediction, assignments map[string]dispatcher.ProwJobData, volumes map[string]float64) {
	for _, name := range sortedStringKeys(assignments) {
		job := assignments[name]
		if job.Affinity.Cluster != "" {
			report.AffinityBoundJobs++
			report.AffinityBoundDemand += job.Demand
		}
		if job.PlacementKnown && job.Relocatable && job.Affinity.Cluster == "" {
			report.UnboundRelocatableJobs++
			report.UnboundRelocatableDemand += job.Demand
		}
		if sample, exists := volumes[name]; exists && sample > 0 && !math.IsInf(sample, 0) {
			report.SampledJobs++
			continue
		}
		report.FallbackDemandJobs++
	}
}

// predictPlan includes empty clusters so balance statistics cannot hide idle capacity.
func predictPlan(order string, assignments, previous map[string]dispatcher.ProwJobData, inventory dispatcher.ClusterMap, blocked sets.Set[string]) planPrediction {
	result := planPrediction{Order: order}
	loads := make(map[string]float64)
	movedGroups := sets.New[string]()
	groupClusters := make(map[string]sets.Set[string])
	for _, name := range sortedStringKeys(assignments) {
		job := assignments[name]
		loads[job.Cluster] += job.Demand
		if !job.PlacementKnown {
			result.UnknownJobs++
			result.UnknownDemand += job.Demand
		}
		if _, exists := inventory[job.Cluster]; !exists {
			result.UnclassifiedJobs++
			result.UnclassifiedDemand += job.Demand
		}
		if prior, exists := previous[name]; exists && prior.Cluster != job.Cluster {
			result.MovedJobs++
			result.MovedDemand += job.Demand
			movedGroups.Insert(job.Group)
		}
		if job.PlacementKnown && job.Relocatable && job.Group != "" {
			if groupClusters[job.Group] == nil {
				groupClusters[job.Group] = sets.New[string]()
			}
			groupClusters[job.Group].Insert(job.Cluster)
		}
	}
	result.MovedGroups = movedGroups.Len()
	for _, clusters := range groupClusters {
		if clusters.Len() > 1 {
			result.SplitGroups++
		}
	}
	providers := make(map[string]providerPrediction)
	counts := make(map[string]int)
	for _, cluster := range sortedStringKeys(inventory) {
		info := inventory[cluster]
		provider := providers[info.Provider]
		provider.Provider = info.Provider
		if provider.ClusterDemand == nil {
			provider.ClusterDemand = make(map[string]float64)
		}
		provider.ClusterDemand[cluster] = loads[cluster]
		if info.Capacity > 0 && !blocked.Has(cluster) {
			normalized := loads[cluster] / float64(info.Capacity)
			provider.NormalizedMean += normalized
			if normalized > provider.NormalizedMax {
				provider.NormalizedMax = normalized
			}
			counts[info.Provider]++
		}
		providers[info.Provider] = provider
	}
	for _, name := range sortedStringKeys(providers) {
		provider := providers[name]
		if counts[name] > 0 {
			provider.NormalizedMean /= float64(counts[name])
		}
		if provider.NormalizedMean > 0 {
			provider.MaxMeanSkew = provider.NormalizedMax / provider.NormalizedMean
		}
		result.Providers = append(result.Providers, provider)
	}
	return result
}

// record uses a fixed measurement vocabulary and inventory-bounded provider and cluster labels.
func (metrics *planMetrics) record(role string, report planPrediction) {
	for _, provider := range report.Providers {
		metrics.values.WithLabelValues(role, provider.Provider, "", "normalized_max").Set(provider.NormalizedMax)
		metrics.values.WithLabelValues(role, provider.Provider, "", "normalized_mean").Set(provider.NormalizedMean)
		metrics.values.WithLabelValues(role, provider.Provider, "", "max_mean_skew").Set(provider.MaxMeanSkew)
		for cluster, demand := range provider.ClusterDemand {
			metrics.values.WithLabelValues(role, provider.Provider, cluster, "demand").Set(demand)
		}
	}
	for measure, value := range map[string]float64{"moved_demand": report.MovedDemand, "moved_jobs": float64(report.MovedJobs), "moved_groups": float64(report.MovedGroups), "split_groups": float64(report.SplitGroups), "unknown_jobs": float64(report.UnknownJobs), "unknown_demand": report.UnknownDemand, "unclassified_jobs": float64(report.UnclassifiedJobs), "unclassified_demand": report.UnclassifiedDemand} {
		metrics.values.WithLabelValues(role, "", "", measure).Set(value)
	}
	for measure, value := range map[string]float64{"affinity_bound_jobs": float64(report.AffinityBoundJobs), "affinity_bound_demand": report.AffinityBoundDemand, "unbound_relocatable_jobs": float64(report.UnboundRelocatableJobs), "unbound_relocatable_demand": report.UnboundRelocatableDemand, "sampled_jobs": float64(report.SampledJobs), "fallback_demand_jobs": float64(report.FallbackDemandJobs)} {
		metrics.values.WithLabelValues(role, "", "", measure).Set(value)
	}
}
