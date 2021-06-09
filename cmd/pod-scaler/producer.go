package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openhistogram/circonusllhist"
	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/steps"
)

const (
	MetricNameCPUUsage         = `container_cpu_usage_seconds_total`
	MetricNameMemoryWorkingSet = `container_memory_working_set_bytes`

	containerFilter = `{container!="POD",container!=""}`

	// MaxSamplesPerRequest is the maximum number of samples that Prometheus will allow a client to ask for in
	// one request. We also use this to approximate the maximum number of samples we should be asking any one
	// Prometheus server for at once from many requests.
	MaxSamplesPerRequest = 11000
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
			prefix:   "prowjobs",
			selector: `{` + string(ProwLabelNameCreated) + `="true",` + string(ProwLabelNameJob) + `!="",` + string(LabelNameRehearsal) + `=""}`,
			labels:   []string{string(ProwLabelNameCreated), string(ProwLabelNameContext), string(ProwLabelNameOrg), string(ProwLabelNameRepo), string(ProwLabelNameBranch), string(ProwLabelNameJob), string(ProwLabelNameType)},
		},
		{
			prefix:   "pods",
			selector: `{` + string(LabelNameCreated) + `="true",` + string(LabelNameStep) + `=""}`,
			labels:   []string{string(LabelNameOrg), string(LabelNameRepo), string(LabelNameBranch), string(LabelNameVariant), string(LabelNameTarget), string(LabelNameBuild), string(LabelNameRelease), string(LabelNameApp)},
		},
		{
			prefix:   "steps",
			selector: `{` + string(LabelNameCreated) + `="true",` + string(LabelNameStep) + `!=""}`,
			labels:   []string{string(LabelNameOrg), string(LabelNameRepo), string(LabelNameBranch), string(LabelNameVariant), string(LabelNameTarget), string(LabelNameStep)},
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

func produce(clients map[string]prometheusapi.API, dataCache cache) {
	interrupts.TickLiteral(func() {
		for name, query := range queriesByMetric() {
			name := name
			query := query
			logger := logrus.WithField("metric", name)
			cache, err := loadCache(dataCache, name, logger)
			if errors.Is(err, notExist{}) {
				ranges := map[string][]TimeRange{}
				for cluster := range clients {
					ranges[cluster] = []TimeRange{}
				}
				cache = &CachedQuery{
					Query:           query,
					RangesByCluster: ranges,
					Data:            map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{},
					DataByMetaData:  map[FullMetadata][]model.Fingerprint{},
				}
			} else if err != nil {
				logrus.WithError(err).Error("Failed to load data from storage.")
				return
			}
			now := time.Now()
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
					if err := q.execute(interrupts.Context(), metadata, now); err != nil {
						metadata.logger.WithError(err).Error("Failed to query Prometheus.")
					}
				}()
			}
			go func() { // don't associate this with the context as we want to flush when interrupted
				wg.Wait()
				if err := storeCache(dataCache, name, cache, logger); err != nil {
					logger.WithError(err).Error("Failed to write cached data.")
				}
			}()
		}
	}, 3*time.Hour)
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

func rangeFrom(r prometheusapi.Range) TimeRange {
	return TimeRange{
		Start: r.Start,
		End:   r.End,
	}
}

type querier struct {
	lock *sync.RWMutex
	data *CachedQuery
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
		Start: until.Add(-time.Duration(retention)),
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

	queryStart := time.Now()
	logger := c.logger.WithFields(logrus.Fields{
		"start": r.Start.Format(time.RFC3339),
		"end":   r.End.Format(time.RFC3339),
		"step":  r.Step,
	})
	logger.Info("Initiating queries to Prometheus.")
	uncovered := q.uncoveredRanges(c.name, rangeFrom(r))
	for _, uncoveredRange := range uncovered {
		// Prometheus has practical limits for how much data we can ask for in any one request,
		// so we take each uncovered range and split it into chunks we can ask for.
		start := uncoveredRange.Start
		stop := uncoveredRange.End
		c.lock.RLock()
		numSteps := c.maxSize - 1
		c.lock.RUnlock()
		for {
			if start == uncoveredRange.End {
				break
			}
			if int64(stop.Sub(start)/r.Step) > numSteps {
				stop = start.Add(time.Duration(numSteps) * r.Step)
			}
			c.wg.Add(1)
			go q.executeOverRange(ctx, c, prometheusapi.Range{Start: start, End: stop, Step: r.Step})
			start = stop
			stop = uncoveredRange.End
		}
	}
	c.wg.Wait()
	logger.Infof("Query completed after %s.", time.Since(queryStart).Round(time.Second))
	close(c.errors)
	errLock.Lock()
	return kerrors.NewAggregate(errs)
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
		go q.executeOverRange(ctx, c, prometheusapi.Range{Start: middle, End: r.End, Step: r.Step})
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
	q.data.record(c.name, rangeFrom(r), matrix, logger)
	q.lock.Unlock()
	logger.Debugf("Saved Prometheus response after %s.", time.Since(saveStart).Round(time.Second))
}

