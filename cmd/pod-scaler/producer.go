package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openhistogram/circonusllhist"
	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/prow/pkg/interrupts"

	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
)

const (
	MetricNameCPUUsage         = `container_cpu_usage_seconds_total`
	MetricNameMemoryWorkingSet = `container_memory_working_set_bytes`

	containerFilter = `{container!="POD",container!=""}`

	// MaxSamplesPerRequest is the maximum number of samples that Prometheus will allow a client to ask for in
	// one request. We also use this to approximate the maximum number of samples we should be asking any one
	// Prometheus server for at once from many requests.
	MaxSamplesPerRequest = 11000

	ProwjobsCachePrefix = "prowjobs"
	PodsCachePrefix     = "pods"
	StepsCachePrefix    = "steps"

	metricOOMKilled    = `kube_pod_container_status_last_terminated_reason`
	metricCPUThrottled = `container_cpu_cfs_throttled_periods_total`
	metricCPUPeriods   = `container_cpu_cfs_periods_total`
)

type metricQueryConfig struct {
	prefix   string
	selector string
	labels   []string
}

func metricQueryConfigs() []metricQueryConfig {
	return []metricQueryConfig{
		{
			prefix:   ProwjobsCachePrefix,
			selector: `{` + string(podscaler.ProwLabelNameCreated) + `="true",` + string(podscaler.ProwLabelNameJob) + `!="",` + string(podscaler.LabelNameRehearsal) + `=""}`,
			labels:   []string{string(podscaler.ProwLabelNameCreated), string(podscaler.ProwLabelNameContext), string(podscaler.ProwLabelNameOrg), string(podscaler.ProwLabelNameRepo), string(podscaler.ProwLabelNameBranch), string(podscaler.ProwLabelNameJob), string(podscaler.ProwLabelNameType), string(podscaler.LabelNameMeasured)},
		},
		{
			prefix:   PodsCachePrefix,
			selector: `{` + string(podscaler.LabelNameCreated) + `="true",` + string(podscaler.LabelNameStep) + `=""}`,
			labels:   []string{string(podscaler.LabelNameOrg), string(podscaler.LabelNameRepo), string(podscaler.LabelNameBranch), string(podscaler.LabelNameVariant), string(podscaler.LabelNameTarget), string(podscaler.LabelNameBuild), string(podscaler.LabelNameRelease), string(podscaler.LabelNameApp), string(podscaler.LabelNameMeasured)},
		},
		{
			prefix:   StepsCachePrefix,
			selector: `{` + string(podscaler.LabelNameCreated) + `="true",` + string(podscaler.LabelNameStep) + `!=""}`,
			labels:   []string{string(podscaler.LabelNameOrg), string(podscaler.LabelNameRepo), string(podscaler.LabelNameBranch), string(podscaler.LabelNameVariant), string(podscaler.LabelNameTarget), string(podscaler.LabelNameStep), string(podscaler.LabelNameMeasured)},
		},
	}
}

func queriesByMetric() map[string]string {
	queries := map[string]string{}
	for _, info := range metricQueryConfigs() {
		for name, metric := range map[string]string{
			MetricNameCPUUsage:         `rate(` + MetricNameCPUUsage + containerFilter + `[3m])`,
			MetricNameMemoryWorkingSet: MetricNameMemoryWorkingSet + containerFilter,
		} {
			queries[fmt.Sprintf("%s/%s", info.prefix, name)] = queryFor(metric, info.selector, info.labels)
		}
	}
	return queries
}

