package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/prow/pkg/interrupts"

	"github.com/openshift/ci-tools/pkg/api"
	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
)

const (
	// PodScalerLabelKey is the label we use to mark pods as either "normal" or "measured".
	// This helps the scheduler know which pods can share nodes and which need isolation.
	PodScalerLabelKey = "pod-scaler"
	// PodScalerLabelValueNormal means this pod can run alongside other CI workloads.
	// These pods use the regular resource recommendations from Prometheus data.
	PodScalerLabelValueNormal = "normal"
	// PodScalerLabelValueMeasured means this pod needs to run on an isolated node (no other CI pods).
	// We do this so we can accurately measure what resources it actually uses without interference.
	PodScalerLabelValueMeasured = "measured"
	// MeasuredPodDataRetentionDays is how long we trust measured pod data before we need to re-measure.
	// After 10 days, we mark the pod as "measured" again to get fresh data.
	MeasuredPodDataRetentionDays = 10
	// BigQueryRefreshInterval is how often we pull fresh data from BigQuery.
	// We refresh once a day to keep our cache up to date with the latest measured pod metrics.
	BigQueryRefreshInterval = 24 * time.Hour
	// MeasuredPodResourceBuffer is the safety buffer we add to measured resource utilization.
	// We apply 20% buffer (1.2x) to measured CPU and memory usage to account for variability.
	MeasuredPodResourceBuffer = 1.2
)

// MeasuredPodData holds what we learned about a pod when it ran in isolation.
// This tells us the real resource needs without interference from other workloads.
type MeasuredPodData struct {
	// Metadata tells us which pod this is (org, repo, branch, container name, etc.)
	Metadata podscaler.FullMetadata `json:"metadata"`
	// MaxCPUUtilization is the highest CPU usage we saw when this pod ran alone (in cores).
	// This is the real number - not limited by node contention.
	MaxCPUUtilization float64
	// MaxMemoryUtilization is the highest memory usage we saw when this pod ran alone (in bytes).
	// Again, this is the real number without interference.
	MaxMemoryUtilization int64
	// LastMeasuredTime tells us when we last ran this pod in isolation.
	// If it's been more than 10 days, we should measure it again.
	LastMeasuredTime time.Time
	// ContainerDurations tells us how long each container ran.
	// We use this to find the longest-running container, which gets the resource increases.
	ContainerDurations map[string]time.Duration
}

// MeasuredPodCache keeps measured pod data in memory so we can quickly check if a pod needs measuring.
// We refresh this from BigQuery once a day, so it's always reasonably fresh.
type MeasuredPodCache struct {
	mu     sync.RWMutex
	data   map[podscaler.FullMetadata]*MeasuredPodData
	logger *logrus.Entry
}

// NewMeasuredPodCache creates a new cache for measured pod data
func NewMeasuredPodCache(logger *logrus.Entry) *MeasuredPodCache {
	return &MeasuredPodCache{
		data:   make(map[podscaler.FullMetadata]*MeasuredPodData),
		logger: logger,
	}
}

// Get retrieves measured pod data for the given metadata
func (c *MeasuredPodCache) Get(meta podscaler.FullMetadata) (*MeasuredPodData, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, exists := c.data[meta]
	return data, exists
}

// Update updates the cache with new data
func (c *MeasuredPodCache) Update(data map[podscaler.FullMetadata]*MeasuredPodData) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = data
	c.logger.Infof("Updated measured pod cache with %d entries", len(data))
}

// BigQueryClient handles queries to BigQuery for measured pod data
type BigQueryClient struct {
	client      *bigquery.Client
	projectID   string
	datasetID   string
	logger      *logrus.Entry
	cache       *MeasuredPodCache
	lastRefresh time.Time
}

