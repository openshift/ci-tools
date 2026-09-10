package main

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// TestAuthoritativeResourceLabel checks prometheus resource label mapping.
func TestAuthoritativeResourceLabel(t *testing.T) {
	tests := []struct {
		field        corev1.ResourceName
		resourceType string
		want         string
	}{
		{corev1.ResourceCPU, "request", "cpu_request"},
		{corev1.ResourceCPU, "limit", "cpu_limit"},
		{corev1.ResourceMemory, "request", "memory_request"},
		{corev1.ResourceMemory, "limit", "memory_limit"},
		{corev1.ResourceStorage, "request", "unknown"},
	}
	for _, tt := range tests {
		got := authoritativeResourceLabel(tt.field, tt.resourceType)
		if got != tt.want {
			t.Errorf("authoritativeResourceLabel(%v, %q) = %q, want %q", tt.field, tt.resourceType, got, tt.want)
		}
	}
}

// TestNormalizeWorkloadType checks workload type label normalization.
func TestNormalizeWorkloadType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "", want: "unknown"},
		{in: "step", want: "step"},
		{in: "prowjob", want: "prowjob"},
	}
	for _, tt := range tests {
		if got := normalizeWorkloadType(tt.in); got != tt.want {
			t.Errorf("normalizeWorkloadType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestRecordAuthoritativeDecrease checks authoritative decrease metric recording.
func TestRecordAuthoritativeDecrease(t *testing.T) {
	t.Run("applied cpu request", func(t *testing.T) {
		workload := "metrics-test-applied-cpu"
		beforeDecrease := counterValue(authoritativeDecreaseTotal.WithLabelValues(decreaseModeApplied, "cpu_request", workload, "false"))
		beforeSavings := counterValue(authoritativeSavingsCPUMillicores.WithLabelValues(decreaseModeApplied, "cpu_request", workload))
		recordAuthoritativeDecrease(decreaseModeApplied, corev1.ResourceCPU, "request", workload, resource.MustParse("2"), resource.MustParse("1500m"), false)
		if counterValue(authoritativeDecreaseTotal.WithLabelValues(decreaseModeApplied, "cpu_request", workload, "false")) != beforeDecrease+1 {
			t.Fatalf("decrease counter not incremented")
		}
		if got := counterValue(authoritativeSavingsCPUMillicores.WithLabelValues(decreaseModeApplied, "cpu_request", workload)); got != beforeSavings+500 {
			t.Fatalf("cpu savings counter mismatch: got %v want %v", got, beforeSavings+500)
		}
	})
	t.Run("applied records histogram observation", func(t *testing.T) {
		workload := "metrics-test-histogram"
		beforeCount := histogramSampleCount(decreaseModeApplied, "cpu_request", workload)
		recordAuthoritativeDecrease(decreaseModeApplied, corev1.ResourceCPU, "request", workload, resource.MustParse("1000m"), resource.MustParse("800m"), false)
		if got := histogramSampleCount(decreaseModeApplied, "cpu_request", workload); got != beforeCount+1 {
			t.Fatalf("histogram sample count want %d got %d", beforeCount+1, got)
		}
	})
	t.Run("dry run memory limit capped", func(t *testing.T) {
		workload := "metrics-test-dry-run-mem"
		beforeDecrease := counterValue(authoritativeDecreaseTotal.WithLabelValues(decreaseModeDryRun, "memory_limit", workload, "true"))
		beforeSavings := counterValue(authoritativeSavingsMemoryBytes.WithLabelValues(decreaseModeDryRun, "memory_limit", workload))
		recordAuthoritativeDecrease(decreaseModeDryRun, corev1.ResourceMemory, "limit", workload, resource.MustParse("4Gi"), resource.MustParse("3Gi"), true)
		if counterValue(authoritativeDecreaseTotal.WithLabelValues(decreaseModeDryRun, "memory_limit", workload, "true")) != beforeDecrease+1 {
			t.Fatalf("dry-run decrease counter not incremented")
		}
		oneGi := resource.MustParse("1Gi")
		wantDelta := float64(oneGi.Value())
		if got := counterValue(authoritativeSavingsMemoryBytes.WithLabelValues(decreaseModeDryRun, "memory_limit", workload)); got != beforeSavings+wantDelta {
			t.Fatalf("memory savings counter mismatch: got %v want %v", got, beforeSavings+wantDelta)
		}
	})
}

func counterValue(counter prometheus.Counter) float64 {
	metric, ok := counter.(prometheus.Metric)
	if !ok {
		return 0
	}
	var dtoMetric dto.Metric
	if err := metric.Write(&dtoMetric); err != nil || dtoMetric.Counter == nil {
		return 0
	}
	return *dtoMetric.Counter.Value
}

func histogramSampleCount(mode, resourceLabel, workload string) uint64 {
	metric, ok := authoritativeReductionRatio.WithLabelValues(mode, resourceLabel, workload).(prometheus.Metric)
	if !ok {
		return 0
	}
	var dtoMetric dto.Metric
	if err := metric.Write(&dtoMetric); err != nil || dtoMetric.Histogram == nil {
		return 0
	}
	return *dtoMetric.Histogram.SampleCount
}
