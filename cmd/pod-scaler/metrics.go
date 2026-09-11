package main

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	decreaseModeApplied = "applied"
	decreaseModeDryRun  = "dry_run"
)

var (
	authoritativeDecreaseTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "pod_scaler_authoritative_decrease_total",
		Help: "Authoritative resource decreases considered by pod-scaler admission, by mode, resource dimension, workload type, and whether max-reduction capped the decrease.",
	}, []string{"mode", "resource", "workload_type", "reduction_capped"})

	authoritativeReductionRatio = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "pod_scaler_authoritative_reduction_ratio",
		Help:    "Fraction of configured CPU/memory removed by authoritative decrease (0-1), by mode, resource dimension, and workload type.",
		Buckets: []float64{0.05, 0.10, 0.15, 0.20, 0.25, 0.30, 0.40, 0.50},
	}, []string{"mode", "resource", "workload_type"})

	authoritativeSavingsCPUMillicores = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "pod_scaler_authoritative_savings_cpu_millicores_total",
		Help: "Cumulative CPU millicores between configured and decreased authoritative values.",
	}, []string{"mode", "resource", "workload_type"})

	authoritativeSavingsMemoryBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "pod_scaler_authoritative_savings_memory_bytes_total",
		Help: "Cumulative memory bytes between configured and decreased authoritative values.",
	}, []string{"mode", "resource", "workload_type"})
)

func init() {
	prometheus.MustRegister(
		authoritativeDecreaseTotal,
		authoritativeReductionRatio,
		authoritativeSavingsCPUMillicores,
		authoritativeSavingsMemoryBytes,
	)
}

// authoritativeResourceLabel maps admission resource fields to prometheus label values.
func authoritativeResourceLabel(field corev1.ResourceName, resourceType string) string {
	switch {
	case field == corev1.ResourceCPU && resourceType == "request":
		return "cpu_request"
	case field == corev1.ResourceCPU && resourceType == "limit":
		return "cpu_limit"
	case field == corev1.ResourceMemory && resourceType == "request":
		return "memory_request"
	case field == corev1.ResourceMemory && resourceType == "limit":
		return "memory_limit"
	default:
		return "unknown"
	}
}

// normalizeWorkloadType returns a non-empty workload type label for metrics.
func normalizeWorkloadType(workloadType string) string {
	if workloadType == "" {
		return "unknown"
	}
	return workloadType
}

// recordAuthoritativeDecrease updates authoritative decrease counters, histograms, and savings totals.
func recordAuthoritativeDecrease(mode string, field corev1.ResourceName, resourceType string, workloadType string, configured, determined resource.Quantity, reductionCapped bool) {
	resourceLabel := authoritativeResourceLabel(field, resourceType)
	workloadType = normalizeWorkloadType(workloadType)
	capped := strconv.FormatBool(reductionCapped)

	authoritativeDecreaseTotal.WithLabelValues(mode, resourceLabel, workloadType, capped).Inc()

	configuredFloat := configured.AsApproximateFloat64()
	determinedFloat := determined.AsApproximateFloat64()
	if configuredFloat > 0 && determinedFloat < configuredFloat {
		authoritativeReductionRatio.WithLabelValues(mode, resourceLabel, workloadType).Observe(1.0 - determinedFloat/configuredFloat)
	}

	savings := configured.DeepCopy()
	savings.Sub(determined)
	if savings.Sign() <= 0 {
		return
	}

	switch field {
	case corev1.ResourceCPU:
		authoritativeSavingsCPUMillicores.WithLabelValues(mode, resourceLabel, workloadType).Add(float64(savings.MilliValue()))
	case corev1.ResourceMemory:
		authoritativeSavingsMemoryBytes.WithLabelValues(mode, resourceLabel, workloadType).Add(float64(savings.Value()))
	}
}