// NewBigQueryClient creates a new BigQuery client for measured pods
func NewBigQueryClient(projectID, datasetID, credentialsFile string, cache *MeasuredPodCache, logger *logrus.Entry) (*BigQueryClient, error) {
	ctx := interrupts.Context()
	var opts []option.ClientOption
	if credentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(credentialsFile))
	}

	client, err := bigquery.NewClient(ctx, projectID, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create BigQuery client: %w", err)
	}

	bq := &BigQueryClient{
		client:    client,
		projectID: projectID,
		datasetID: datasetID,
		logger:    logger,
		cache:     cache,
	}

	// Initial refresh on startup
	if err := bq.Refresh(ctx); err != nil {
		logger.WithError(err).Warn("Failed to refresh BigQuery data on startup")
	}

	// Schedule daily refresh
	interrupts.TickLiteral(func() {
		if err := bq.Refresh(interrupts.Context()); err != nil {
			logger.WithError(err).Error("Failed to refresh BigQuery data")
		}
	}, BigQueryRefreshInterval)

	return bq, nil
}

// Refresh pulls the latest measured pod data from BigQuery and updates our cache.
// We call this on startup and then once per day to keep the data fresh.
func (bq *BigQueryClient) Refresh(ctx context.Context) error {
	bq.logger.Info("Refreshing measured pod data from BigQuery")

	// Query ci_operator_metrics table for measured pod data
	// This queries the table that stores max CPU/memory utilization for pods that ran with the "measured" label
	query := bq.client.Query(fmt.Sprintf(`
		SELECT
			org,
			repo,
			branch,
			target,
			container,
			pod_name,
			MAX(max_cpu) as max_cpu,
			MAX(max_memory) as max_memory,
			MAX(created) as last_measured,
			ANY_VALUE(container_durations) as container_durations
		FROM
			`+"`%s.%s.ci_operator_metrics`"+`
		WHERE
			pod_scaler_label = 'measured'
			AND created >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL %d DAY)
		GROUP BY
			org, repo, branch, target, container, pod_name
	`, bq.projectID, bq.datasetID, MeasuredPodDataRetentionDays))

	query.QueryConfig.Labels = map[string]string{
		"client-application": "pod-scaler",
		"query-details":      "measured-pod-cpu-utilization",
	}

	it, err := query.Read(ctx)
	if err != nil {
		return fmt.Errorf("failed to execute BigQuery query: %w", err)
	}

	data := make(map[podscaler.FullMetadata]*MeasuredPodData)
	for {
		var row struct {
			Org                string    `bigquery:"org"`
			Repo               string    `bigquery:"repo"`
			Branch             string    `bigquery:"branch"`
			Target             string    `bigquery:"target"`
			Container          string    `bigquery:"container"`
			PodName            string    `bigquery:"pod_name"`
			MaxCPU             float64   `bigquery:"max_cpu"`
			MaxMemory          int64     `bigquery:"max_memory"`
			LastMeasured       time.Time `bigquery:"last_measured"`
			ContainerDurations string    `bigquery:"container_durations"` // JSON string
		}
		if err := it.Next(&row); err != nil {
			if err == iterator.Done {
				break
			}
			bq.logger.WithError(err).Warn("Failed to read BigQuery row")
			continue
		}

		meta := podscaler.FullMetadata{
			Metadata: api.Metadata{
				Org:    row.Org,
				Repo:   row.Repo,
				Branch: row.Branch,
			},
			Target:    row.Target,
			Pod:       row.PodName,
			Container: row.Container,
		}

		// Parse container_durations JSON string into map[string]time.Duration
		// BigQuery stores this as a JSON string. time.Duration serializes as int64 (nanoseconds)
		// so the format is: {"container1": 3600000000000, "container2": 245000000000}
		containerDurations := make(map[string]time.Duration)
		if row.ContainerDurations != "" {
			var durationsMap map[string]int64
			if err := json.Unmarshal([]byte(row.ContainerDurations), &durationsMap); err == nil {
				for container, nanoseconds := range durationsMap {
					containerDurations[container] = time.Duration(nanoseconds)
				}
			} else {
				// Fallback: try parsing as string format (for backwards compatibility)
				var durationsMapStr map[string]string
				if err := json.Unmarshal([]byte(row.ContainerDurations), &durationsMapStr); err == nil {
					for container, durationStr := range durationsMapStr {
						if duration, err := time.ParseDuration(durationStr); err == nil {
							containerDurations[container] = duration
						}
					}
				}
			}
		}

		data[meta] = &MeasuredPodData{
			Metadata:             meta,
			MaxCPUUtilization:    row.MaxCPU,
			MaxMemoryUtilization: row.MaxMemory,
			LastMeasuredTime:     row.LastMeasured,
			ContainerDurations:   containerDurations,
		}
	}

	bq.cache.Update(data)
	bq.lastRefresh = time.Now()
	bq.logger.Infof("Refreshed measured pod data: %d entries, last refresh: %v", len(data), bq.lastRefresh)
	return nil
}

