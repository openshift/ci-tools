package main

import (
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openhistogram/circonusllhist"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestQuantileValueUsable(t *testing.T) {
	testCases := []struct {
		name string
		v    float64
		want bool
	}{
		{name: "normal", v: 0.5, want: true},
		{name: "zero", v: 0, want: true},
		{name: "nan", v: math.NaN()},
		{name: "positive infinity", v: math.Inf(1)},
		{name: "negative infinity", v: math.Inf(-1)},
		{name: "negative", v: -1},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := quantileValueUsable(tc.v)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("quantileValueUsable(%v) differs from expected, diff:\n%s", tc.v, diff)
			}
		})
	}
}

func TestRecommendationValue(t *testing.T) {
	testCases := []struct {
		name        string
		sampleCount int
		sampleValue float64
		quantile    float64
		want        *float64
	}{
		{
			name:        "sparse uses max",
			sampleCount: 5,
			sampleValue: 2.5,
			quantile:    0.8,
			want:        ptr(2.6),
		},
		{
			name:     "empty histogram",
			quantile: 0.8,
		},
		{
			name:        "quantile with sufficient samples",
			sampleCount: 20,
			sampleValue: 0.5,
			quantile:    0.8,
			want:        ptr(0.508),
		},
		{
			name:        "nan samples only",
			sampleCount: 20,
			sampleValue: math.NaN(),
			quantile:    0.8,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hist := circonusllhist.New()
			for i := 0; i < tc.sampleCount; i++ {
				if err := hist.RecordValue(tc.sampleValue); err != nil {
					t.Fatalf("RecordValue: %v", err)
				}
			}
			got := recommendationValue(hist, tc.quantile)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("recommendationValue differs from expected, diff:\n%s", diff)
			}
		})
	}
}

func TestCPURequestQuantityFromHistogram(t *testing.T) {
	cpuCap := *resource.NewQuantity(10, resource.DecimalSI)

	testCases := []struct {
		name        string
		sampleCount int
		sampleValue float64
		quantile    float64
		want        corev1.ResourceList
	}{
		{
			name:        "normal usage",
			sampleCount: 20,
			sampleValue: 0.5,
			quantile:    0.8,
			want:        corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("508m")},
		},
		{
			name:     "positive infinity",
			quantile: math.Inf(1),
		},
		{
			name:     "negative infinity",
			quantile: math.Inf(-1),
		},
		{
			name:     "nan",
			quantile: math.NaN(),
		},
		{
			name:        "capped at digest",
			sampleCount: 20,
			sampleValue: 50,
			quantile:    0.8,
			want:        corev1.ResourceList{corev1.ResourceCPU: cpuCap},
		},
		{
			name:     "empty histogram",
			quantile: 0.8,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hist := circonusllhist.New()
			for i := 0; i < tc.sampleCount; i++ {
				if err := hist.RecordValue(tc.sampleValue); err != nil {
					t.Fatalf("RecordValue: %v", err)
				}
			}
			got := cpuRequestQuantityFromHistogram(hist, tc.quantile, cpuCap, logrus.WithField("test", tc.name))
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("cpuRequestQuantityFromHistogram differs from expected, diff:\n%s", diff)
			}
		})
	}
}

func TestMemoryRequestQuantityFromHistogram(t *testing.T) {
	memoryCap := resource.MustParse("20Gi")

	testCases := []struct {
		name        string
		sampleCount int
		sampleValue float64
		quantile    float64
		want        corev1.ResourceList
	}{
		{
			name:        "normal usage",
			sampleCount: 20,
			sampleValue: 1e8,
			quantile:    0.8,
			want:        corev1.ResourceList{corev1.ResourceMemory: *resource.NewQuantity(108000000, resource.BinarySI)},
		},
		{
			name:        "capped at digest",
			sampleCount: 20,
			sampleValue: 30 * 1024 * 1024 * 1024,
			quantile:    0.8,
			want:        corev1.ResourceList{corev1.ResourceMemory: memoryCap},
		},
		{
			name:     "negative infinity",
			quantile: math.Inf(-1),
		},
		{
			name:     "empty histogram",
			quantile: 0.8,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hist := circonusllhist.New()
			for i := 0; i < tc.sampleCount; i++ {
				if err := hist.RecordValue(tc.sampleValue); err != nil {
					t.Fatalf("RecordValue: %v", err)
				}
			}
			got := memoryRequestQuantityFromHistogram(hist, tc.quantile, memoryCap, logrus.WithField("test", tc.name))
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("memoryRequestQuantityFromHistogram differs from expected, diff:\n%s", diff)
			}
		})
	}
}