func produce(clients map[string]prometheusapi.API, dataCache Cache, ignoreLatest, maxDataAge time.Duration, once bool, failureEscalationMaxLevel int, cpuThrottleThreshold float64) {
	var execute func(func())
	if once {
		execute = func(f func()) {
			f()
		}
	} else {
		execute = func(f func()) {
			interrupts.TickLiteral(f, 2*time.Hour)
		}
	}
	execute(func() {
		for name, query := range queriesByMetric() {
			name := name
			query := query
			logger := logrus.WithFields(logrus.Fields{
				"version": "v2",
				"metric":  name,
			})
			cache, err := LoadCache(dataCache, name, logger)
			if errors.Is(err, notExist{}) {
				ranges := map[string][]podscaler.TimeRange{}
				for cluster := range clients {
					ranges[cluster] = []podscaler.TimeRange{}
				}
				cache = &podscaler.CachedQuery{
					Query:           query,
					RangesByCluster: ranges,
					Data:            map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{},
					DataByMetaData:  map[podscaler.FullMetadata][]podscaler.FingerprintTime{},
				}
			} else if err != nil {
				logrus.WithError(err).Error("Failed to load data from storage.")
				continue
			}
			if cache.Query != query {
				logger.WithFields(logrus.Fields{
					"old_query": cache.Query,
					"new_query": query,
				}).Info("Query has changed, updating cached query. Previously collected data will be re-queried over time.")
				cache.Query = query
				// Reset the covered ranges so the new query will be executed over the full
				// retention window. Existing data remains valid but new data will be collected
				// with the updated query, which may include additional labels.
				cache.RangesByCluster = map[string][]podscaler.TimeRange{}
				for cluster := range clients {
					cache.RangesByCluster[cluster] = []podscaler.TimeRange{}
				}
			}
			until := time.Now().Add(-ignoreLatest)
			q := querier{
				lock:       &sync.RWMutex{},
				data:       cache,
				maxDataAge: maxDataAge,
			}
			wg := &sync.WaitGroup{}
			for clusterName, client := range clients {
				metadata := &clusterMetadata{
					logger: logger.WithField("cluster", clusterName),
					name:   clusterName,
					client: client,
					lock:   &sync.RWMutex{},
					// there's absolutely no chance Prometheus at the current scaling will ever be able
					// to respond to large requests it's completely capable of creating, so don't even
					// bother asking for anything larger than 1/20th of the largest request we can get
					// responses within the default client connection timeout.
					maxSize: MaxSamplesPerRequest / 20,
					errors:  make(chan error),
					// there's also no chance that Prometheus will be able to handle any real concurrent
					// request volume, so don't even bother trying to request more samples at once than
					// a fifth of the maximum samples it can technically provide in one request
					sync: semaphore.NewWeighted(MaxSamplesPerRequest / 15),
					wg:   &sync.WaitGroup{},
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := q.execute(interrupts.Context(), metadata, until); err != nil {
						metadata.logger.WithError(err).Error("Failed to query Prometheus.")
					}
				}()
			}
			wg.Wait()
			if err := storeCache(dataCache, name, cache, maxDataAge, logger); err != nil {
				logger.WithError(err).Error("Failed to write cached data.")
			}
		}
		refreshEscalationIndex(clients, dataCache, failureEscalationMaxLevel, cpuThrottleThreshold)
	})
}

// queryFor applies our filtering and left joins to a metric to get data we can use
func queryFor(metric, selector string, labels []string) string {
	return `sum by (
    namespace,
    pod,
    container
  ) (` + metric + `)
  * on(namespace,pod) 
  group_left(
    ` + strings.Join(labels, ",\n    ") + `
  ) max by (
    namespace,
    pod,
    ` + strings.Join(labels, ",\n    ") + `
  ) (kube_pod_labels` + selector + `)`
}

func rangeFrom(r prometheusapi.Range) podscaler.TimeRange {
	return podscaler.TimeRange{
		Start: r.Start,
		End:   r.End,
	}
}

type querier struct {
	lock       *sync.RWMutex
	data       *podscaler.CachedQuery
	maxDataAge time.Duration
}

type clusterMetadata struct {
	logger *logrus.Entry
	name   string
	client prometheusapi.API
	errors chan error

	lock    *sync.RWMutex
	maxSize int64

	// sync guards the number of concurrent samples we can be asking Prometheus for at any one time
	sync *semaphore.Weighted
	wg   *sync.WaitGroup
}

