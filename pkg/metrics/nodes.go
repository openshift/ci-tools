package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const NodeEventsName = "nodes"

// NodeEvent contains all the node details you want to report.
type NodeEvent struct {
	Node         string             `json:"node"`
	Arch         string             `json:"arch"`
	MachineType  string             `json:"machine_type"`
	MachineID    string             `json:"machine_id"`
	AgeSeconds   int64              `json:"age_seconds"`
	Resources    ResourcesInfo      `json:"resources"`
	UsageStats   ResourceUsageStats `json:"usage_stats"`
	Labels       map[string]string  `json:"labels"`
	Timestamp    time.Time          `json:"timestamp"`
	PollStarted  time.Time          `json:"poll_started"`
	Workloads    []string           `json:"workloads"`
	WatchHistory []WatchPeriod      `json:"watch_history"`
}

// ResourcesInfo contains the capacity and allocatable resources on a node
type ResourcesInfo struct {
	Capacity    ResourceDetails `json:"capacity"`
	Allocatable ResourceDetails `json:"allocatable"`
}

// ResourceDetails contains the details of a specific resource
type ResourceDetails struct {
	CPU              string `json:"cpu"`
	Memory           string `json:"memory"`
	EphemeralStorage string `json:"ephemeral-storage"`
	Pods             string `json:"pods"`
}

// WatchPeriod represents a period when a node was being watched
type WatchPeriod struct {
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

type ResourceUsageStats struct {
	MinCPU int64 `json:"min_cpu_milli"`
	MaxCPU int64 `json:"max_cpu_milli"`
	AvgCPU int64 `json:"avg_cpu_milli"`
	MinMem int64 `json:"min_memory_bytes"`
	MaxMem int64 `json:"max_memory_bytes"`
	AvgMem int64 `json:"avg_memory_bytes"`
}

func (ne *NodeEvent) SetTimestamp(t time.Time) {
	ne.Timestamp = t
}

// nodesMetricsPlugin uses the controller-runtime client to poll only specified nodes.
type nodesMetricsPlugin struct {
	mu     sync.Mutex
	ctx    context.Context
	logger *logrus.Entry

	events        []NodeEvent
	client        ctrlruntimeclient.Client
	metricsClient metricsclient.Interface
	nodesCh       chan string
	nodesToPoll   sets.Set[string]
	watchTimes    *sync.Map
	stopCh        map[string]chan struct{}
	nodeWorkloads *sync.Map
}

// newNodesMetricsPlugin creates a new instance and receives the shared nodes channel.
func newNodesMetricsPlugin(ctx context.Context, logger *logrus.Entry, client ctrlruntimeclient.Client, metricsClient metricsclient.Interface, nodesCh chan string) *nodesMetricsPlugin {
	return &nodesMetricsPlugin{
		ctx:           ctx,
		client:        client,
		nodesCh:       nodesCh,
		logger:        logger.WithField("plugin", "nodes"),
		nodesToPoll:   sets.New[string](),
		watchTimes:    &sync.Map{},
		stopCh:        make(map[string]chan struct{}),
		nodeWorkloads: &sync.Map{},
		metricsClient: metricsClient,
	}
}

func (p *nodesMetricsPlugin) Name() string {
	return NodeEventsName
}

func (p *nodesMetricsPlugin) Record(ev MetricsEvent) {
	ne, ok := ev.(*NodeEvent)
	if !ok {
		return
	}
	p.mu.Lock()

	p.logger.WithField("event", ne).Debug("Recorded node metrics event")
	p.events = append(p.events, *ne)
	p.mu.Unlock()
}

func (p *nodesMetricsPlugin) Events() []MetricsEvent {
	p.mu.Lock()
	defer p.mu.Unlock()

	events := make([]MetricsEvent, 0, len(p.events))
	for i := range p.events {
		events = append(events, &p.events[i])
	}

	return events
}

func (p *nodesMetricsPlugin) Run(ctx context.Context) {
	p.logger.Debug("Starting nodes metrics plugin")
	for {
		select {
		case nodeName := <-p.nodesCh:
			p.mu.Lock()
			if !p.nodesToPoll.Has(nodeName) {
				p.logger.Debugf("Adding new node to poll: %s", nodeName)
				p.nodesToPoll.Insert(nodeName)
				p.watchTimes.Store(nodeName, time.Now().UTC())
				stopCh := make(chan struct{})
				p.stopCh[nodeName] = stopCh

				go p.watchNode(ctx, nodeName, stopCh)
			} else {
				p.logger.Debugf("Node %s already being polled, ignoring", nodeName)
			}
			p.mu.Unlock()
		case <-ctx.Done():
			p.logger.Debug("Context done, stopping nodes metrics plugin")
			p.mu.Lock()
			for nodeName, ch := range p.stopCh {
				p.logger.Debugf("Closing stop channel for node: %s", nodeName)
				close(ch)
			}
			p.mu.Unlock()
			return
		}
	}
}

func (p *nodesMetricsPlugin) watchNode(ctx context.Context, nodeName string, stopCh <-chan struct{}) {
	p.logger.Debugf("Started watching node: %s", nodeName)

	var cpuUtilization []int64
	var memUtilization []int64

	pollStartTime := time.Now()

	p.mu.Lock()
	var currentWorkloads []string

	if value, exists := p.nodeWorkloads.Load(nodeName); exists {
		workloadSet := value.(sets.Set[string])
		currentWorkloads = workloadSet.UnsortedList()
	}

	p.mu.Unlock()

	// Initial poll
	cpuUtil, memUtil := p.pollNodeMetrics(ctx, nodeName)
	cpuUtilization = append(cpuUtilization, cpuUtil)
	memUtilization = append(memUtilization, memUtil)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.createAndStoreNodeEventWithWorkloads(nodeName, pollStartTime, cpuUtilization, memUtilization, currentWorkloads)
			return
		case <-stopCh:
			p.createAndStoreNodeEventWithWorkloads(nodeName, pollStartTime, cpuUtilization, memUtilization, currentWorkloads)
			return
		case <-ticker.C:
			cpuUtil, memUtil := p.pollNodeMetrics(ctx, nodeName)
			cpuUtilization = append(cpuUtilization, cpuUtil)
			memUtilization = append(memUtilization, memUtil)
		}
	}
}