func TestRecommendationPeakValue(t *testing.T) {
	testCases := []struct {
		name        string
		sampleCount int
		sampleValue float64
		spikeValue  float64
		want        *float64
	}{
		{
			name:        "peak reflects spike",
			sampleCount: 20,
			sampleValue: 0.5,
			spikeValue:  5,
			want:        ptr(5.1),
		},
		{
			name: "empty histogram",
		},
		{
			name:        "rejects int64 overflow boundary",
			sampleCount: 1,
			sampleValue: float64(1 << 63),
		},
		{
			name:        "rejects negative",
			sampleCount: 1,
			sampleValue: -1,
		},
		{
			name:        "rejects nan",
			sampleCount: 1,
			sampleValue: math.NaN(),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hist := circonusllhist.New()
			for i := 0; i < tc.sampleCount; i++ {
				if err := hist.RecordValue(tc.sampleValue); err != nil {
					t.Fatalf("RecordValue: %v", err)
				}
			}
			if tc.spikeValue != 0 {
				if err := hist.RecordValue(tc.spikeValue); err != nil {
					t.Fatalf("RecordValue spike: %v", err)
				}
			}
			got := recommendationPeakValue(hist)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("recommendationPeakValue differs from expected, diff:\n%s", diff)
			}
		})
	}
}

func TestCPUPeakQuantityFromHistogram(t *testing.T) {
	cpuCap := *resource.NewQuantity(10, resource.DecimalSI)
	hist := circonusllhist.New()
	for i := 0; i < 20; i++ {
		if err := hist.RecordValue(0.5); err != nil {
			t.Fatalf("RecordValue: %v", err)
		}
	}
	if err := hist.RecordValue(50); err != nil {
		t.Fatalf("RecordValue spike: %v", err)
	}
	got := cpuPeakQuantityFromHistogram(hist)
	want := cpuQuantityFromCores(*recommendationPeakValue(hist))
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("cpuPeakQuantityFromHistogram differs from expected, diff:\n%s", diff)
	}
	if got.Cmp(cpuCap) <= 0 {
		t.Fatalf("peak quantity should exceed request cap %v, got %v", cpuCap, got)
	}
	capped := cpuRequestQuantityFromHistogram(hist, 0.8, cpuCap, logrus.WithField("test", t.Name()))
	if capped != nil && capped.Cpu().Cmp(cpuCap) > 0 {
		t.Fatalf("request helper should cap at %v, got %v", cpuCap, capped.Cpu())
	}
}

func TestMemoryPeakQuantityFromHistogram(t *testing.T) {
	memoryCap := resource.MustParse("20Gi")
	hist := circonusllhist.New()
	spikeBytes := float64(30 * 1024 * 1024 * 1024)
	for i := 0; i < 20; i++ {
		if err := hist.RecordValue(1e8); err != nil {
			t.Fatalf("RecordValue: %v", err)
		}
	}
	if err := hist.RecordValue(spikeBytes); err != nil {
		t.Fatalf("RecordValue spike: %v", err)
	}
	got := memoryPeakQuantityFromHistogram(hist)
	want := memoryQuantityFromBytes(*recommendationPeakValue(hist))
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("memoryPeakQuantityFromHistogram differs from expected, diff:\n%s", diff)
	}
	if got.Cmp(memoryCap) <= 0 {
		t.Fatalf("peak quantity should exceed request cap %v, got %v", memoryCap, got)
	}
	capped := memoryRequestQuantityFromHistogram(hist, 0.8, memoryCap, logrus.WithField("test", t.Name()))
	if capped != nil && capped.Memory().Cmp(memoryCap) > 0 {
		t.Fatalf("request helper should cap at %v, got %v", memoryCap, capped.Memory())
	}
}

func ptr(v float64) *float64 {
	return &v
}