// ShouldBeMeasured checks if we need to run this pod in isolation to measure it.
// We measure it if we've never measured it before, or if it's been more than 10 days
// since the last measurement (data gets stale).
func (bq *BigQueryClient) ShouldBeMeasured(meta podscaler.FullMetadata) bool {
	data, exists := bq.cache.Get(meta)
	if !exists {
		// Never measured this pod before, so we should measure it now.
		return true
	}

	// If it's been more than 10 days, the data is stale and we should re-measure.
	cutoff := time.Now().Add(-MeasuredPodDataRetentionDays * 24 * time.Hour)
	return data.LastMeasuredTime.Before(cutoff)
}

// GetMeasuredData returns the measured pod data for the given metadata
func (bq *BigQueryClient) GetMeasuredData(meta podscaler.FullMetadata) (*MeasuredPodData, bool) {
	return bq.cache.Get(meta)
}

// ClassifyPod decides whether this pod should run in isolation ("measured") or with others ("normal").
// We check each container - if any container needs measuring, the whole pod gets the "measured" label.
// This label tells the scheduler to keep it away from other CI workloads.
func ClassifyPod(pod *corev1.Pod, bqClient *BigQueryClient, logger *logrus.Entry) {
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}

	// If BigQuery client is not available, default to normal to avoid overwhelming isolated nodes.
	if bqClient == nil {
		pod.Labels[PodScalerLabelKey] = PodScalerLabelValueNormal
		logger.Debugf("Classified pod as normal - BigQuery client not available")
		return
	}

	// Check each container to see if we need fresh measurement data for it.
	// If any container needs measuring, we mark the whole pod as "measured".
	for _, container := range pod.Spec.Containers {
		fullMeta := podscaler.MetadataFor(pod.ObjectMeta.Labels, pod.ObjectMeta.Name, container.Name)
		if bqClient.ShouldBeMeasured(fullMeta) {
			// This container needs fresh data, so mark the pod as measured.
			pod.Labels[PodScalerLabelKey] = PodScalerLabelValueMeasured
			logger.Debugf("Classified pod as measured - will run on isolated node")
			return
		}
	}

	// No container needs measuring, so mark as normal.
	pod.Labels[PodScalerLabelKey] = PodScalerLabelValueNormal
	logger.Debugf("Classified pod as normal - can share node with other workloads")
}

// AddPodAntiAffinity sets up scheduling rules so measured pods get isolated nodes.
// Measured pods avoid ALL other pod-scaler labeled pods (they need the whole node).
// Normal pods avoid measured pods (so measured pods can have their isolation).
func AddPodAntiAffinity(pod *corev1.Pod, logger *logrus.Entry) {
	podScalerLabel, hasLabel := pod.Labels[PodScalerLabelKey]
	if !hasLabel {
		logger.Debug("Pod does not have pod-scaler label, skipping anti-affinity")
		return
	}

	// Set up the affinity rules if they don't exist yet.
	if pod.Spec.Affinity == nil {
		pod.Spec.Affinity = &corev1.Affinity{}
	}
	if pod.Spec.Affinity.PodAntiAffinity == nil {
		pod.Spec.Affinity.PodAntiAffinity = &corev1.PodAntiAffinity{}
	}

	var requiredTerms []corev1.PodAffinityTerm

	switch podScalerLabel {
	case PodScalerLabelValueMeasured:
		// Measured pods need complete isolation - they can't share a node with ANY other pod-scaler pod.
		// This ensures they get the full node resources for accurate measurement.
		requiredTerms = append(requiredTerms, corev1.PodAffinityTerm{
			LabelSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      PodScalerLabelKey,
						Operator: metav1.LabelSelectorOpExists,
					},
				},
			},
			TopologyKey: "kubernetes.io/hostname",
		})
		logger.Debug("Added podAntiAffinity for measured pod - will avoid all pod-scaler labeled pods")
	case PodScalerLabelValueNormal:
		// Normal pods stay away from measured pods so measured pods can have their isolation.
		requiredTerms = append(requiredTerms, corev1.PodAffinityTerm{
			LabelSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      PodScalerLabelKey,
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{PodScalerLabelValueMeasured},
					},
				},
			},
			TopologyKey: "kubernetes.io/hostname",
		})
		logger.Debug("Added podAntiAffinity for normal pod - will avoid measured pods")
	}

	// Merge with existing anti-affinity terms instead of overwriting
	existingTerms := pod.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if existingTerms != nil {
		requiredTerms = append(existingTerms, requiredTerms...)
	}
	pod.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = requiredTerms
}