// pollNode polls a node and returns its raw CPU and memory usage (in millicores and bytes).
func (p *nodesMetricsPlugin) pollNodeMetrics(ctx context.Context, nodeName string) (cpuUsageMilli int64, memUsageBytes int64) {
	nodeMetrics, err := p.metricsClient.MetricsV1beta1().NodeMetricses().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		p.logger.WithError(err).Debugf("Failed to fetch live metrics for node %s", nodeName)
		return 0, 0
	}
	cpuUsageMilli = nodeMetrics.Usage.Cpu().MilliValue()
	memUsageBytes = nodeMetrics.Usage.Memory().Value()

	return cpuUsageMilli, memUsageBytes
}

// AddNodeForWorkload adds a node to be watched for a specific workload
func (p *nodesMetricsPlugin) AddNodeForWorkload(nodeName, workloadName string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.logger.Debugf("Adding node %s for workload %s", nodeName, workloadName)

	value, exists := p.nodeWorkloads.Load(nodeName)
	if !exists {
		p.logger.Debugf("First time seeing node %s, initializing", nodeName)
		p.nodeWorkloads.Store(nodeName, sets.New(workloadName))
		p.nodesCh <- nodeName
	} else {
		workloadSet := value.(sets.Set[string])
		workloadSet.Insert(workloadName)
		p.nodeWorkloads.Store(nodeName, workloadSet)
		p.logger.WithField("workload", workloadName).Debugf("Added node %s to watch list for workloads: %v", nodeName, workloadSet.UnsortedList())
	}
	p.watchTimes.Store(nodeName, time.Now().UTC())
}

func (p *nodesMetricsPlugin) RemoveWorkload(workloadName string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.logger.Debugf("Removing workload %s from all nodes", workloadName)

	p.nodeWorkloads.Range(func(key, value any) bool {
		nodeName := key.(string)
		workloads := value.(sets.Set[string])

		if workloads.Has(workloadName) {
			workloads.Delete(workloadName)
			p.logger.Debugf("Removed workload %s from node %s", workloadName, nodeName)

			// If there are no more workloads for this node, stop watching it
			if workloads.Len() == 0 {
				p.logger.Debugf("No more workloads on node %s, cleaning up", nodeName)
				p.nodeWorkloads.Delete(nodeName)

				if p.nodesToPoll.Has(nodeName) {
					p.nodesToPoll.Delete(nodeName)
					p.logger.Debugf("Removed node %s from polling list", nodeName)

					// Stop the watcher for this node
					if stopCh, exists := p.stopCh[nodeName]; exists {
						p.logger.Debugf("Closing stop channel for node %s", nodeName)
						close(stopCh)
						delete(p.stopCh, nodeName)
					}

					p.watchTimes.Delete(nodeName)
					p.logger.Debugf("Removed watch times for node %s", nodeName)
					p.logger.Debugf("Stopped watching node: %s (no more workloads)", nodeName)
				}
			}
			return false
		}
		return true
	})
}

