package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const NodeEventsName = "nodes"

// NodeEvent contains all the node details you want to report.
type NodeEvent struct {
	Node         string                `json:"node"`
	Arch         string                `json:"arch"`
	MachineType  string                `json:"machine_type"`
	MachineID    string                `json:"machine_id"`
	AgeSeconds   int64                 `json:"age_seconds"`
	Resources    ResourcesInfo         `json:"resources"`
	Reservation  SystemReservationInfo `json:"system_reservation"`
	Labels       map[string]string     `json:"labels"`
	Timestamp    time.Time             `json:"timestamp"`
	PollStarted  time.Time             `json:"poll_started"`
	Workloads    []string              `json:"workloads"`
	WatchHistory []WatchPeriod         `json:"watch_history"`
}

// WatchPeriod represents a period when a node was being watched
type WatchPeriod struct {
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

type ResourcesInfo struct {
	CPUCores      int   `json:"cpu_cores"`
	ReservedCores int   `json:"reserved_cores"`
	MemoryKB      int64 `json:"memory_kb"`
}

type SystemReservationInfo struct {
	MinCPUPercent     int `json:"min_cpu_reserved_percent"`
	MaxCPUPercent     int `json:"max_cpu_reserved_percent"`
	AvgCPUPercent     int `json:"avg_cpu_reserved_percent"`
	MinMemPercent     int `json:"min_memory_reserved_percent"`
	MaxMemPercent     int `json:"max_memory_reserved_percent"`
	AvgMemPercent     int `json:"avg_mem_reserved_percent"`
	MinStoragePercent int `json:"min_storage_reserved_percent"`
	MaxStoragePercent int `json:"max_storage_reserved_percent"`
	AvgStoragePercent int `json:"avg_storage_reserved_percent"`
}

func (ne *NodeEvent) SetTimestamp(t time.Time) {
	ne.Timestamp = t
}

// nodesMetricsPlugin uses the controller-runtime client to poll only specified nodes.
type nodesMetricsPlugin struct {
	mu          sync.Mutex
	logger      *logrus.Entry
	events      []NodeEvent
	client      ctrlruntimeclient.Client
	nodesCh     chan string
	nodesToPoll sets.Set[string]
	watchTimes  map[string]time.Time
	stopCh      map[string]chan struct{}
	// Track which workloads are using each node
	nodeWorkloads map[string]sets.Set[string]
	// Track all watch periods for each node
	watchPeriods map[string][]WatchPeriod
}

// newNodesMetricsPlugin creates a new instance and receives the shared nodes channel.
func newNodesMetricsPlugin(client ctrlruntimeclient.Client, nodesCh chan string) *nodesMetricsPlugin {
	return &nodesMetricsPlugin{
		client:        client,
		nodesCh:       nodesCh,
		logger:        logrus.WithField("component", "metricsAgent").WithField("plugin", "nodes"),
		nodesToPoll:   sets.New[string](),
		watchTimes:    make(map[string]time.Time),
		stopCh:        make(map[string]chan struct{}),
		nodeWorkloads: make(map[string]sets.Set[string]),
		watchPeriods:  make(map[string][]WatchPeriod),
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
				p.watchTimes[nodeName] = time.Now()
				stopCh := make(chan struct{})
				p.stopCh[nodeName] = stopCh

				go p.watchNode(ctx, nodeName, stopCh)
			} else {
				p.logger.Debugf("Node %s already being polled, ignoring", nodeName)
			}
			p.mu.Unlock()
		case <-ctx.Done():
			p.logger.Info("Context done, stopping nodes metrics plugin")
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

	cpuUtilization := []int{}
	memUtilization := []int{}
	storageUtilization := []int{}
	pollStartTime := time.Now()

	p.mu.Lock()
	var currentWorkloads []string
	if workloadSet, exists := p.nodeWorkloads[nodeName]; exists {
		currentWorkloads = workloadSet.UnsortedList()
	}
	p.mu.Unlock()

	// Initial poll
	cpuUtil, memUtil, storageUtil := p.pollNode(ctx, nodeName)
	cpuUtilization = append(cpuUtilization, cpuUtil)
	memUtilization = append(memUtilization, memUtil)
	storageUtilization = append(storageUtilization, storageUtil)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.createAndStoreNodeEventWithWorkloads(nodeName, pollStartTime, cpuUtilization, memUtilization, storageUtilization, currentWorkloads)
			return
		case <-stopCh:
			p.createAndStoreNodeEventWithWorkloads(nodeName, pollStartTime, cpuUtilization, memUtilization, storageUtilization, currentWorkloads)
			return
		case <-ticker.C:
			p.logger.Debugf("Ticker triggered for node: %s", nodeName)
			cpuUtil, memUtil, storageUtil := p.pollNode(ctx, nodeName)
			cpuUtilization = append(cpuUtilization, cpuUtil)
			memUtilization = append(memUtilization, memUtil)
			storageUtilization = append(storageUtilization, storageUtil)
		}
	}
}

