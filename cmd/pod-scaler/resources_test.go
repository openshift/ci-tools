package main

import (
	"math"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openhistogram/circonusllhist"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
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
	request := resource.MustParse("500m")
	got := peakLimitQuantity(corev1.ResourceCPU, hist, &request, cpuCap, resource.Quantity{})
	if got == nil {
		t.Fatal("expected capped peak limit")
	}
	if got.Cmp(*resource.NewMilliQuantity(5000, resource.DecimalSI)) != 0 {
		t.Fatalf("peak limit = %v, want 5 cores", got)
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
	request := resource.MustParse("100Mi")
	got := peakLimitQuantity(corev1.ResourceMemory, hist, &request, resource.Quantity{}, memoryCap)
	if got == nil {
		t.Fatal("expected capped peak limit")
	}
	wantPeak := resource.MustParse("1000Mi")
	if got.Cmp(wantPeak) != 0 {
		t.Fatalf("peak limit = %v, want %v", got, wantPeak)
	}
	capped := memoryRequestQuantityFromHistogram(hist, 0.8, memoryCap, logrus.WithField("test", t.Name()))
	if capped != nil && capped.Memory().Cmp(memoryCap) > 0 {
		t.Fatalf("request helper should cap at %v, got %v", memoryCap, capped.Memory())
	}
}

func ptr(v float64) *float64 {
	return &v
}

func TestCPURequestQuantityFromHistogram_rejectsBelowMinimum(t *testing.T) {
	hist := circonusllhist.New()
	if err := hist.RecordValue(0.001); err != nil {
		t.Fatalf("RecordValue: %v", err)
	}
	got := cpuRequestQuantityFromHistogram(hist, 0.8, *resource.NewQuantity(10, resource.DecimalSI), logrus.WithField("test", t.Name()))
	if got != nil {
		t.Fatalf("expected nil recommendation below CPU minimum, got %v", got)
	}
}

func TestMemoryRequestQuantityFromHistogram_rejectsBelowCgroupMinimum(t *testing.T) {
	hist := circonusllhist.New()
	if err := hist.RecordValue(200000); err != nil {
		t.Fatalf("RecordValue: %v", err)
	}
	got := memoryRequestQuantityFromHistogram(hist, 0.8, resource.MustParse("20Gi"), logrus.WithField("test", t.Name()))
	if got != nil {
		t.Fatalf("expected nil recommendation below cgroup minimum, got %v", got)
	}
}

func TestMergeHistogramsForMeta(t *testing.T) {
	meta := podscaler.FullMetadata{Container: "test"}
	fp := model.Fingerprint(42)
	inner := circonusllhist.New(circonusllhist.NoLookup())
	if err := inner.RecordValue(1e8); err != nil {
		t.Fatalf("RecordValue: %v", err)
	}
	hist := circonusllhist.NewHistogramWithoutLookups(inner)

	t.Run("orphan refs only", func(t *testing.T) {
		data := &podscaler.CachedQuery{
			Data:           map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{},
			DataByMetaData: map[podscaler.FullMetadata][]podscaler.FingerprintTime{meta: {{Fingerprint: fp}}},
		}
		merged := mergeHistogramsForMeta(data, data.DataByMetaData[meta])
		if merged != nil {
			t.Fatalf("expected no merge for orphan refs, got histogram")
		}
	})

	t.Run("resolvable histogram", func(t *testing.T) {
		data := &podscaler.CachedQuery{
			Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
				fp: hist,
			},
			DataByMetaData: map[podscaler.FullMetadata][]podscaler.FingerprintTime{meta: {{Fingerprint: fp}}},
		}
		merged := mergeHistogramsForMeta(data, data.DataByMetaData[meta])
		if merged == nil {
			t.Fatalf("expected merged histogram")
		}
	})
}

func TestDigestRecommendations_replacesPerResourceSnapshot(t *testing.T) {
	meta := podscaler.FullMetadata{Container: "test"}
	fp := model.Fingerprint(7)
	inner := circonusllhist.New(circonusllhist.NoLookup())
	for i := 0; i < 20; i++ {
		if err := inner.RecordValue(1e8); err != nil {
			t.Fatalf("RecordValue: %v", err)
		}
	}
	hist := circonusllhist.NewHistogramWithoutLookups(inner)

	server := &resourceServer{
		logger:           logrus.WithField("test", t.Name()),
		byMetaData:       map[podscaler.FullMetadata]corev1.ResourceRequirements{},
		memoryRequestCap: resource.MustParse("20Gi"),
	}
	staleMeta := podscaler.FullMetadata{Container: "stale"}
	server.byMetaData[staleMeta] = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(324000, resource.BinarySI),
		},
	}

	data := &podscaler.CachedQuery{
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
			fp: hist,
		},
		DataByMetaData: map[podscaler.FullMetadata][]podscaler.FingerprintTime{
			meta: {{Fingerprint: fp, Added: time.Now()}},
		},
	}
	server.digestRecommendations(data, corev1.ResourceMemory, memRequestQuantile, func(h *circonusllhist.Histogram, quantile float64) corev1.ResourceList {
		return memoryRequestQuantityFromHistogram(h, quantile, server.memoryRequestCap, server.logger)
	})

	if _, ok := server.byMetaData[staleMeta]; ok {
		t.Fatalf("expected stale memory recommendation to be cleared on reload")
	}
	got, ok := server.recommendedRequestFor(meta)
	if !ok || got.Requests.Memory().IsZero() {
		t.Fatalf("expected fresh memory recommendation for %v, got ok=%v reqs=%v", meta, ok, got.Requests)
	}
}