func (q *querier) execute(ctx context.Context, c *clusterMetadata, until time.Time) error {
	runtime, err := c.client.Runtimeinfo(ctx)
	if err != nil {
		return fmt.Errorf("could not query Prometheus runtime info: %w", err)
	}
	storageRetention := runtime.StorageRetention
	// storageRetention may look like "11d or 90Gib" or "30d" depending on the configuration
	parts := strings.Split(storageRetention, " ")
	retention, err := model.ParseDuration(parts[0])
	if err != nil {
		return fmt.Errorf("could not determine Prometheus retention duration: %w", err)
	}
	start := time.Now().Add(-time.Duration(retention))
	if q.maxDataAge > 0 {
		maxStart := time.Now().Add(-q.maxDataAge)
		if maxStart.After(start) {
			start = maxStart
		}
	}
	r := prometheusapi.Range{
		Start: start,
		End:   until,
		Step:  1 * time.Minute,
	}

	errLock := &sync.Mutex{}
	var errs []error
	go func() {
		errLock.Lock()
		defer errLock.Unlock()
		for err := range c.errors {
			errs = append(errs, err)
		}
	}()

	q.lock.RLock()
	previousEntries, previousIdentifiers := len(q.data.Data), len(q.data.DataByMetaData)
	q.lock.RUnlock()
	queryStart := time.Now()
	logger := c.logger.WithFields(logrus.Fields{
		"start": r.Start.Format(time.RFC3339),
		"end":   r.End.Format(time.RFC3339),
		"step":  r.Step,
	})
	logger.Info("Initiating queries to Prometheus.")
	uncovered := q.uncoveredRanges(c.name, rangeFrom(r))
	c.lock.RLock()
	numSteps := c.maxSize - 1
	c.lock.RUnlock()
	for _, r := range divideRange(uncovered, r.Step, numSteps) {
		c.wg.Add(1)
		go q.executeOverRange(ctx, c, r)
	}
	c.wg.Wait()
	q.lock.RLock()
	currentEntries, currentIdentifiers := len(q.data.Data), len(q.data.DataByMetaData)
	q.lock.RUnlock()
	logger.Infof("Query completed after %s, yielding %d new identifiers and %d new data series.", time.Since(queryStart).Round(time.Second), currentIdentifiers-previousIdentifiers, currentEntries-previousEntries)
	close(c.errors)
	errLock.Lock()
	return kerrors.NewAggregate(errs)
}

// uncoveredRanges determines the largest subset ranges of r that are not covered by
// existing data in the querier.
func (q *querier) uncoveredRanges(cluster string, r podscaler.TimeRange) []podscaler.TimeRange {
	q.lock.RLock()
	defer q.lock.RUnlock()
	return podscaler.UncoveredRanges(r, q.data.RangesByCluster[cluster])
}

// divideRange divides a range into smaller ranges based on how many samples we think is reasonable
// to ask for from Prometheus in one query
func divideRange(uncovered []podscaler.TimeRange, step time.Duration, numSteps int64) []prometheusapi.Range {
	var divided []prometheusapi.Range
	for _, uncoveredRange := range uncovered {
		// Prometheus has practical limits for how much data we can ask for in any one request,
		// so we take each uncovered range and split it into chunks we can ask for.
		start := uncoveredRange.Start
		stop := uncoveredRange.End
		for {
			if start.After(uncoveredRange.End) {
				break
			}
			steps := int64(stop.Sub(start) / step)
			if steps <= 1 {
				// this range is likely too small to contain novel data and asking for this in a query leads
				// to weird behavior - if we ignore it for now, we won't call it covered and will subsume it
				// into a larger range in the future
				break
			} else if steps > numSteps {
				stop = start.Add(time.Duration(numSteps) * step)
			}
			divided = append(divided, prometheusapi.Range{Start: start, End: stop, Step: step})
			// adding a step to the start will ensure we don't double-count samples on the edge, as ranges are inclusive
			// and the query is evaulated at start, start+step, start+2*step, etc - if we started less than end+step, we
			// would get the same value we got at the end of the previous query
			start = stop.Add(step)
			stop = uncoveredRange.End
		}
	}
	return divided
}

