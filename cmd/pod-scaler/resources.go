package main

import (
	"math"
	"sync"

	"github.com/openhistogram/circonusllhist"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/prow/pkg/pjutil"

	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
)

func newResourceServer(loaders map[string][]*cacheReloader, health *pjutil.Health, cpuCapCores int64, memoryCapFlag string) *resourceServer {
	logger := logrus.WithField("component", "pod-scaler request server")
	server := &resourceServer{
		logger:           logger,
		lock:             sync.RWMutex{},
		byMetaData:       map[podscaler.FullMetadata]corev1.ResourceRequirements{},
		cpuRequestCap:    *resource.NewQuantity(cpuCapCores, resource.DecimalSI),
		memoryRequestCap: resource.MustParse(memoryCapFlag),
	}
	digestAll(loaders, map[string]digester{
		MetricNameCPUUsage: func(data *podscaler.CachedQuery) {
			server.digestRecommendations(data, corev1.ResourceCPU, cpuRequestQuantile, func(hist *circonusllhist.Histogram, quantile float64) corev1.ResourceList {
				return cpuRequestQuantityFromHistogram(hist, quantile, server.cpuRequestCap, server.logger)
			})
		},
		MetricNameMemoryWorkingSet: func(data *podscaler.CachedQuery) {
			server.digestRecommendations(data, corev1.ResourceMemory, memRequestQuantile, func(hist *circonusllhist.Histogram, quantile float64) corev1.ResourceList {
				return memoryRequestQuantityFromHistogram(hist, quantile, server.memoryRequestCap, server.logger)
			})
		},
	}, health, logger)

	return server
}

type resourceServer struct {
	logger *logrus.Entry
	lock   sync.RWMutex
	// byMetaData caches resource requirements calculated for the full assortment of
	// metadata labels.
	byMetaData map[podscaler.FullMetadata]corev1.ResourceRequirements
	// cpuRequestCap is parsed from --cpu-cap (whole cores). memoryRequestCap is parsed
	// from --memory-cap (Kubernetes quantity string, e.g. 20Gi), not a raw float.
	cpuRequestCap    resource.Quantity
	memoryRequestCap resource.Quantity
}

const (
	// cpuRequestQuantile is the quantile of CPU core usage data to use as the CPU request
	cpuRequestQuantile = 0.8
	// sparseSampleThreshold uses Max instead of quantile when sample count is below this.
	sparseSampleThreshold uint64 = 10
)

const (
	// memRequestQuantile is the quantile of memory usage data to use as the memory request
	memRequestQuantile = 0.8
)

func quantileValueUsable(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

const maxUsableUsageValue = float64(1 << 63)

// recommendationPeakValue returns the histogram max (burst/spike usage).
func recommendationPeakValue(hist *circonusllhist.Histogram) *float64 {
	if hist.Count() == 0 {
		return nil
	}
	v := hist.Max()
	if !quantileValueUsable(v) || v >= maxUsableUsageValue {
		return nil
	}
	return &v
}

func cpuPeakQuantityFromHistogram(hist *circonusllhist.Histogram) *resource.Quantity {
	usage := recommendationPeakValue(hist)
	if usage == nil {
		return nil
	}
	return cpuQuantityFromCores(*usage)
}

func memoryPeakQuantityFromHistogram(hist *circonusllhist.Histogram) *resource.Quantity {
	usage := recommendationPeakValue(hist)
	if usage == nil {
		return nil
	}
	return memoryQuantityFromBytes(*usage)
}

// recommendationValue returns usage in histogram-native units (cores for CPU, bytes for memory).
// nil means there is no usable recommendation.
func recommendationValue(hist *circonusllhist.Histogram, quantile float64) *float64 {
	count := hist.Count()
	if count == 0 {
		return nil
	}
	var v float64
	if count < sparseSampleThreshold {
		v = hist.Max()
	} else {
		v = hist.ValueAtQuantile(quantile)
	}
	if !quantileValueUsable(v) || v > float64(math.MaxInt64) {
		return nil
	}
	return &v
}

func cpuQuantityFromCores(cores float64) *resource.Quantity {
	if !quantileValueUsable(cores) {
		return nil
	}
	milli := cores * 1000
	if milli > float64(math.MaxInt64) {
		return nil
	}
	return resource.NewMilliQuantity(int64(milli), resource.DecimalSI)
}

func memoryQuantityFromBytes(bytes float64) *resource.Quantity {
	if !quantileValueUsable(bytes) {
		return nil
	}
	if bytes >= maxUsableUsageValue {
		return nil
	}
	return resource.NewQuantity(int64(bytes), resource.BinarySI)
}

// cpuRequestQuantityFromHistogram returns a capped CPU request, or nil.
func cpuRequestQuantityFromHistogram(hist *circonusllhist.Histogram, quantile float64, cpuCap resource.Quantity, logger *logrus.Entry) corev1.ResourceList {
	usage := recommendationValue(hist, quantile)
	if usage == nil {
		return nil
	}
	q := cpuQuantityFromCores(*usage)
	if q == nil {
		return nil
	}
	if !recommendationQuantityUsable(corev1.ResourceCPU, *q) {
		logger.WithField("recommendation", q.String()).Debug("skipping CPU recommendation below minimum")
		return nil
	}
	reqs := &corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: *q}}
	capDigestRequests(reqs, cpuCap, resource.Quantity{}, logger)
	return reqs.Requests
}

