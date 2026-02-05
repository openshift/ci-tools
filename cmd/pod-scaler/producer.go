package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/bigquery"
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
)

// escapePromQLLabelValue escapes special characters in PromQL label values.
// PromQL label values in selectors must be quoted and have backslashes and quotes escaped.
func escapePromQLLabelValue(value string) string {
	// Replace backslashes first (before replacing quotes)
	value = strings.ReplaceAll(value, `\`, `\\`)
	// Escape double quotes
	value = strings.ReplaceAll(value, `"`, `\"`)
	// Escape newlines
	value = strings.ReplaceAll(value, "\n", `\n`)
	// Escape carriage returns
	value = strings.ReplaceAll(value, "\r", `\r`)
	return value
}

// queriesByMetric returns a mapping of Prometheus query by metric name for all queries we want to execute
func queriesByMetric() map[string]string {
	queries := map[string]string{}
	for _, info := range []struct {
		prefix   string
		selector string
		labels   []string
	}{
		{
			prefix:   ProwjobsCachePrefix,
			selector: `{` + string(podscaler.ProwLabelNameCreated) + `="true",` + string(podscaler.ProwLabelNameJob) + `!="",` + string(podscaler.LabelNameRehearsal) + `=""}`,
			labels:   []string{string(podscaler.ProwLabelNameCreated), string(podscaler.ProwLabelNameContext), string(podscaler.ProwLabelNameOrg), string(podscaler.ProwLabelNameRepo), string(podscaler.ProwLabelNameBranch), string(podscaler.ProwLabelNameJob), string(podscaler.ProwLabelNameType)},
		},
		{
			prefix:   PodsCachePrefix,
			selector: `{` + string(podscaler.LabelNameCreated) + `="true",` + string(podscaler.LabelNameStep) + `=""}`,
			labels:   []string{string(podscaler.LabelNameOrg), string(podscaler.LabelNameRepo), string(podscaler.LabelNameBranch), string(podscaler.LabelNameVariant), string(podscaler.LabelNameTarget), string(podscaler.LabelNameBuild), string(podscaler.LabelNameRelease), string(podscaler.LabelNameApp)},
		},
		{
			prefix:   StepsCachePrefix,
			selector: `{` + string(podscaler.LabelNameCreated) + `="true",` + string(podscaler.LabelNameStep) + `!=""}`,
			labels:   []string{string(podscaler.LabelNameOrg), string(podscaler.LabelNameRepo), string(podscaler.LabelNameBranch), string(podscaler.LabelNameVariant), string(podscaler.LabelNameTarget), string(podscaler.LabelNameStep)},
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

func produce(clients map[string]prometheusapi.API, dataCache Cache, ignoreLatest time.Duration, once bool, bqClient *BigQueryClient) {
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

	// Run measured pod data collection on startup and then periodically if BigQuery client is available
	if bqClient != nil {
		// Use sync.Once to ensure the client is only closed once
		var closeOnce sync.Once
		closeClient := func() {
			closeOnce.Do(func() {
				if err := bqClient.client.Close(); err != nil {
					logrus.WithError(err).Error("Failed to close BigQuery client")
				}
			})
		}
		// Register cleanup handler to close BigQuery client on shutdown
		// This ensures the client is closed when the program exits, not when produce() returns
		// In non-once mode, this handles graceful shutdown. In once mode, this provides a safety net.
		interrupts.OnInterrupt(closeClient)

		// Run measured pod data collection on startup and then periodically
		execute(func() {
			if err := collectMeasuredPodMetrics(interrupts.Context(), clients, bqClient, logrus.WithField("component", "measured-pods-collector")); err != nil {
				logrus.WithError(err).Error("Failed to collect measured pod metrics")
			}
			// In once mode, close the client after work is done since the program will exit
			if once {
				closeClient()
			}
		})
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

func rangeFrom(r prometheusapi.Range) podscaler.TimeRange {
	return podscaler.TimeRange{
		Start: r.Start,
		End:   r.End,
	}
}

type querier struct {
	lock *sync.RWMutex
	data *podscaler.CachedQuery
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

	saveStart := time.Now()
	logger.Debug("Saving response from Prometheus data.")
	q.lock.Lock()
	q.data.Record(c.name, rangeFrom(r), matrix, logger)
	q.lock.Unlock()
	logger.Debugf("Saved Prometheus response after %s.", time.Since(saveStart).Round(time.Second))
}

// collectMeasuredPodMetrics queries Prometheus for completed measured pods and writes to BigQuery
func collectMeasuredPodMetrics(ctx context.Context, clients map[string]prometheusapi.API, bqClient *BigQueryClient, logger *logrus.Entry) error {
	logger.Info("Starting measured pod data collection")

	// Query window: last 4 hours (to catch recently completed pods)
	until := time.Now()
	from := until.Add(-4 * time.Hour)

	// Query Prometheus for pods with pod-scaler=measured label
	// kube_pod_labels metric exposes labels with a label_ prefix
	measuredPodSelector := `{label_pod_scaler="measured",label_created_by_ci="true"}`

	// Query for pod labels to identify measured pods
	podLabelsQuery := fmt.Sprintf(`kube_pod_labels%s`, measuredPodSelector)

	var allMeasuredPods []measuredPodData
	for clusterName, client := range clients {
		clusterLogger := logger.WithField("cluster", clusterName)

		// Query for pod labels to find measured pods
		labelsResult, _, err := client.QueryRange(ctx, podLabelsQuery, prometheusapi.Range{
			Start: from,
			End:   until,
			Step:  30 * time.Second,
		})
		if err != nil {
			clusterLogger.WithError(err).Warn("Failed to query pod labels")
			continue
		}

		// Extract pod names and metadata from labels
		matrix, ok := labelsResult.(model.Matrix)
		if !ok {
			clusterLogger.Warn("Unexpected result type for pod labels query")
			continue
		}

		// For each measured pod, get CPU and memory usage
		// Deduplicate pods by name to avoid processing the same pod multiple times
		seenPods := make(map[string]bool)
		for _, sampleStream := range matrix {
			podName := string(sampleStream.Metric["pod"])
			namespace := string(sampleStream.Metric["namespace"])
			podKey := fmt.Sprintf("%s/%s", namespace, podName)

			// Skip if we've already processed this pod
			if seenPods[podKey] {
				continue
			}
			seenPods[podKey] = true

			podDataList, err := extractMeasuredPodData(ctx, client, sampleStream, from, until, clusterLogger)
			if err != nil {
				clusterLogger.WithError(err).Warn("Failed to extract measured pod data")
				continue
			}
			if len(podDataList) > 0 {
				allMeasuredPods = append(allMeasuredPods, podDataList...)
			}
		}
	}

	// Write all collected data to BigQuery
	if len(allMeasuredPods) > 0 {
		if err := writeMeasuredPodsToBigQuery(ctx, bqClient.client, bqClient.projectID, bqClient.datasetID, allMeasuredPods, logger); err != nil {
			return fmt.Errorf("failed to write measured pods to BigQuery: %w", err)
		}
		logger.Infof("Collected and wrote %d measured pod records to BigQuery", len(allMeasuredPods))
	} else {
		logger.Info("No measured pods found in the query window")
	}

	return nil
}

type measuredPodData struct {
	Org                string
	Repo               string
	Branch             string
	Target             string
	Container          string
	MinCPU             float64
	MaxCPU             float64
	MinMemory          int64
	MaxMemory          int64
	ContainerDurations map[string]time.Duration
	NodeName           string
	PodName            string
	Timestamp          time.Time
	// Node-level metrics for validation (since pod is isolated, node metrics should match)
	NodeMinCPU    float64
	NodeMaxCPU    float64
	NodeMinMemory int64
	NodeMaxMemory int64
}

func extractMeasuredPodData(ctx context.Context, client prometheusapi.API, sampleStream *model.SampleStream, from, until time.Time, logger *logrus.Entry) ([]measuredPodData, error) {
	// Extract pod metadata from labels
	podName := string(sampleStream.Metric["pod"])
	namespace := string(sampleStream.Metric["namespace"])
	org := string(sampleStream.Metric["label_ci_openshift_io_metadata_org"])
	repo := string(sampleStream.Metric["label_ci_openshift_io_metadata_repo"])
	branch := string(sampleStream.Metric["label_ci_openshift_io_metadata_branch"])
	target := string(sampleStream.Metric["label_ci_openshift_io_metadata_target"])

	if org == "" || repo == "" {
		// Skip pods without proper metadata
		return nil, nil
	}

	// Query Prometheus for pod metrics
	cpuResult, memoryResult, containerInfoResult, err := queryPodMetrics(ctx, client, namespace, podName, from, until, logger)
	if err != nil {
		return nil, err
	}

	// Process CPU and memory results to get min/max per container
	minCPUByContainer, maxCPUByContainer := extractCPUUsage(cpuResult)
	minMemoryByContainer, maxMemoryByContainer := extractMemoryUsage(memoryResult)

	// Get node name and query node-level metrics for validation
	nodeName := string(sampleStream.Metric["node"])
	nodeMinCPU, nodeMaxCPU, nodeMinMemory, nodeMaxMemory := queryNodeMetrics(ctx, client, nodeName, from, until, logger)

	// Extract container durations
	containerDurations := extractContainerDurations(containerInfoResult)

	// Build records for each container
	records := buildMeasuredPodRecords(org, repo, branch, target, podName, nodeName, cpuResult, minCPUByContainer, maxCPUByContainer, minMemoryByContainer, maxMemoryByContainer, containerDurations, nodeMinCPU, nodeMaxCPU, nodeMinMemory, nodeMaxMemory, from, until)

	return records, nil
}

// queryPodMetrics queries Prometheus for CPU, memory, and container info metrics
func queryPodMetrics(ctx context.Context, client prometheusapi.API, namespace, podName string, from, until time.Time, logger *logrus.Entry) (model.Value, model.Value, model.Value, error) {
	// Escape label values to prevent PromQL injection
	escapedNamespace := escapePromQLLabelValue(namespace)
	escapedPodName := escapePromQLLabelValue(podName)

	queryRange := prometheusapi.Range{
		Start: from,
		End:   until,
		Step:  30 * time.Second,
	}

	// Query CPU usage for this pod - we'll compute min/max from the time series
	// Using rate with a 3-minute window to get per-second CPU usage
	cpuQuery := fmt.Sprintf(`rate(container_cpu_usage_seconds_total{namespace="%s",pod="%s",container!="POD",container!=""}[3m])`, escapedNamespace, escapedPodName)
	cpuResult, _, err := client.QueryRange(ctx, cpuQuery, queryRange)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to query CPU: %w", err)
	}

	// Query memory usage for this pod - we'll compute min/max from the time series
	memoryQuery := fmt.Sprintf(`container_memory_working_set_bytes{namespace="%s",pod="%s",container!="POD",container!=""}`, escapedNamespace, escapedPodName)
	memoryResult, _, err := client.QueryRange(ctx, memoryQuery, queryRange)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to query memory: %w", err)
	}

	// Query container lifecycle info to get actual container durations
	containerInfoQuery := fmt.Sprintf(`kube_pod_container_info{namespace="%s",pod="%s",container!="POD",container!=""}`, escapedNamespace, escapedPodName)
	containerInfoResult, _, err := client.QueryRange(ctx, containerInfoQuery, queryRange)
	if err != nil {
		logger.WithError(err).Debug("Failed to query container info, will estimate duration")
	}

	return cpuResult, memoryResult, containerInfoResult, nil
}

// extractCPUUsage processes CPU query results and returns min/max CPU usage per container
func extractCPUUsage(cpuResult model.Value) (map[string]float64, map[string]float64) {
	minCPUByContainer := make(map[string]float64)
	maxCPUByContainer := make(map[string]float64)

	if cpuMatrix, ok := cpuResult.(model.Matrix); ok {
		for _, stream := range cpuMatrix {
			container := string(stream.Metric["container"])
			var minCPU, maxCPU float64
			first := true
			for _, sample := range stream.Values {
				cpuVal := float64(sample.Value)
				if first {
					minCPU = cpuVal
					maxCPU = cpuVal
					first = false
				} else {
					if cpuVal < minCPU {
						minCPU = cpuVal
					}
					if cpuVal > maxCPU {
						maxCPU = cpuVal
					}
				}
			}
			if maxCPU > 0 {
				minCPUByContainer[container] = minCPU
				maxCPUByContainer[container] = maxCPU
			}
		}
	}

	return minCPUByContainer, maxCPUByContainer
}

// extractMemoryUsage processes memory query results and returns min/max memory usage per container
func extractMemoryUsage(memoryResult model.Value) (map[string]int64, map[string]int64) {
	minMemoryByContainer := make(map[string]int64)
	maxMemoryByContainer := make(map[string]int64)

	if memoryMatrix, ok := memoryResult.(model.Matrix); ok {
		for _, stream := range memoryMatrix {
			container := string(stream.Metric["container"])
			var minMemory, maxMemory int64
			first := true
			for _, sample := range stream.Values {
				memVal := int64(sample.Value)
				if first {
					minMemory = memVal
					maxMemory = memVal
					first = false
				} else {
					if memVal < minMemory {
						minMemory = memVal
					}
					if memVal > maxMemory {
						maxMemory = memVal
					}
				}
			}
			if maxMemory > 0 {
				minMemoryByContainer[container] = minMemory
				maxMemoryByContainer[container] = maxMemory
			}
		}
	}

	return minMemoryByContainer, maxMemoryByContainer
}

// queryNodeMetrics queries node-level metrics for validation (since pod is isolated, node metrics should match pod metrics)
func queryNodeMetrics(ctx context.Context, client prometheusapi.API, nodeName string, from, until time.Time, logger *logrus.Entry) (float64, float64, int64, int64) {
	var nodeMinCPU, nodeMaxCPU float64
	var nodeMinMemory, nodeMaxMemory int64

	if nodeName == "" {
		return nodeMinCPU, nodeMaxCPU, nodeMinMemory, nodeMaxMemory
	}

	// Escape node name for PromQL
	escapedNodeName := escapePromQLLabelValue(nodeName)
	queryRange := prometheusapi.Range{
		Start: from,
		End:   until,
		Step:  30 * time.Second,
	}

	// Query node CPU utilization - sum all containers on the node
	nodeCPUQuery := fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{node="%s",container!="POD",container!=""}[3m]))`, escapedNodeName)
	nodeCPUResult, _, err := client.QueryRange(ctx, nodeCPUQuery, queryRange)
	if err == nil {
		if nodeCPUMatrix, ok := nodeCPUResult.(model.Matrix); ok && len(nodeCPUMatrix) > 0 {
			first := true
			for _, stream := range nodeCPUMatrix {
				for _, sample := range stream.Values {
					cpuVal := float64(sample.Value)
					if first {
						nodeMinCPU = cpuVal
						nodeMaxCPU = cpuVal
						first = false
					} else {
						if cpuVal < nodeMinCPU {
							nodeMinCPU = cpuVal
						}
						if cpuVal > nodeMaxCPU {
							nodeMaxCPU = cpuVal
						}
					}
				}
			}
		}
	} else {
		logger.WithError(err).Debug("Failed to query node CPU metrics for validation")
	}

	// Query node memory utilization - sum all containers on the node
	nodeMemoryQuery := fmt.Sprintf(`sum(container_memory_working_set_bytes{node="%s",container!="POD",container!=""})`, escapedNodeName)
	nodeMemoryResult, _, err := client.QueryRange(ctx, nodeMemoryQuery, queryRange)
	if err == nil {
		if nodeMemoryMatrix, ok := nodeMemoryResult.(model.Matrix); ok && len(nodeMemoryMatrix) > 0 {
			first := true
			for _, stream := range nodeMemoryMatrix {
				for _, sample := range stream.Values {
					memVal := int64(sample.Value)
					if first {
						nodeMinMemory = memVal
						nodeMaxMemory = memVal
						first = false
					} else {
						if memVal < nodeMinMemory {
							nodeMinMemory = memVal
						}
						if memVal > nodeMaxMemory {
							nodeMaxMemory = memVal
						}
					}
				}
			}
		}
	} else {
		logger.WithError(err).Debug("Failed to query node memory metrics for validation")
	}

	return nodeMinCPU, nodeMaxCPU, nodeMinMemory, nodeMaxMemory
}

// extractContainerDurations extracts container durations from container info query results
func extractContainerDurations(containerInfoResult model.Value) map[string]time.Duration {
	containerDurations := make(map[string]time.Duration)
	if containerInfoMatrix, ok := containerInfoResult.(model.Matrix); ok {
		for _, stream := range containerInfoMatrix {
			container := string(stream.Metric["container"])
			if len(stream.Values) > 0 {
				// Container duration is from first to last sample
				firstTime := stream.Values[0].Timestamp.Time()
				lastTime := stream.Values[len(stream.Values)-1].Timestamp.Time()
				containerDurations[container] = lastTime.Sub(firstTime)
			}
		}
	}
	return containerDurations
}

// buildMeasuredPodRecords builds measuredPodData records for each container
func buildMeasuredPodRecords(org, repo, branch, target, podName, nodeName string, cpuResult model.Value, minCPUByContainer, maxCPUByContainer map[string]float64, minMemoryByContainer, maxMemoryByContainer map[string]int64, containerDurations map[string]time.Duration, nodeMinCPU, nodeMaxCPU float64, nodeMinMemory, nodeMaxMemory int64, from, until time.Time) []measuredPodData {
	var records []measuredPodData

	for container, maxCPU := range maxCPUByContainer {
		minCPU := minCPUByContainer[container]
		maxMemory := maxMemoryByContainer[container]
		minMemory := minMemoryByContainer[container]

		// Use actual container duration if available, otherwise estimate
		containerDuration := containerDurations[container]
		if containerDuration == 0 {
			// Estimate based on query window if we don't have container info
			containerDuration = until.Sub(from)
		}

		// Determine pod execution timestamp (use first sample time if available)
		podTimestamp := until
		if cpuMatrix, ok := cpuResult.(model.Matrix); ok {
			for _, stream := range cpuMatrix {
				if string(stream.Metric["container"]) == container && len(stream.Values) > 0 {
					podTimestamp = stream.Values[0].Timestamp.Time()
					break
				}
			}
		}

		// Use all container durations for this pod, not just the current container
		// This gives us complete duration information for all containers in the pod
		podContainerDurations := make(map[string]time.Duration)
		for c, d := range containerDurations {
			podContainerDurations[c] = d
		}
		// If this container's duration isn't in the map, add it
		if _, exists := podContainerDurations[container]; !exists {
			podContainerDurations[container] = containerDuration
		}

		records = append(records, measuredPodData{
			Org:                org,
			Repo:               repo,
			Branch:             branch,
			Target:             target,
			Container:          container,
			MinCPU:             minCPU,
			MaxCPU:             maxCPU,
			MinMemory:          minMemory,
			MaxMemory:          maxMemory,
			ContainerDurations: podContainerDurations,
			NodeName:           nodeName,
			PodName:            podName,
			Timestamp:          podTimestamp,
			NodeMinCPU:         nodeMinCPU,
			NodeMaxCPU:         nodeMaxCPU,
			NodeMinMemory:      nodeMinMemory,
			NodeMaxMemory:      nodeMaxMemory,
		})
	}

	return records
}

type bigQueryPodMetricsRow struct {
	Org                string    `bigquery:"org"`
	Repo               string    `bigquery:"repo"`
	Branch             string    `bigquery:"branch"`
	Target             string    `bigquery:"target"`
	Container          string    `bigquery:"container"`
	PodName            string    `bigquery:"pod_name"`
	PodScalerLabel     string    `bigquery:"pod_scaler_label"`
	MinCPU             float64   `bigquery:"min_cpu"`
	MaxCPU             float64   `bigquery:"max_cpu"`
	MinMemory          int64     `bigquery:"min_memory"`
	MaxMemory          int64     `bigquery:"max_memory"`
	ContainerDurations string    `bigquery:"container_durations"`
	NodeName           string    `bigquery:"node_name"`
	NodeMinCPU         float64   `bigquery:"node_min_cpu"`
	NodeMaxCPU         float64   `bigquery:"node_max_cpu"`
	NodeMinMemory      int64     `bigquery:"node_min_memory"`
	NodeMaxMemory      int64     `bigquery:"node_max_memory"`
	Created            time.Time `bigquery:"created"`
	LastMeasured       time.Time `bigquery:"last_measured"`
}

func writeMeasuredPodsToBigQuery(ctx context.Context, bqClient *bigquery.Client, _ /* projectID */, datasetID string, pods []measuredPodData, logger *logrus.Entry) error {
	// Use ci_operator_metrics table with additional fields for measured pod data
	inserter := bqClient.Dataset(datasetID).Table("ci_operator_metrics").Inserter()

	// Convert to BigQuery rows
	rows := make([]*bigQueryPodMetricsRow, 0, len(pods))
	for _, pod := range pods {
		// Serialize container durations as JSON
		durationsJSON, err := json.Marshal(pod.ContainerDurations)
		if err != nil {
			logger.WithError(err).Warn("Failed to marshal container durations")
			continue
		}

		rows = append(rows, &bigQueryPodMetricsRow{
			Org:                pod.Org,
			Repo:               pod.Repo,
			Branch:             pod.Branch,
			Target:             pod.Target,
			Container:          pod.Container,
			PodName:            pod.PodName,
			PodScalerLabel:     "measured",
			MinCPU:             pod.MinCPU,
			MaxCPU:             pod.MaxCPU,
			MinMemory:          pod.MinMemory,
			MaxMemory:          pod.MaxMemory,
			ContainerDurations: string(durationsJSON),
			NodeName:           pod.NodeName,
			NodeMinCPU:         pod.NodeMinCPU,
			NodeMaxCPU:         pod.NodeMaxCPU,
			NodeMinMemory:      pod.NodeMinMemory,
			NodeMaxMemory:      pod.NodeMaxMemory,
			Created:            pod.Timestamp,
			LastMeasured:       pod.Timestamp,
		})
	}

	if err := inserter.Put(ctx, rows); err != nil {
		return fmt.Errorf("failed to insert rows: %w", err)
	}

	return nil
}
