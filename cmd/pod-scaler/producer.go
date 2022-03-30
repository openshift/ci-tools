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
	"k8s.io/test-infra/prow/interrupts"

	pod_scaler "github.com/openshift/ci-tools/pkg/pod-scaler"
)

const (
	MetricNameCPUUsage         = `container_cpu_usage_seconds_total`
	MetricNameMemoryWorkingSet = `container_memory_working_set_bytes`

	containerFilter = `{container!="POD",container!=""}`

	// MaxSamplesPerRequest is the maximum number of samples that Prometheus will allow a client to ask for in
	// one request. We also use this to approximate the maximum number of samples we should be asking any one
	// Prometheus server for at once from many requests.
	MaxSamplesPerRequest = 11000

	prowjobsCachePrefix = "prowjobs"
	podsCachePrefix     = "pods"
	stepsCachePrefix    = "steps"
)

// queriesByMetric returns a mapping of Prometheus query by metric name for all queries we want to execute
func queriesByMetric() map[string]string {
	queries := map[string]string{}
	for _, info := range []struct {
		prefix   string
		selector string
		labels   []string
	}{
		{
			prefix:   prowjobsCachePrefix,
			selector: `{` + string(pod_scaler.ProwLabelNameCreated) + `="true",` + string(pod_scaler.ProwLabelNameJob) + `!="",` + string(pod_scaler.LabelNameRehearsal) + `=""}`,
			labels:   []string{string(pod_scaler.ProwLabelNameCreated), string(pod_scaler.ProwLabelNameContext), string(pod_scaler.ProwLabelNameOrg), string(pod_scaler.ProwLabelNameRepo), string(pod_scaler.ProwLabelNameBranch), string(pod_scaler.ProwLabelNameJob), string(pod_scaler.ProwLabelNameType)},
		},
		{
			prefix:   podsCachePrefix,
			selector: `{` + string(pod_scaler.LabelNameCreated) + `="true",` + string(pod_scaler.LabelNameStep) + `=""}`,
			labels:   []string{string(pod_scaler.LabelNameOrg), string(pod_scaler.LabelNameRepo), string(pod_scaler.LabelNameBranch), string(pod_scaler.LabelNameVariant), string(pod_scaler.LabelNameTarget), string(pod_scaler.LabelNameBuild), string(pod_scaler.LabelNameRelease), string(pod_scaler.LabelNameApp)},
		},
		{
			prefix:   stepsCachePrefix,
			selector: `{` + string(pod_scaler.LabelNameCreated) + `="true",` + string(pod_scaler.LabelNameStep) + `!=""}`,
			labels:   []string{string(pod_scaler.LabelNameOrg), string(pod_scaler.LabelNameRepo), string(pod_scaler.LabelNameBranch), string(pod_scaler.LabelNameVariant), string(pod_scaler.LabelNameTarget), string(pod_scaler.LabelNameStep)},
		},
	} {
		for name, metric := range map[string]string{
			MetricNameCPUUsage:         `rate(` + MetricNameCPUUsage + containerFilter + `[3m])`,
			MetricNameMemoryWorkingSet: MetricNameMemoryWorkingSet + containerFilter,
		} {
			queries[fmt.Sprintf("%s/%s", info.prefix, name)] = queryFor(metric, info.selector, info.labels)
		}
	}
	return queries
}

func produce(clients map[string]prometheusapi.API, dataCache cache, ignoreLatest time.Duration, once bool) {
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
			logger := logrus.WithField("metric", name)
			cache, err := loadCache(dataCache, name, logger)
			if errors.Is(err, notExist{}) {
				ranges := map[string][]pod_scaler.TimeRange{}
				for cluster := range clients {
					ranges[cluster] = []pod_scaler.TimeRange{}
				}
				cache = &pod_scaler.CachedQuery{
					Query:           query,
					RangesByCluster: ranges,
					Data:            map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{},
					DataByMetaData:  map[pod_scaler.FullMetadata][]model.Fingerprint{},
				}
			} else if err != nil {
				logrus.WithError(err).Error("Failed to load data from storage.")
				continue
			}
			until := time.Now().Add(-ignoreLatest)
			q := querier{
				lock: &sync.RWMutex{},
				data: cache,
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
			if err := storeCache(dataCache, name, cache, logger); err != nil {
				logger.WithError(err).Error("Failed to write cached data.")
			}
		}
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

func rangeFrom(r prometheusapi.Range) pod_scaler.TimeRange {
	return pod_scaler.TimeRange{
		Start: r.Start,
		End:   r.End,
	}
}

type querier struct {
	lock *sync.RWMutex
	data *pod_scaler.CachedQuery
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
	retention, err := model.ParseDuration(runtime.StorageRetention)
	if err != nil {
		return fmt.Errorf("could not determine Prometheus retention duration: %w", err)
	}
	r := prometheusapi.Range{
		Start: time.Now().Add(-time.Duration(retention)),
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
func (q *querier) uncoveredRanges(cluster string, r pod_scaler.TimeRange) []pod_scaler.TimeRange {
	q.lock.RLock()
	defer q.lock.RUnlock()
	return pod_scaler.UncoveredRanges(r, q.data.RangesByCluster[cluster])
}

// divideRange divides a range into smaller ranges based on how many samples we think is reasonable
// to ask for from Prometheus in one query
func divideRange(uncovered []pod_scaler.TimeRange, step time.Duration, numSteps int64) []prometheusapi.Range {
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

	saveStart := time.Now()
	logger.Debug("Saving response from Prometheus data.")
	q.lock.Lock()
	q.data.Record(c.name, rangeFrom(r), matrix, logger)
	q.lock.Unlock()
	logger.Debugf("Saved Prometheus response after %s.", time.Since(saveStart).Round(time.Second))
}