// record adds the data in the matrix to the cache and records that the given cluster has
// successfully had this time range queried.
func (q *CachedQuery) record(clusterName string, r TimeRange, matrix model.Matrix, logger *logrus.Entry) {
	q.RangesByCluster[clusterName] = coalesce(append(q.RangesByCluster[clusterName], r))

	for _, stream := range matrix {
		fingerprint := stream.Metric.Fingerprint()
		meta := metadataFromMetric(stream.Metric)
		if strings.HasPrefix(meta.Target, fmt.Sprintf("pull-ci-%s-%s", meta.Org, meta.Repo)) || strings.HasPrefix(meta.Target, fmt.Sprintf("branch-ci-%s-%s", meta.Org, meta.Repo)) {
			// TODO(skuznets): remove this once these time out (June 2021)
			// This is ignoring data from old Prow control plane versions that did not label context or branch.
			continue
		}
		if strings.HasPrefix(meta.Pod, "release-") && meta.Target != "" {
			// TODO(skuznets): remove this once these time out (June 2021)
			// This is hacking to fix data from old CI Operator versions that did not label releases.
			meta.Target = ""
		}
		if strings.HasSuffix(meta.Pod, "-build") && meta.Org == "" {
			// TODO(skuznets): remove this once these time out (June 2021)
			// This is hacking to fix data from old build farm versions that did not label Build Pods.
			continue
		}
		// Metrics are unique in our dataset, so if we've already seen this metric/fingerprint,
		// we're guaranteed to already have recorded it in the indices, and we just need to add
		// the new data. This case will occur if one metric/fingerprint shows up in more than
		// one query range.
		seen := false
		var hist *circonusllhist.Histogram
		if existing, exists := q.Data[fingerprint]; exists {
			hist = existing.Histogram()
			seen = true
		} else {
			hist = circonusllhist.New(circonusllhist.NoLookup())
		}
		for _, value := range stream.Values {
			err := hist.RecordValue(float64(value.Value))
			if err != nil {
				logger.WithError(err).Warn("Failed to insert data into histogram. This should never happen.")
			}
		}
		q.Data[fingerprint] = circonusllhist.NewHistogramWithoutLookups(hist)
		if !seen {
			q.DataByMetaData[meta] = append(q.DataByMetaData[meta], fingerprint)
		}
	}
}

// prune ensures that no identifying set of labels contains more than fifty entries.
// We know that an entry fingerprint can only exist for one fully-qualified label set,
// but if the label set contains a multi-stage step, it will also be referenced in
// the additional per-step index.
func (q *CachedQuery) prune() {
	for meta, values := range q.DataByMetaData {
		var toRemove []model.Fingerprint
		if numFingerprints := len(values); numFingerprints > 50 {
			toRemove = append(toRemove, values[0:numFingerprints-50]...)
			q.DataByMetaData[meta] = values[numFingerprints-50:]
		}
		if len(toRemove) == 0 {
			continue
		}
		for _, item := range toRemove {
			delete(q.Data, item)
		}
	}
}

func metadataFromMetric(metric model.Metric) FullMetadata {
	rawMeta := FullMetadata{
		Metadata: api.Metadata{
			Org:     oneOf(metric, LabelNameOrg, ProwLabelNameOrg),
			Repo:    oneOf(metric, LabelNameRepo, ProwLabelNameRepo),
			Branch:  oneOf(metric, LabelNameBranch, ProwLabelNameBranch),
			Variant: string(metric[LabelNameVariant]),
		},
		Target:    oneOf(metric, LabelNameTarget, ProwLabelNameContext),
		Step:      string(metric[LabelNameStep]),
		Pod:       string(metric[LabelNamePod]),
		Container: string(metric[LabelNameContainer]),
	}
	// we know RPM repos, release Pods and Build Pods do not differ by target, so
	// we can remove those fields when we know we're looking at one of those
	_, buildPod := metric[LabelNameBuild]
	_, releasePod := metric[LabelNameRelease]
	value, set := metric[LabelNameApp]
	rpmRepoPod := set && value == model.LabelValue(steps.RPMRepoName)
	if buildPod || releasePod || rpmRepoPod {
		rawMeta.Target = ""
	}
	// RPM repo Pods are generated for a Deployment, so the name is random and not relevant
	if rpmRepoPod {
		rawMeta.Pod = ""
	}
	// we know the name for ProwJobs is not important
	if _, prowJob := metric[ProwLabelNameCreated]; prowJob {
		rawMeta.Pod = ""
		if rawMeta.Target == "" {
			// periodic and postsubmit jobs do not have a context, but we can try to
			// extract a useful name for the job by processing the full name, with the
			// caveat that labels have a finite length limit and the most specific data
			// is in the suffix of the job name, so we will alias jobs here whose names
			// are too long
			rawMeta.Target = syntheticContextFromJob(rawMeta.Metadata, metric)
		}
	}
	return rawMeta
}

