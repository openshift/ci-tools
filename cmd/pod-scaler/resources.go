package main

import (
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/openhistogram/circonusllhist"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/prow/pkg/pjutil"

	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
)

func newResourceServer(loaders map[string][]*cacheReloader, health *pjutil.Health) *resourceServer {
	logger := logrus.WithField("component", "pod-scaler request server")
	server := &resourceServer{
		logger:     logger,
		lock:       sync.RWMutex{},
		byMetaData: map[podscaler.FullMetadata]corev1.ResourceRequirements{},
	}
	digestAll(loaders, map[string]digester{
		MetricNameCPUUsage:         server.digestCPU,
		MetricNameMemoryWorkingSet: server.digestMemory,
	}, health, logger)

	return server
}

type resourceServer struct {
	logger *logrus.Entry
	lock   sync.RWMutex
	// byMetaData caches resource requirements calculated for the full assortment of
	// metadata labels.
	byMetaData map[podscaler.FullMetadata]corev1.ResourceRequirements
}

const (
	// cpuRequestQuantile is the quantile of CPU core usage data to use as the CPU request
	cpuRequestQuantile = 0.8
	// resourceRecommendationWindow is the time window for which we consider historical data
	// when calculating resource recommendations. We only look at the past 3 weeks of data when
	// figuring out how much resources a pod needs. This way we're making decisions based on what's
	// actually happening now, not what happened months ago. Old data can be misleading - maybe a job
	// used to need more resources but doesn't anymore, or vice versa. By sticking to recent data,
	// we can safely adjust resources up or down based on current usage patterns.
	resourceRecommendationWindow = 21 * 24 * time.Hour // 3 weeks
	// minCPURequestMilli is the minimum CPU request we'll ever recommend (10 millicores).
	// This prevents recommending zero or extremely small values that would cause scheduling issues.
	minCPURequestMilli = int64(10)
	// minMemoryRequestBytes is the minimum memory request we'll ever recommend (10Mi).
	// This prevents recommending zero or extremely small values that would cause issues.
	minMemoryRequestBytes = int64(10 * 1024 * 1024)
)

var (
	// minSamplesForRecommendation is the minimum number of recent data points required before
	// we make a recommendation. This prevents recommendations based on too few samples which could
	// be unreliable or misleading. Can be overridden via POD_SCALER_MIN_SAMPLES environment variable
	// (useful for e2e tests where test data may have fewer samples within the time window).
	minSamplesForRecommendation = getMinSamplesForRecommendation()
)

func formatCPU() toQuantity {
	return func(valueAtQuantile float64) *resource.Quantity {
		return resource.NewMilliQuantity(int64(valueAtQuantile*1000), resource.DecimalSI)
	}
}

func (s *resourceServer) digestCPU(data *podscaler.CachedQuery) {
	s.logger.Debugf("Digesting new CPU consumption metrics.")
	s.digestData(data, cpuRequestQuantile, corev1.ResourceCPU, formatCPU())
}

const (
	// memRequestQuantile is the quantile of memory usage data to use as the memory request
	memRequestQuantile = 0.8
)

func formatMemory() toQuantity {
	return func(valueAtQuantile float64) *resource.Quantity {
		return resource.NewQuantity(int64(valueAtQuantile), resource.BinarySI)
	}
}

func (s *resourceServer) digestMemory(data *podscaler.CachedQuery) {
	s.logger.Debugf("Digesting new memory consumption metrics.")
	s.digestData(data, memRequestQuantile, corev1.ResourceMemory, formatMemory())
}

type toQuantity func(valueAtQuantile float64) (quantity *resource.Quantity)

//nolint:unparam // quantile parameter is kept for flexibility even though currently both CPU and memory use 0.8
func (s *resourceServer) digestData(data *podscaler.CachedQuery, quantile float64, request corev1.ResourceName, quantity toQuantity) {
	logger := s.logger.WithField("resource", request)
	logger.Debugf("Digesting %d identifiers.", len(data.DataByMetaData))
	cutoffTime := time.Now().Add(-resourceRecommendationWindow)
	now := time.Now()
	for meta, fingerprintTimes := range data.DataByMetaData {
		overall := circonusllhist.New()
		metaLogger := logger.WithField("meta", meta)
		metaLogger.Tracef("digesting %d fingerprints", len(fingerprintTimes))
		recentCount := 0
		for _, fingerprintTime := range fingerprintTimes {
			if fingerprintTime.Added.After(cutoffTime) {
				hist := data.Data[fingerprintTime.Fingerprint].Histogram()
				// Weight more recent data more heavily to make the scaler more sensitive to recent runs.
				// Past week: 3x weight, 1-2 weeks ago: 2x weight, 2-3 weeks ago: 1x weight.
				age := now.Sub(fingerprintTime.Added)
				weight := 1
				if age < 7*24*time.Hour {
					weight = 3
				} else if age < 14*24*time.Hour {
					weight = 2
				}
				for i := 0; i < weight; i++ {
					overall.Merge(hist)
				}
				recentCount++
			}
		}
		if recentCount == 0 {
			metaLogger.Debugf("no recent fingerprints (within %v), skipping recommendation", resourceRecommendationWindow)
			continue
		}
		if recentCount < minSamplesForRecommendation {
			metaLogger.Debugf("only %d recent fingerprints (need at least %d), skipping recommendation", recentCount, minSamplesForRecommendation)
			continue
		}
		metaLogger.Tracef("merged %d recent fingerprints (out of %d total)", recentCount, len(fingerprintTimes))
		metaLogger.Trace("merged all fingerprints")
		valueAtQuantile := overall.ValueAtQuantile(quantile)
		metaLogger.Trace("locking for value update")
		s.lock.Lock()
		if _, exists := s.byMetaData[meta]; !exists {
			s.byMetaData[meta] = corev1.ResourceRequirements{
				Requests: corev1.ResourceList{},
				Limits:   corev1.ResourceList{},
			}
		}
		q := quantity(valueAtQuantile)
		// Apply minimum thresholds to prevent recommending zero or extremely small values
		if request == corev1.ResourceCPU {
			if q.MilliValue() < minCPURequestMilli {
				q = resource.NewMilliQuantity(minCPURequestMilli, resource.DecimalSI)
			}
		} else if request == corev1.ResourceMemory {
			if q.Value() < minMemoryRequestBytes {
				q = resource.NewQuantity(minMemoryRequestBytes, resource.BinarySI)
			}
		}
		s.byMetaData[meta].Requests[request] = *q
		metaLogger.Trace("unlocking for meta")
		s.lock.Unlock()
	}
	logger.Debug("Finished digesting new data.")
}

func (s *resourceServer) recommendedRequestFor(meta podscaler.FullMetadata) (corev1.ResourceRequirements, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	data, ok := s.byMetaData[meta]
	return data, ok
}

// getMinSamplesForRecommendation returns the minimum number of samples required for a recommendation.
// Defaults to 3 for production use, but can be overridden via POD_SCALER_MIN_SAMPLES environment
// variable (useful for e2e tests where test data may have fewer samples within the time window).
func getMinSamplesForRecommendation() int {
	if val := os.Getenv("POD_SCALER_MIN_SAMPLES"); val != "" {
		if minSamples, err := strconv.Atoi(val); err == nil && minSamples > 0 {
			return minSamples
		}
	}
	return 3
}