// pollNode polls a node and returns its CPU, memory, and storage utilization percentages.
//
// How it works:
// 1. Gets the node object from Kubernetes API
// 2. Extracts three key resource metrics from node.Status:
//   - Capacity: Total resources physically available on the node
//   - Allocatable: Resources available for scheduling pods (Capacity minus system reservations)
//
// What we're measuring:
//
//   - CPU: Percentage of CPU cores reserved by the system (kubelet, OS, etc.)
//     Formula: 100 - (allocatable * 100 / capacity)
//     Example: Node has 4 cores, 3 allocatable, so 25% reserved for system
//
//   - Memory: Percentage of memory reserved by the system
//     Formula: 100 - (allocatable * 100 / capacity)
//     Example: Node has 8GB, 6GB allocatable, so 25% reserved for system
//
//   - Storage: Percentage of ephemeral storage reserved by the system
//     Formula: 100 - (allocatable * 100 / capacity)
//     Example: Node has 100GB, 80GB allocatable, so 20% reserved by system
func (p *nodesMetricsPlugin) pollNode(ctx context.Context, nodeName string) (cpuUtil, memUtil, storageUtil int) {
	p.logger.Debugf("Polling node: %s", nodeName)
	node := &corev1.Node{}
	if err := p.client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: nodeName}, node); err != nil {
		p.logger.WithError(err).Errorf("Failed to get node %s", nodeName)
		return 0, 0, 0
	}
	p.logger.Debugf("Successfully retrieved node %s", nodeName)

	cpuCapacity := node.Status.Capacity.Cpu().MilliValue()
	cpuAllocatable := node.Status.Allocatable.Cpu().MilliValue()
	if cpuCapacity > 0 {
		cpuUtil = int(100 - (cpuAllocatable * 100 / cpuCapacity))
	}

	memCapacity := node.Status.Capacity.Memory().Value()
	memAllocatable := node.Status.Allocatable.Memory().Value()
	if memCapacity > 0 {
		memUtil = int(100 - (memAllocatable * 100 / memCapacity))
	}

	if storageCapacity, ok := node.Status.Capacity[corev1.ResourceEphemeralStorage]; ok {
		if storageAllocatable, ok := node.Status.Allocatable[corev1.ResourceEphemeralStorage]; ok {
			capacityBytes := storageCapacity.Value()
			allocatableBytes := storageAllocatable.Value()
			if capacityBytes > 0 {
				storageUtil = int(100 - (allocatableBytes * 100 / capacityBytes))
			}
		}
	}

	p.logger.Debugf("Node %s stats - CPU: %d%%, Memory: %d%%, Storage: %d%%", nodeName, cpuUtil, memUtil, storageUtil)
	return
}

// AddNodeForWorkload adds a node to be watched for a specific workload
func (p *nodesMetricsPlugin) AddNodeForWorkload(nodeName, workloadName string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.logger.Debugf("Adding node %s for workload %s", nodeName, workloadName)

	if _, exists := p.nodeWorkloads[nodeName]; !exists {
		p.logger.Debugf("First time seeing node %s, initializing", nodeName)
		p.nodeWorkloads[nodeName] = sets.New[string]()
		p.watchTimes[nodeName] = time.Now().UTC()
		p.nodesCh <- nodeName
	}

	p.nodeWorkloads[nodeName].Insert(workloadName)

	p.logger.Debugf("Added node %s to watch list for workload %s (total workloads on node: %d)", nodeName, workloadName, p.nodeWorkloads[nodeName].Len())
}