func oneOf(metric model.Metric, labels ...model.LabelName) string {
	for _, label := range labels {
		if value, set := metric[label]; set {
			return string(value)
		}
	}
	return ""
}

func syntheticContextFromJob(meta api.Metadata, metric model.Metric) string {
	job, jobLabeled := metric[ProwLabelNameJob]
	if !jobLabeled {
		// this should not happen, but if it does, we can't deduce a job name
		return ""
	}
	jobType, typeLabeled := metric[ProwLabelNameType]
	if !typeLabeled {
		// this should not happen, but if it does, we can't deduce a job name
		return ""
	}
	if prowv1.ProwJobType(jobType) == prowv1.PeriodicJob && meta.Repo == "" {
		// this periodic has no repo associated with it, no use to strip any prefix
		return string(job)
	}
	var prefix string
	switch prowv1.ProwJobType(jobType) {
	case prowv1.PresubmitJob, prowv1.BatchJob:
		prefix = jobconfig.PresubmitPrefix
	case prowv1.PostsubmitJob:
		prefix = jobconfig.PostsubmitPrefix
	case prowv1.PeriodicJob:
		prefix = jobconfig.PeriodicPrefix
	default:
		// this should not happen, but if it does, we can't deduce a job name
		return ""
	}
	namePrefix := meta.JobName(prefix, "")
	if len(namePrefix) >= len(job) {
		// the job label truncated away any useful information we would have had
		return ""
	}
	return strings.TrimPrefix(string(job), namePrefix)
}

// uncoveredRanges determines the largest subset ranges of r that are not covered by
// existing data in the querier.
func (q *querier) uncoveredRanges(cluster string, r TimeRange) []TimeRange {
	q.lock.RLock()
	defer q.lock.RUnlock()
	return uncoveredRanges(r, q.data.RangesByCluster[cluster])
}

func uncoveredRanges(r TimeRange, coverage []TimeRange) []TimeRange {
	var covered []TimeRange
	for _, extent := range coverage {
		startsInside := within(extent.Start, r)
		endsInside := within(extent.End, r)
		switch {
		case startsInside && endsInside:
			covered = append(covered, extent)
		case startsInside && !endsInside:
			covered = append(covered, TimeRange{
				Start: extent.Start,
				End:   r.End,
			})
		case !startsInside && endsInside:
			covered = append(covered, TimeRange{
				Start: r.Start,
				End:   extent.End,
			})
		case extent.Start.Before(r.Start) && extent.End.After(r.End):
			covered = append(covered, TimeRange{
				Start: r.Start,
				End:   r.End,
			})
		}
	}
	sort.Slice(covered, func(i, j int) bool {
		return covered[i].Start.Before(covered[j].Start)
	})
	covered = coalesce(covered)

	if len(covered) == 0 {
		return []TimeRange{r}
	}
	var uncovered []TimeRange
	if !covered[0].Start.Equal(r.Start) {
		uncovered = append(uncovered, TimeRange{Start: r.Start, End: covered[0].Start})
	}
	for i := 0; i < len(covered)-1; i++ {
		uncovered = append(uncovered, TimeRange{Start: covered[i].End, End: covered[i+1].Start})
	}
	if !covered[len(covered)-1].End.Equal(r.End) {
		uncovered = append(uncovered, TimeRange{Start: covered[len(covered)-1].End, End: r.End})
	}
	return uncovered
}

// within determines if the time falls within the range
func within(t time.Time, r TimeRange) bool {
	return (r.Start.Equal(t) || r.Start.Before(t)) && (r.End.Equal(t) || r.End.After(t))
}

// coalesce minimizes the number of timeRanges that are needed to describe a set of times.
// The output is sorted by start time of the remaining ranges.
func coalesce(input []TimeRange) []TimeRange {
	for {
		coalesced := coalesceOnce(input)
		if len(coalesced) == len(input) {
			sort.Slice(coalesced, func(i, j int) bool {
				return coalesced[i].Start.Before(coalesced[j].Start)
			})
			return coalesced
		}
		input = coalesced
	}
}

func coalesceOnce(input []TimeRange) []TimeRange {
	for i := 0; i < len(input); i++ {
		for j := i; j < len(input); j++ {
			var coalesced *TimeRange
			if input[i].End.Equal(input[j].Start) {
				coalesced = &TimeRange{
					Start: input[i].Start,
					End:   input[j].End,
				}
			}
			if input[i].Start.Equal(input[j].End) {
				coalesced = &TimeRange{
					Start: input[j].Start,
					End:   input[i].End,
				}
			}
			if coalesced != nil {
				return append(input[:i], append(input[i+1:j], append(input[j+1:], *coalesced)...)...)
			}
		}
	}
	return input
}