func (q *querier) executeOverRange(ctx context.Context, c *clusterMetadata, r prometheusapi.Range) {
	defer c.wg.Done()
	numSteps := int64(r.End.Sub(r.Start) / r.Step)
	logger := c.logger.WithFields(logrus.Fields{
		"start": r.Start.Format(time.RFC3339),
		"end":   r.End.Format(time.RFC3339),
		"step":  r.Step,
		"steps": numSteps,
	})
	if err := c.sync.Acquire(ctx, numSteps); err != nil {
		c.errors <- err
		return
	}
	defer c.sync.Release(numSteps)
	c.lock.RLock()
	currentMax := c.maxSize
	c.lock.RUnlock()
	subdivide := func() {
		c.wg.Add(2)
		middle := r.Start.Add(time.Duration(numSteps) / 2 * r.Step)
		go q.executeOverRange(ctx, c, prometheusapi.Range{Start: r.Start, End: middle, Step: r.Step})
		go q.executeOverRange(ctx, c, prometheusapi.Range{Start: middle.Add(r.Step), End: r.End, Step: r.Step})
	}
	if numSteps >= currentMax {
		logger.Debugf("Preemptively halving request as prior data shows ours is too large (%d>=%d).", numSteps, currentMax)
		subdivide()
		return
	}

	queryStart := time.Now()
	logger.Debug("Querying Prometheus.")
	q.lock.RLock()
	query := q.data.Query
	q.lock.RUnlock()
	result, warnings, err := c.client.QueryRange(ctx, query, r)
	logger.Debugf("Queried Prometheus API in %s.", time.Since(queryStart).Round(time.Second))
	if err != nil {
		apiError := &prometheusapi.Error{}
		if errors.As(err, &apiError) {
			// Prometheus determined not to expose this programmatically ...
			if strings.HasSuffix(apiError.Msg, "504") {
				var ignoreErrorAndSubdivide bool
				c.lock.Lock()
				if numSteps >= c.maxSize {
					// We hit a timeout asking for a known large value, subdivide our query.
					ignoreErrorAndSubdivide = true
				} else if numSteps > 250 { // implicit: numSteps < c.maxSize
					// We hit a timeout and are still asking for a reasonably "large" amount of
					// data at once, so halve the amount of data we are asking for in order to
					// have a higher chance of getting the data next time. If we're asking for
					// a small amount already it's likely the server is on the verge of falling
					// over, so just error out and try again later.
					logger.Debugf("Received 504 asking for %d samples, halving to %d.", numSteps, numSteps/2)
					c.maxSize = numSteps
					ignoreErrorAndSubdivide = true
				} else {
					logger.Debugf("Received 504 but only asking for %d samples, aborting.", numSteps)
				}
				c.lock.Unlock()
				if ignoreErrorAndSubdivide {
					// the error isn't fatal to the fetch, ignore it and subdivide the query
					subdivide()
					return
				}
			}
		}
		logger.WithError(err).Error("Failed to query Prometheus API.")
		c.errors <- fmt.Errorf("failed to query Prometheus API: %w", err)
		return
	}
	if len(warnings) > 0 {
		logger.WithField("warnings", warnings).Warn("Got warnings from Prometheus.")
	}

	matrix, ok := result.(model.Matrix)
	if !ok {
		c.errors <- fmt.Errorf("returned result of type %T from Prometheus cannot be cast to matrix", result)
		return
	}

	// Filter out sub-1Mi memory samples to prevent garbage idle-noise
	// measurements (e.g. 324Ki entrypoint-wrapper stamps) from entering
	// the GCS cache where they would produce invalid recommendations.
	if strings.Contains(query, MetricNameMemoryWorkingSet) {
		filterMemoryFloor(matrix, logger)
	}

	saveStart := time.Now()
	logger.Debug("Saving response from Prometheus data.")
	q.lock.Lock()
	q.data.Record(c.name, rangeFrom(r), matrix, logger)
	q.lock.Unlock()
	logger.Debugf("Saved Prometheus response after %s.", time.Since(saveStart).Round(time.Second))
}