func (p *nodesMetricsPlugin) RemoveWorkload(workloadName string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.logger.Debugf("Removing workload %s from all nodes", workloadName)

	for nodeName, workloads := range p.nodeWorkloads {
		if workloads.Has(workloadName) {
			workloads.Delete(workloadName)
			p.logger.Debugf("Removed workload %s from node %s", workloadName, nodeName)

			// If there are no more workloads for this node, stop watching it
			if workloads.Len() == 0 {
				p.logger.Debugf("No more workloads on node %s, cleaning up", nodeName)
				delete(p.nodeWorkloads, nodeName)

				if p.nodesToPoll.Has(nodeName) {
					p.nodesToPoll.Delete(nodeName)
					p.logger.Debugf("Removed node %s from polling list", nodeName)

					// Stop the watcher for this node
					if stopCh, exists := p.stopCh[nodeName]; exists {
						p.logger.Debugf("Closing stop channel for node %s", nodeName)
						close(stopCh)
						delete(p.stopCh, nodeName)
					}

					delete(p.watchTimes, nodeName)
					p.logger.Debugf("Removed watch times for node %s", nodeName)
					p.logger.Debugf("Stopped watching node: %s (no more workloads)", nodeName)
				}
			}
			return
		}
	}
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
			p.logger.WithError(err).Warnf("Failed to get pod %s/%s for node metrics", namespace, podName)
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
		p.logger.WithError(err).Warnf("Failed to extract node for workload %s (pod: %s/%s)", workloadName, namespace, podName)
	}
}

// createAndStoreNodeEventWithWorkloads creates a final node event with specified workloads and stores it
func (p *nodesMetricsPlugin) createAndStoreNodeEventWithWorkloads(nodeName string, pollStartTime time.Time, cpuUtilization, memUtilization, storageUtilization []int, workloads []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	node := &corev1.Node{}
	ctx := context.Background()
	if err := p.client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: nodeName}, node); err != nil {
		p.logger.WithError(err).Errorf("Failed to get node %s for final event", nodeName)
	}

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
	}

	if node.Status.Capacity != nil {
		event.Resources.CPUCores = int(node.Status.Capacity.Cpu().MilliValue() / 1000)
		event.Resources.MemoryKB = node.Status.Capacity.Memory().Value() / 1024
	}
	if node.Status.Allocatable != nil {
		allocatableCores := int(node.Status.Allocatable.Cpu().MilliValue() / 1000)
		event.Resources.ReservedCores = event.Resources.CPUCores - allocatableCores
	}

	if len(cpuUtilization) > 0 {
		minCPU, maxCPU, avgCPU := calculateStats(cpuUtilization)
		event.Reservation.MinCPUPercent = minCPU
		event.Reservation.MaxCPUPercent = maxCPU
		event.Reservation.AvgCPUPercent = avgCPU
	}

	if len(memUtilization) > 0 {
		minMem, maxMem, avgMem := calculateStats(memUtilization)
		event.Reservation.MinMemPercent = minMem
		event.Reservation.MaxMemPercent = maxMem
		event.Reservation.AvgMemPercent = avgMem
	}

	if len(storageUtilization) > 0 {
		minStorage, maxStorage, avgStorage := calculateStats(storageUtilization)
		event.Reservation.MinStoragePercent = minStorage
		event.Reservation.MaxStoragePercent = maxStorage
		event.Reservation.AvgStoragePercent = avgStorage
	}

	p.events = append(p.events, *event)
	p.logger.Debugf("Created and stored final event for node %s with workloads: %v", nodeName, workloads)
}

func calculateStats(data []int) (min, max, avg int) {
	if len(data) == 0 {
		return 0, 0, 0
	}

	min = data[0]
	max = data[0]
	sum := 0

	for _, val := range data {
		sum += val
		if val < min {
			min = val
		}
		if val > max {
			max = val
		}
	}

	avg = sum / len(data)
	return min, max, avg
}