// ApplyMeasuredPodResources uses the real resource data we collected when this pod ran in isolation.
// We only increase resources for the longest-running container (the main workload), not all containers.
// This is based on actual measured usage, not Prometheus data that might be skewed by node contention.
// This function applies resources to pods labeled "normal" (which have measured data), not "measured" (which need measurement).
func ApplyMeasuredPodResources(pod *corev1.Pod, bqClient *BigQueryClient, logger *logrus.Entry) {
	if bqClient == nil {
		return
	}

	podScalerLabel, hasLabel := pod.Labels[PodScalerLabelKey]
	// Apply measured resources to pods labeled "normal" (which have fresh measured data)
	// Pods labeled "measured" don't have data yet - they're being measured for the first time.
	if !hasLabel || podScalerLabel != PodScalerLabelValueNormal {
		return
	}

	// Find the container that runs the longest - that's the one that needs the resource increases.
	// The other containers are usually sidecars or helpers that don't need as much.
	var longestContainer *corev1.Container
	var longestDuration time.Duration
	var measuredData *MeasuredPodData

	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		fullMeta := podscaler.MetadataFor(pod.ObjectMeta.Labels, pod.ObjectMeta.Name, container.Name)

		containerMeasuredData, exists := bqClient.GetMeasuredData(fullMeta)
		if !exists {
			continue
		}

		// Track which container ran the longest - that's our main workload.
		if duration, ok := containerMeasuredData.ContainerDurations[container.Name]; ok {
			if duration > longestDuration {
				longestDuration = duration
				longestContainer = container
				measuredData = containerMeasuredData
			}
		}
	}

	// If we don't have duration data, just use the first container with measured data as a fallback.
	if longestContainer == nil && len(pod.Spec.Containers) > 0 {
		for i := range pod.Spec.Containers {
			container := &pod.Spec.Containers[i]
			fullMeta := podscaler.MetadataFor(pod.ObjectMeta.Labels, pod.ObjectMeta.Name, container.Name)
			if containerMeasuredData, exists := bqClient.GetMeasuredData(fullMeta); exists {
				longestContainer = container
				measuredData = containerMeasuredData
				break
			}
		}
	}

	if longestContainer == nil || measuredData == nil {
		logger.Debug("No containers with measured data found for measured pod resource application")
		return
	}

	// Set up the resource requests if they don't exist yet.
	if longestContainer.Resources.Requests == nil {
		longestContainer.Resources.Requests = corev1.ResourceList{}
	}

	// Apply CPU request based on what we actually saw when it ran in isolation, with safety buffer.
	cpuRequest := measuredData.MaxCPUUtilization * MeasuredPodResourceBuffer
	if cpuRequest > 0 {
		cpuQuantity := resource.NewMilliQuantity(int64(cpuRequest*1000), resource.DecimalSI)
		longestContainer.Resources.Requests[corev1.ResourceCPU] = *cpuQuantity
		logger.Debugf("Applied CPU request %v to container %s based on measured data", cpuQuantity, longestContainer.Name)
	}

	// Apply memory request based on what we actually saw, with safety buffer.
	if measuredData.MaxMemoryUtilization > 0 {
		memoryRequest := int64(float64(measuredData.MaxMemoryUtilization) * MeasuredPodResourceBuffer)
		memoryQuantity := resource.NewQuantity(memoryRequest, resource.BinarySI)
		longestContainer.Resources.Requests[corev1.ResourceMemory] = *memoryQuantity
		logger.Debugf("Applied memory request %v to container %s based on measured data", memoryQuantity, longestContainer.Name)
	}
}