// ExtractPodNode polls until a pod is scheduled and registers its node for metrics collection
func (p *nodesMetricsPlugin) ExtractPodNode(ctx context.Context, namespace, podName, workloadName string, podClient ctrlruntimeclient.Client) {
	p.logger.Debugf("Starting to extract node for pod %s/%s (workload: %s)", namespace, podName, workloadName)

	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()

	err := wait.PollUntilContextCancel(waitCtx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		var pod corev1.Pod
		if err := podClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: podName}, &pod); err != nil {
			if kerrors.IsNotFound(err) {
				p.logger.Debugf("Pod %s/%s not found yet, continuing to poll", namespace, podName)
				return false, nil
			}
			p.logger.WithError(err).Debugf("Failed to get pod %s/%s for node metrics", namespace, podName)
			return false, err
		}

		if pod.Spec.NodeName != "" {
			p.AddNodeForWorkload(pod.Spec.NodeName, workloadName)
			p.logger.Debugf("Added node %s to metrics watch for workload %s", pod.Spec.NodeName, workloadName)
			return true, nil
		}

		p.logger.Debugf("Pod %s/%s exists but not scheduled yet", namespace, podName)
		return false, nil
	})

	if err != nil {
		p.logger.WithError(err).Debugf("Failed to extract node for workload %s (pod: %s/%s)", workloadName, namespace, podName)
	}
}

// createAndStoreNodeEventWithWorkloads creates a final node event with specified workloads and stores it
func (p *nodesMetricsPlugin) createAndStoreNodeEventWithWorkloads(nodeName string, pollStartTime time.Time, cpuUtilization, memUtilization []int64, workloads []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	node := &corev1.Node{}
	if err := p.client.Get(p.ctx, ctrlruntimeclient.ObjectKey{Name: nodeName}, node); err != nil {
		p.logger.WithError(err).Errorf("Failed to get node %s for final event", nodeName)
	}

	minCPU, maxCPU, avgCPU := calculateStats(cpuUtilization)
	minMem, maxMem, avgMem := calculateStats(memUtilization)

	event := &NodeEvent{
		Node:         nodeName,
		Arch:         node.Status.NodeInfo.Architecture,
		MachineType:  node.Labels["node.kubernetes.io/instance-type"],
		MachineID:    node.Status.NodeInfo.MachineID,
		AgeSeconds:   int64(time.Since(node.CreationTimestamp.Time).Seconds()),
		Labels:       node.Labels,
		Timestamp:    time.Now(),
		PollStarted:  pollStartTime,
		Workloads:    workloads,
		WatchHistory: []WatchPeriod{{StartTime: pollStartTime, EndTime: time.Now()}},
		UsageStats:   ResourceUsageStats{MinCPU: minCPU, MaxCPU: maxCPU, AvgCPU: avgCPU, MinMem: minMem, MaxMem: maxMem, AvgMem: avgMem},
	}

	if node.Status.Capacity != nil {
		event.Resources.Capacity.CPU = node.Status.Capacity.Cpu().String()
		event.Resources.Capacity.Memory = node.Status.Capacity.Memory().String()
		event.Resources.Capacity.EphemeralStorage = node.Status.Capacity.StorageEphemeral().String()
		event.Resources.Capacity.Pods = node.Status.Capacity.Pods().String()
	}
	if node.Status.Allocatable != nil {
		event.Resources.Allocatable.CPU = node.Status.Allocatable.Cpu().String()
		event.Resources.Allocatable.Memory = node.Status.Allocatable.Memory().String()
		event.Resources.Allocatable.EphemeralStorage = node.Status.Allocatable.StorageEphemeral().String()
		event.Resources.Allocatable.Pods = node.Status.Allocatable.Pods().String()
	}

	p.events = append(p.events, *event)
	p.logger.Debugf("Created and stored final event for node %s with workloads: %v", nodeName, workloads)
}

func calculateStats(data []int64) (min, max, avg int64) {
	if len(data) == 0 {
		return 0, 0, 0
	}

	min = data[0]
	max = data[0]
	sum := int64(0)

	for _, val := range data {
		sum += val
		if val < min {
			min = val
		}
		if val > max {
			max = val
		}
	}

	avg = sum / int64(len(data))
	return min, max, avg
}
