package dispatcher

import (
	"sort"

	"github.com/prometheus/client_golang/prometheus"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
)

var (
	policyGenerationMetric = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "prowjob_dispatcher_policy_generation",
		Help: "Current immutable dispatcher policy generation.",
	})
	policyReadyMetric = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "prowjob_dispatcher_policy_ready",
		Help: "Whether a complete valid dispatcher policy snapshot is loaded.",
	})
	decisionMetric = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "prowjob_dispatcher_decisions_total",
		Help: "Scheduling decisions served by source and target cluster.",
	}, []string{"source", "cluster"})
	overrideStateMetric = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "prowjob_dispatcher_overrides",
		Help: "Durable runtime overrides by kind and lifecycle state.",
	}, []string{"kind", "state"})
	capabilityCoverageJobsMetric = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "prowjob_dispatcher_capability_coverage_jobs",
		Help: "Number of baseline jobs classified under each scheduling capability; __unclassified__ has none.",
	}, []string{"capability"})
	capabilityCoverageDemandMetric = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "prowjob_dispatcher_capability_coverage_demand",
		Help: "Estimated baseline demand classified under each scheduling capability; __unclassified__ has none.",
	}, []string{"capability"})
	compileDurationMetric = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "prowjob_dispatcher_snapshot_compile_seconds",
		Help:    "Time spent compiling and publishing immutable policy snapshots.",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 12),
	})
	overrideActivationMetric = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "prowjob_dispatcher_override_transition_seconds",
		Help:    "Time from override start/revocation/expiry to observed policy publication.",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
	}, []string{"transition"})
)

func init() {
	prometheus.MustRegister(
		policyGenerationMetric, policyReadyMetric, decisionMetric, overrideStateMetric,
		capabilityCoverageJobsMetric, capabilityCoverageDemandMetric,
		compileDurationMetric, overrideActivationMetric,
	)
}

func observeDecision(decision Decision) {
	decisionMetric.WithLabelValues(decision.Source, decision.Cluster).Inc()
}

func observeSnapshot(snapshot *PolicySnapshot) {
	if snapshot == nil {
		policyReadyMetric.Set(0)
		return
	}
	policyReadyMetric.Set(1)
	policyGenerationMetric.Set(float64(snapshot.Generation))
	capabilityCoverageJobsMetric.Reset()
	capabilityCoverageDemandMetric.Reset()
	jobCounts := make(map[string]float64)
	demand := make(map[string]float64)
	for _, job := range snapshot.Baseline {
		capabilities := append([]string(nil), job.Capabilities...)
		sort.Strings(capabilities)
		if len(capabilities) == 0 {
			capabilities = []string{"__unclassified__"}
		}
		for _, capability := range capabilities {
			jobCounts[capability]++
			demand[capability] += effectiveDemand(job.Demand)
		}
	}
	for capability, count := range jobCounts {
		capabilityCoverageJobsMetric.WithLabelValues(capability).Set(count)
		capabilityCoverageDemandMetric.WithLabelValues(capability).Set(demand[capability])
	}
}

func observeOverrides(overrides []dispatcherv1.DispatchOverride) {
	overrideStateMetric.Reset()
	for i := range overrides {
		overrideStateMetric.WithLabelValues(string(overrides[i].Spec.Kind), string(overrides[i].Status.State)).Inc()
	}
}