// memoryRequestQuantityFromHistogram returns a capped memory request, or nil.
func memoryRequestQuantityFromHistogram(hist *circonusllhist.Histogram, quantile float64, memoryCap resource.Quantity, logger *logrus.Entry) corev1.ResourceList {
	usage := recommendationValue(hist, quantile)
	if usage == nil {
		return nil
	}
	q := memoryQuantityFromBytes(*usage)
	if q == nil {
		return nil
	}
	if !recommendationQuantityUsable(corev1.ResourceMemory, *q) {
		logger.WithField("recommendation", q.String()).Debug("skipping memory recommendation below cgroup v2 minimum")
		return nil
	}
	reqs := &corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceMemory: *q}}
	capDigestRequests(reqs, resource.Quantity{}, memoryCap, logger)
	return reqs.Requests
}

func mergeHistogramsForMeta(data *podscaler.CachedQuery, fingerprintTimes []podscaler.FingerprintTime) *circonusllhist.Histogram {
	overall := circonusllhist.New()
	merged := 0
	for _, fingerprintTime := range fingerprintTimes {
		histData := data.Data[fingerprintTime.Fingerprint]
		if histData == nil {
			continue
		}
		overall.Merge(histData.Histogram())
		merged++
	}
	if merged == 0 || overall.Count() == 0 {
		return nil
	}
	return overall
}

func peakLimitQuantity(resourceName corev1.ResourceName, hist *circonusllhist.Histogram, request *resource.Quantity, cpuCap, memoryCap resource.Quantity) *resource.Quantity {
	var q *resource.Quantity
	switch resourceName {
	case corev1.ResourceCPU:
		q = cpuPeakQuantityFromHistogram(hist)
	case corev1.ResourceMemory:
		q = memoryPeakQuantityFromHistogram(hist)
	}
	if q == nil || !recommendationQuantityUsable(resourceName, *q) {
		return nil
	}
	if request != nil && !request.IsZero() && increaseExceedsConfiguredThreshold(resourceName, *q, *request) {
		capped := cappedIncreaseQuantity(resourceName, *request, cpuCap, memoryCap)
		q = &capped
	} else {
		capped := capQuantityToClusterMaximum(resourceName, *q, cpuCap, memoryCap)
		q = &capped
	}
	return q
}

func (s *resourceServer) applyDigestUpdates(resource corev1.ResourceName, updates map[podscaler.FullMetadata]corev1.ResourceRequirements) {
	s.lock.Lock()
	defer s.lock.Unlock()
	for meta, existing := range s.byMetaData {
		delete(existing.Requests, resource)
		delete(existing.Limits, resource)
		if len(existing.Requests) == 0 && len(existing.Limits) == 0 {
			delete(s.byMetaData, meta)
		}
	}
	for meta, reqs := range updates {
		entry, exists := s.byMetaData[meta]
		if !exists {
			entry = corev1.ResourceRequirements{
				Requests: corev1.ResourceList{},
				Limits:   corev1.ResourceList{},
			}
		}
		if q, ok := reqs.Requests[resource]; ok {
			entry.Requests[resource] = q
		}
		if q, ok := reqs.Limits[resource]; ok {
			entry.Limits[resource] = q
		}
		s.byMetaData[meta] = entry
	}
}

func (s *resourceServer) digestRecommendations(
	data *podscaler.CachedQuery,
	resource corev1.ResourceName,
	quantile float64,
	recommend func(*circonusllhist.Histogram, float64) corev1.ResourceList,
) {
	logger := s.logger.WithField("resource", resource)
	logger.Debugf("Digesting %d identifiers.", len(data.DataByMetaData))
	updates := map[podscaler.FullMetadata]corev1.ResourceRequirements{}
	for meta, fingerprintTimes := range data.DataByMetaData {
		metaLogger := logger.WithField("meta", meta)
		metaLogger.Tracef("digesting %d fingerprints", len(fingerprintTimes))
		overall := mergeHistogramsForMeta(data, fingerprintTimes)
		if overall == nil {
			metaLogger.Debug("skipping recommendation with no resolvable histogram data")
			continue
		}
		requests := recommend(overall, quantile)
		if len(requests) == 0 {
			metaLogger.Debug("skipping recommendation with no usable histogram data")
			continue
		}
		entry := corev1.ResourceRequirements{
			Requests: corev1.ResourceList{resource: requests[resource]},
		}
		requestQty := requests[resource]
		if peak := peakLimitQuantity(resource, overall, &requestQty, s.cpuRequestCap, s.memoryRequestCap); peak != nil {
			entry.Limits = corev1.ResourceList{resource: *peak}
		}
		updates[meta] = entry
	}
	s.applyDigestUpdates(resource, updates)
	logger.Debug("Finished digesting new data.")
}

func (s *resourceServer) recommendedRequestFor(meta podscaler.FullMetadata) (corev1.ResourceRequirements, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	data, ok := s.byMetaData[meta]
	return data, ok
}
