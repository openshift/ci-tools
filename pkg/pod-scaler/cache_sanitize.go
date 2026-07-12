package pod_scaler

import (
	"math"
	"strings"

	"github.com/openhistogram/circonusllhist"
	"github.com/prometheus/common/model"
)

const (
	// DefaultMaxMemoryWorkingSetBytes matches the build-farm admission --memory-cap default.
	DefaultMaxMemoryWorkingSetBytes = 20 * 1024 * 1024 * 1024
	// DefaultMaxCPUCores matches the build-farm admission --cpu-cap default.
	DefaultMaxCPUCores = 10.0
	// DefaultMaxIncreaseRatio matches admission corrupt-spike detection.
	DefaultMaxIncreaseRatio = 10.0
)

// SanitizeOptions bounds acceptable histogram values when repairing cached query data.
type SanitizeOptions struct {
	MaxMemoryBytes     float64
	MaxCPUCores        float64
	MaxIncreaseRatio   float64
	SparseSampleCutoff uint64
}

// DefaultSanitizeOptions returns options aligned with admission webhook caps.
func DefaultSanitizeOptions() SanitizeOptions {
	return SanitizeOptions{
		MaxMemoryBytes:     DefaultMaxMemoryWorkingSetBytes,
		MaxCPUCores:        DefaultMaxCPUCores,
		MaxIncreaseRatio:   DefaultMaxIncreaseRatio,
		SparseSampleCutoff: 10,
	}
}

// RepairStats summarizes cache repair actions.
type RepairStats struct {
	RemovedMetaKeys      int
	RemovedOrphanRefs    int
	RemovedHistograms    int
	RemovedCorruptValues int
}

// Repair removes orphan metadata references and histograms with corrupt spikes.
func (q *CachedQuery) Repair(metricName string, opts SanitizeOptions) RepairStats {
	if q == nil {
		return RepairStats{}
	}
	if q.Data == nil {
		q.Data = map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{}
	}
	if q.DataByMetaData == nil {
		q.DataByMetaData = map[FullMetadata][]FingerprintTime{}
	}

	stats := RepairStats{}
	stats.RemovedMetaKeys, stats.RemovedOrphanRefs = q.pruneOrphanMetadata()
	removedCorrupt, extraMeta, extraRefs := q.pruneCorruptHistograms(metricName, opts)
	stats.RemovedCorruptValues = removedCorrupt
	stats.RemovedMetaKeys += extraMeta
	stats.RemovedOrphanRefs += extraRefs
	stats.RemovedHistograms = q.pruneUnreferencedHistograms()
	return stats
}

func (q *CachedQuery) pruneOrphanMetadata() (removedMeta int, removedRefs int) {
	for meta, fingerprintTimes := range q.DataByMetaData {
		kept := make([]FingerprintTime, 0, len(fingerprintTimes))
		for _, fingerprintTime := range fingerprintTimes {
			if _, ok := q.Data[fingerprintTime.Fingerprint]; ok {
				kept = append(kept, fingerprintTime)
				continue
			}
			removedRefs++
		}
		if len(kept) == 0 {
			delete(q.DataByMetaData, meta)
			removedMeta++
			continue
		}
		q.DataByMetaData[meta] = kept
	}
	return removedMeta, removedRefs
}

func (q *CachedQuery) pruneCorruptHistograms(metricName string, opts SanitizeOptions) (removed int, extraMeta int, extraRefs int) {
	for fingerprint, histWithoutLookups := range q.Data {
		hist := histWithoutLookups.Histogram()
		if hist == nil || hist.Count() == 0 {
			delete(q.Data, fingerprint)
			removed++
			continue
		}
		if histogramCorrupt(metricName, hist, opts) {
			delete(q.Data, fingerprint)
			removed++
		}
	}
	if removed > 0 {
		extraMeta, extraRefs = q.pruneOrphanMetadata()
	}
	return removed, extraMeta, extraRefs
}

func (q *CachedQuery) pruneUnreferencedHistograms() int {
	referenced := map[model.Fingerprint]struct{}{}
	for _, fingerprintTimes := range q.DataByMetaData {
		for _, fingerprintTime := range fingerprintTimes {
			referenced[fingerprintTime.Fingerprint] = struct{}{}
		}
	}
	removed := 0
	for fingerprint := range q.Data {
		if _, ok := referenced[fingerprint]; ok {
			continue
		}
		delete(q.Data, fingerprint)
		removed++
	}
	return removed
}

func histogramCorrupt(metricName string, hist *circonusllhist.Histogram, opts SanitizeOptions) bool {
	maxValue := hist.Max()
	if !valueUsable(maxValue) {
		return true
	}
	if isMemoryMetric(metricName) {
		if maxValue > opts.MaxMemoryBytes {
			return true
		}
	} else if isCPUMetric(metricName) {
		if maxValue > opts.MaxCPUCores {
			return true
		}
	}
	baseline := histogramBaseline(hist, opts.SparseSampleCutoff)
	if baseline != nil && *baseline > 0 && maxValue > (*baseline)*opts.MaxIncreaseRatio {
		return true
	}
	return false
}

func histogramBaseline(hist *circonusllhist.Histogram, sparseSampleCutoff uint64) *float64 {
	if hist.Count() == 0 {
		return nil
	}
	var baseline float64
	if hist.Count() < sparseSampleCutoff {
		baseline = hist.Max()
	} else {
		baseline = hist.ValueAtQuantile(0.8)
	}
	if !valueUsable(baseline) {
		return nil
	}
	return &baseline
}

func valueUsable(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

func isMemoryMetric(metricName string) bool {
	return strings.Contains(metricName, "memory")
}

func isCPUMetric(metricName string) bool {
	return strings.Contains(metricName, "cpu")
}