func refreshEscalationIndex(clients map[string]prometheusapi.API, dataCache Cache, maxLevel int, cpuThrottleThreshold float64) {
	logger := logrus.WithField("component", "pod-scaler escalation producer")
	index := loadEscalationIndex(dataCache, logger)
	oomWorkloads := map[string]struct{}{}
	throttledWorkloads := map[string]struct{}{}
	usageWorkloads := map[string]struct{}{}

	for clusterName, client := range clients {
		clusterLogger := logger.WithField("cluster", clusterName)
		for _, info := range metricQueryConfigs() {
			oomQuery := queryFor(
				metricOOMKilled+`{reason="OOMKilled",container!="POD",container!=""}`,
				info.selector,
				info.labels,
			)
			if err := queryInstantVector(clusterLogger.WithField("query", "oom"), client, oomQuery, func(metric model.Metric, value model.SampleValue) {
				if value > 0 {
					oomWorkloads[podscaler.WorkloadKeyFromMetric(metric)] = struct{}{}
				}
			}); err != nil {
				clusterLogger.WithError(err).WithField("query", "oom").Warn("Failed to query OOM signal.")
			}

			throttleQuery := queryFor(
				`sum by (namespace,pod,container) (rate(`+metricCPUThrottled+containerFilter+`[1h])) / sum by (namespace,pod,container) (rate(`+metricCPUPeriods+containerFilter+`[1h]))`,
				info.selector,
				info.labels,
			)
			if err := queryInstantVector(clusterLogger.WithField("query", "cpu_throttle"), client, throttleQuery, func(metric model.Metric, value model.SampleValue) {
				if value >= model.SampleValue(cpuThrottleThreshold) {
					throttledWorkloads[podscaler.WorkloadKeyFromMetric(metric)] = struct{}{}
				}
			}); err != nil {
				clusterLogger.WithError(err).WithField("query", "cpu_throttle").Warn("Failed to query CPU throttle signal.")
			}

			usageQuery := queriesByMetric()[info.prefix+"/"+MetricNameMemoryWorkingSet]
			if err := queryInstantVector(clusterLogger.WithField("query", "usage"), client, usageQuery, func(metric model.Metric, value model.SampleValue) {
				if value > 0 {
					usageWorkloads[podscaler.WorkloadKeyFromMetric(metric)] = struct{}{}
				}
			}); err != nil {
				clusterLogger.WithError(err).WithField("query", "usage").Warn("Failed to query usage signal.")
			}
		}
	}

	for key := range oomWorkloads {
		state := index[key]
		if state.MemoryLevel < maxLevel {
			state.MemoryLevel++
		}
		index[key] = state
	}
	for key := range throttledWorkloads {
		state := index[key]
		if state.CPULevel < maxLevel {
			state.CPULevel++
		}
		index[key] = state
	}
	decayEscalation := func(key string) {
		state, ok := index[key]
		if !ok {
			return
		}
		if state.MemoryLevel > 0 {
			state.MemoryLevel--
		}
		if state.CPULevel > 0 {
			state.CPULevel--
		}
		if state.MemoryLevel == 0 && state.CPULevel == 0 {
			delete(index, key)
			return
		}
		index[key] = state
	}
	for key := range usageWorkloads {
		if _, oom := oomWorkloads[key]; oom {
			continue
		}
		if _, throttled := throttledWorkloads[key]; throttled {
			continue
		}
		decayEscalation(key)
	}
	for key := range index {
		if _, oom := oomWorkloads[key]; oom {
			continue
		}
		if _, throttled := throttledWorkloads[key]; throttled {
			continue
		}
		if _, active := usageWorkloads[key]; active {
			continue
		}
		decayEscalation(key)
	}

	if err := storeEscalationIndex(dataCache, index); err != nil {
		logger.WithError(err).Error("Failed to store workload escalation index.")
	}
}

func queryInstantVector(logger *logrus.Entry, client prometheusapi.API, query string, apply func(model.Metric, model.SampleValue)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	value, warnings, err := client.Query(ctx, query, time.Now())
	if err != nil {
		return err
	}
	if len(warnings) > 0 {
		logger.WithField("warnings", warnings).Warn("Got warnings from Prometheus.")
	}
	vector, ok := value.(model.Vector)
	if !ok {
		return nil
	}
	for _, sample := range vector {
		if sample.Metric == nil {
			continue
		}
		apply(sample.Metric, sample.Value)
	}
	return nil
}

// filterMemoryFloor removes memory sample values below authoritativeMinMemoryLimit
// from the matrix in-place. This prevents garbage sub-1Mi measurements from
// entering the GCS cache where they could later produce invalid recommendations.
func filterMemoryFloor(matrix model.Matrix, logger *logrus.Entry) {
	minValue := model.SampleValue(authoritativeMinMemoryLimit.AsApproximateFloat64())
	for _, stream := range matrix {
		filtered := stream.Values[:0]
		for _, v := range stream.Values {
			if v.Value >= minValue {
				filtered = append(filtered, v)
			}
		}
		if dropped := len(stream.Values) - len(filtered); dropped > 0 {
			logger.WithField("dropped", dropped).Debug("Dropped sub-1Mi memory samples.")
		}
		stream.Values = filtered
	}
}
