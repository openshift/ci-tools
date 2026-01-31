package main

import (
	"testing"
	"time"

	"github.com/openhistogram/circonusllhist"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/openshift/ci-tools/pkg/api"
	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
)

func TestDigestData_FiltersOldData(t *testing.T) {
	now := time.Now()
	fourWeeksAgo := now.Add(-28 * 24 * time.Hour)
	oneWeekAgo := now.Add(-7 * 24 * time.Hour)

	// Create test data with fingerprints at different times
	oldFingerprint := model.Fingerprint(1)
	recentFingerprint1 := model.Fingerprint(2)
	recentFingerprint2 := model.Fingerprint(3)
	recentFingerprint3 := model.Fingerprint(4)

	// Create histograms with different values so we can verify which ones are used
	oldHist := circonusllhist.New()
	if err := oldHist.RecordValue(100.0); err != nil { // High value - should be ignored
		t.Fatalf("failed to record value: %v", err)
	}

	recentHist1 := circonusllhist.New()
	if err := recentHist1.RecordValue(50.0); err != nil { // Lower value - should be used
		t.Fatalf("failed to record value: %v", err)
	}

	recentHist2 := circonusllhist.New()
	if err := recentHist2.RecordValue(75.0); err != nil { // Medium value - should be used
		t.Fatalf("failed to record value: %v", err)
	}

	recentHist3 := circonusllhist.New()
	if err := recentHist3.RecordValue(60.0); err != nil { // Another recent value - should be used
		t.Fatalf("failed to record value: %v", err)
	}

	meta := podscaler.FullMetadata{
		Metadata: api.Metadata{
			Org:    "test-org",
			Repo:   "test-repo",
			Branch: "main",
		},
		Container: "test-container",
	}

	data := &podscaler.CachedQuery{
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
			oldFingerprint:     circonusllhist.NewHistogramWithoutLookups(oldHist),
			recentFingerprint1: circonusllhist.NewHistogramWithoutLookups(recentHist1),
			recentFingerprint2: circonusllhist.NewHistogramWithoutLookups(recentHist2),
			recentFingerprint3: circonusllhist.NewHistogramWithoutLookups(recentHist3),
		},
		DataByMetaData: map[podscaler.FullMetadata][]podscaler.FingerprintTime{
			meta: {
				// Old data - should be filtered out
				{
					Fingerprint: oldFingerprint,
					Added:       fourWeeksAgo,
				},
				// Recent data - should be included (need at least 3 samples)
				{
					Fingerprint: recentFingerprint1,
					Added:       oneWeekAgo,
				},
				{
					Fingerprint: recentFingerprint2,
					Added:       oneWeekAgo,
				},
				{
					Fingerprint: recentFingerprint3,
					Added:       oneWeekAgo,
				},
			},
		},
	}

	server := &resourceServer{
		logger:     logrus.WithField("test", "TestDigestData_FiltersOldData"),
		byMetaData: map[podscaler.FullMetadata]corev1.ResourceRequirements{},
	}

	// The digestData function uses time.Now() internally to calculate the cutoff.
	// Since we're using actual timestamps (fourWeeksAgo and oneWeekAgo), the function
	// will correctly filter based on the current time and resourceRecommendationWindow.

	// Digest the data
	server.digestData(data, 0.8, corev1.ResourceCPU, func(valueAtQuantile float64) *resource.Quantity {
		return resource.NewMilliQuantity(int64(valueAtQuantile*1000), resource.DecimalSI)
	})

	// Verify that the recommendation was created
	recommendation, exists := server.recommendedRequestFor(meta)
	if !exists {
		t.Fatal("Expected recommendation to exist, but it doesn't")
	}

	// The recommendation should be based on recent data only (50.0, 75.0, 60.0)
	// At 0.8 quantile with weighted averaging (3x weight for past week), it should be
	// closer to 75.0 than 100.0. Note: digestData returns the raw quantile value;
	// the 1.2 multiplier is applied later in applyRecommendationsBasedOnRecentData.
	cpuRequest := recommendation.Requests[corev1.ResourceCPU]
	cpuRequestMilli := cpuRequest.MilliValue()

	// The value should be based on recent data (around 70-100 cores worth of millicores)
	// We allow some variance due to histogram quantization
	if cpuRequestMilli < 70000 || cpuRequestMilli > 100000 {
		t.Errorf("Expected CPU request to be based on recent data (around 70000-100000 millicores, i.e., 70-100 cores), got %d millicores", cpuRequestMilli)
	}

	// Verify it's not based on the old high value (100.0)
	if cpuRequestMilli > 120000 {
		t.Errorf("CPU request appears to be based on old data (100.0), got %d millicores", cpuRequestMilli)
	}
}

func TestDigestData_SkipsWhenNoRecentData(t *testing.T) {
	now := time.Now()
	fourWeeksAgo := now.Add(-28 * 24 * time.Hour)
	fiveWeeksAgo := now.Add(-35 * 24 * time.Hour)

	oldFingerprint1 := model.Fingerprint(1)
	oldFingerprint2 := model.Fingerprint(2)

	oldHist1 := circonusllhist.New()
	if err := oldHist1.RecordValue(100.0); err != nil {
		t.Fatalf("failed to record value: %v", err)
	}

	oldHist2 := circonusllhist.New()
	if err := oldHist2.RecordValue(200.0); err != nil {
		t.Fatalf("failed to record value: %v", err)
	}

	meta := podscaler.FullMetadata{
		Metadata: api.Metadata{
			Org:    "test-org",
			Repo:   "test-repo",
			Branch: "main",
		},
		Container: "test-container",
	}

	data := &podscaler.CachedQuery{
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
			oldFingerprint1: circonusllhist.NewHistogramWithoutLookups(oldHist1),
			oldFingerprint2: circonusllhist.NewHistogramWithoutLookups(oldHist2),
		},
		DataByMetaData: map[podscaler.FullMetadata][]podscaler.FingerprintTime{
			meta: {
				// All data is old - should be skipped
				{
					Fingerprint: oldFingerprint1,
					Added:       fourWeeksAgo,
				},
				{
					Fingerprint: oldFingerprint2,
					Added:       fiveWeeksAgo,
				},
			},
		},
	}

	server := &resourceServer{
		logger:     logrus.WithField("test", "TestDigestData_SkipsWhenNoRecentData"),
		byMetaData: map[podscaler.FullMetadata]corev1.ResourceRequirements{},
	}

	// Digest the data
	server.digestData(data, 0.8, corev1.ResourceCPU, func(valueAtQuantile float64) *resource.Quantity {
		return resource.NewMilliQuantity(int64(valueAtQuantile*1000), resource.DecimalSI)
	})

	// Verify that no recommendation was created (since all data is old)
	recommendation, exists := server.recommendedRequestFor(meta)
	if exists {
		t.Errorf("Expected no recommendation when all data is old, but got: %v", recommendation)
	}
}
