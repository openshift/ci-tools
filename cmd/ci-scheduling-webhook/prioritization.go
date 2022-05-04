package main

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	metricsv1b1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metrics "k8s.io/metrics/pkg/client/clientset/versioned"
	"sort"
	"strings"
	"sync"
	"time"
)

type PodClass string

const (
	PodClassBuilds PodClass = "builds"
	PodClassTests PodClass = "tests"
	PodClassNone PodClass = ""
)

var (
	nodeMetricsMap sync.Map // map[nodeName string]*NodeMetrics
	podCpuRequests sync.Map // map[ns_slash_podName string]in64
	assumedPodAssignment sync.Map // map[nodeName String]*map[time.Time]*Pod
	nodesInformer  cache.SharedIndexInformer
	podsInformer   cache.SharedIndexInformer
)

const IndexPodsByNode = "IndexPodsByNode"
const IndexNodesByCiWorkload = "IndexNodesByCiWorkload"

func getPodFromInformer(qualifiedPodName string) (*corev1.Pod, error) {
	obj, ok, err := podsInformer.GetIndexer().GetByKey(qualifiedPodName)
	if err != nil {
		return nil, err
	}
	if ok {
		pod := obj.(*corev1.Pod)
		return pod, nil
	} else {
		return nil, nil
	}
}

func pruneScheduledAssumedPods(nodeName string, excludePod *corev1.Pod) (remainingAssumedPods []*corev1.Pod) {
	remainingAssumedPods = make([]*corev1.Pod, 0)
	if _, ok, err := nodesInformer.GetIndexer().GetByKey(nodeName); !ok || err != nil {
		if err != nil {
			klog.Errorf("Error querying node indexer: %v", err)
			return
		}
		assumedPodAssignment.Delete(nodeName) // the node is gone, we don't need assumed pods for it
		return
	} else {
		value, ok := assumedPodAssignment.Load(nodeName)
		if !ok {
			return
		}
		assumedPods := value.(*sync.Map)
		assumedPods.Range(func(key, value interface{}) bool {
			unixNanosCreation := key.(time.Time)
			pod := value.(*corev1.Pod)

			if excludePod != nil && excludePod.Namespace == pod.Namespace && excludePod.Name == pod.Name {
				return true
			}

			age := time.Now().Sub(unixNanosCreation)
			if age > 20 * time.Minute {
				klog.Warningf("Pod %v has languished without being scheduled -- removing from assumed")
				// Why is this pod not getting scheduled. Who knows. Avoid memory leak.
				assumedPods.Delete(key)
				return true
			}

			qualifiedPodName := pod.Namespace + "/" + pod.Name
			currentPodCopy, err := getPodFromInformer(qualifiedPodName)
			if err != nil {
				klog.Errorf("Error querying pod indexer: %v", err)
				return true
			}

			if currentPodCopy != nil && currentPodCopy.Spec.NodeName != "" {
				if age > 1 * time.Minute {
					// Don't remove the pod from assumed for 1 minute. This will give the pod a chance to start
					// consuming actual CPU, measured by node metrics. Until then, we may be double counting the
					// pod's requests. That's fine.
					// Yes, this all to help reduce OutOfCpu from
					// https://github.com/kubernetes/kubernetes/issues/106884#issuecomment-1005074672 .
					klog.Infof("Pod %v/%v [%v] has been scheduled to %v -- removing from assumed for %v", currentPodCopy.Namespace, currentPodCopy.Name, key, currentPodCopy.Spec.NodeName, nodeName)
					// the pod has been scheduled, so remove it from assumed
					assumedPods.Delete(key)
					return true
				}
			}
			// Otherwise, the pod has not been scheduled and we keep in it assumed for now.
			remainingAssumedPods = append(remainingAssumedPods, pod)
			return true
		})
		return
	}
}

// pruneScheduledAssumedPod removes a pod from assumed scheduling when a pod is scheduled
func pruneScheduledAssumedPod(pod *corev1.Pod) {
	nodeName := pod.Spec.NodeName
	if len(nodeName) == 0 {
		return
	}
	if assumedNodeName, ok := pod.Labels[CiWorkloadNodeAssumed]; ok {
		pruneScheduledAssumedPods(assumedNodeName, nil)
	}
}

func initializePrioritization(ctx context.Context, k8sClientSet *kubernetes.Clientset, metricsClientSet *metrics.Clientset) error {

	informerFactory := informers.NewSharedInformerFactory(k8sClientSet, 0)
	nodesInformer = informerFactory.Core().V1().Nodes().Informer()

	err := nodesInformer.AddIndexers(map[string]cache.IndexFunc{
		IndexNodesByCiWorkload: func(obj interface{}) ([]string, error) {
			node := obj.(*corev1.Node)
			workloads := []string{""}
			if workload, ok := node.Labels[CiWorkloadLabelName]; ok {
				workloads = []string{workload}
			}
			return workloads, nil
		},
	})

	if err != nil {
		return fmt.Errorf("unable to create new node informer index: %v", err)
	}

	podsInformer = informerFactory.Core().V1().Pods().Informer()

	// Index pods by the nodes they are assigned to
	err = podsInformer.AddIndexers(map[string]cache.IndexFunc{
		IndexPodsByNode: func(obj interface{}) ([]string, error) {
			nodeNames := []string{obj.(*corev1.Pod).Spec.NodeName}
			return nodeNames, nil
		},
	})

	if err != nil {
		return fmt.Errorf("unable to create new pod informer index: %v", err)
	}

	podsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			pruneScheduledAssumedPod(pod)
		},
		UpdateFunc: func(obj interface{}, newObj interface{}) {
			pod := newObj.(*corev1.Pod)
			pruneScheduledAssumedPod(pod)
		},
	})

	stopCh := make(chan struct{})
	informerFactory.Start(stopCh) // runs in background
	informerFactory.WaitForCacheSync(stopCh)

	initialMetrics, err := metricsClientSet.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("unable to list mertics: %v", err)
	}

	for _, nodeMetrics := range initialMetrics.Items {
		nodeName := nodeMetrics.Name
		nodeMetricsMap.Store(nodeName, &nodeMetrics)
	}

	go func() {
		// periodically query the measured load on nodes (same data as "oc adm top node")
		// and store it for reference when determining scheduling priority
		for range time.Tick(time.Second * 3) {
			metricsList, err := metricsClientSet.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
			if err != nil {
				klog.Warningf("Error retrieving node metrics: %v", err)
				continue
			}
			for i := range metricsList.Items {
				nodeMetrics := &metricsList.Items[i]
				nodeMetricsMap.Store(nodeMetrics.Name, nodeMetrics)
			}
		}
	}()

	go func() {
		// periodically clean up:
		//  1. metrics for nodes that are no longer around
		//  2. calculated pod requests for pods that are no longer present / running
		//  3. Assumed pod assignments
		for range time.Tick(time.Second * 60) {

			nodeMetricsCount := 0
			nodeMetricsMap.Range(func(key, value interface{}) bool {
				nodeMetricsCount++
				nodeName := key.(string)
				if _, ok, err := nodesInformer.GetIndexer().GetByKey(nodeName); !ok || err != nil {
					if err != nil {
						klog.Errorf("Error querying node indexer: %v", err)
						return true
					}
					nodeMetricsMap.Delete(nodeName)
					klog.InfoS("Removing node metrics for absent node", "nodeName", nodeName)
				}
				return true
			})

			cachedPodRequests := 0
			podCpuRequests.Range(func(key, value interface{}) bool {
				cachedPodRequests++
				qualifiedPodName := key.(string)
				pod, err := getPodFromInformer(qualifiedPodName)
				if err != nil {
					klog.Errorf("Error querying pod indexer: %v", err)
					podCpuRequests.Delete(key) // avoid a memory leak in this undefined error state
					return true
				}
				if pod != nil {
					// Pod was found. But is it running?
					if pod.Status.Phase == corev1.PodPending || pod.Status.Phase == corev1.PodRunning {
						// Still running. Don't remove its metrics from our cache.
						return true
					}
				}
				// Pod has not been found or is no longer running.
				klog.InfoS("Removing pod request cache for defunct pod", "pod", qualifiedPodName)
				podCpuRequests.Delete(key)
				return true
			})

			assumedPodsCount := 0
			assumedPodAssignment.Range(func(key, value interface{}) bool {
				nodeName := key.(string)
				unprunedPods := pruneScheduledAssumedPods(nodeName, nil)
				assumedPodsCount += len(unprunedPods)
				return true
			})

			// None of these should grow disproportionally to the number of pods / nodes in the system.
			klog.InfoS("Caching metrics", "assumedPods", assumedPodsCount, "cachedPodRequests", cachedPodRequests, "cachedNodeMetrics", nodeMetricsCount)
		}
	}()

	return nil
}

func isNodeReady(node *corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			if condition.Status == corev1.ConditionTrue {
				return true
			}
		}
	}
	return false
}

// getWorkloadNodes returns all nodes presently available which support a given
// podClass (workload type).
func getWorkloadNodes(podClass PodClass) ([]*corev1.Node, error) {
	items, err := nodesInformer.GetIndexer().ByIndex(IndexNodesByCiWorkload, string(podClass))
	if err != nil {
		return nil, err
	}
	nodes := make([]*corev1.Node, 0)
	for i := range items {
		node := items[i].(*corev1.Node)
		if !isNodeReady(node) {
			// If the node is cordoned or otherwise unavailable, don't
			// include it. We should only return viable nodes for new workloads.
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func getPodsUsingNode(nodeName string, excludePod *corev1.Pod) ([]*corev1.Pod, []*corev1.Pod, error) {
	items, err := podsInformer.GetIndexer().ByIndex(IndexPodsByNode, nodeName)
	if err != nil {
		return nil, nil, err
	}
	pods := make([]*corev1.Pod, 0)
	for i := range items {
		pod := items[i].(*corev1.Pod)
		if excludePod != nil && excludePod.Namespace == pod.Namespace && excludePod.Name == pod.Name {
			continue
		}
		if pod.Status.Phase == corev1.PodPending || pod.Status.Phase == corev1.PodRunning {
			// Count only pods which are consuming resources
			pods = append(pods, pod)
		}
	}

	// If we have potentially recently informed scheduling of a pod and it may not
	// have yet been official scheduled on the node, then it will be in the assumed
	// pod map for this node.
	// Return the pods we find as if they are actually scheduled.
	assumedPods := pruneScheduledAssumedPods(nodeName, excludePod)
	return pods, assumedPods, nil
}

func calculatePodCPUMillisRequest(pod *corev1.Pod, useCache bool) int64 {
	if pod.Name == "" || pod.Namespace == "" {
		// Incoming pods may not specify their names and expect the server to generate one.
		// Avoid using cached calculations for such pods.
		useCache = false
	}
	qualifiedPodName := pod.Namespace + "/" + pod.Name
	if useCache {
		if val, ok := podCpuRequests.Load(qualifiedPodName); ok {
			// we've calculated this pod before
			return val.(int64)
		}
	}

	var podRequest int64

	findContainerCpuMillis := func(container *corev1.Container) int64 {
		val := container.Resources.Requests.Cpu().MilliValue()
		if val < 100 {
			val = 100 // minimum used by kube
		}
		return val
	}

	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		podRequest += findContainerCpuMillis(container)
	}

	for i := range pod.Spec.InitContainers {
		initContainer := &pod.Spec.InitContainers[i]
		value := findContainerCpuMillis(initContainer)
		if podRequest < value {
			podRequest = value // An initContainer requiring more memory than containers will be the actual request
		}
	}

	// If Overhead is being utilized, add to the total requests for the pod
	if pod.Spec.Overhead != nil {
		if quantity, found := pod.Spec.Overhead[corev1.ResourceCPU]; found {
			podRequest += quantity.MilliValue()
		}
	}

	if useCache {
		podCpuRequests.Store(qualifiedPodName, podRequest)
	}
	return podRequest
}

// filterWorkloadNodes will return a new slice after removing nodes which
// heavily utilized.
func filterWorkloadNodes(workloadNodes []*corev1.Node, pod *corev1.Pod) ([]*corev1.Node, []*corev1.Node, error) {
	// Don't use cache for incoming pod. It's possible it was recreated under the same name
	// or that we are being reinvoked as part of our reinvocation policy.
	incomingPodCpuMillis := calculatePodCPUMillisRequest(pod, false)
	filteredList := make([]*corev1.Node, 0)
	execludedList := make([]*corev1.Node, 0)

	// Sort in an order of node name to encourage assignment in a deterministic
	// node order.
	sort.Slice(workloadNodes, func(i, j int) bool {
		return strings.Compare(workloadNodes[i].Name, workloadNodes[j].Name) < 0
	})

	const filteredNodeCountTarget = 5  // how many nodes should we return?

	for i, node := range workloadNodes {
		nodeName := node.Name
		exclusionReasons := make([]string, 0)

		logKeyPairs := make([]interface{}, 0)
		addLogKeyPair := func(key string, val string) {
			logKeyPairs = append(logKeyPairs, key)
			logKeyPairs = append(logKeyPairs, val)
		}

		// Pass incoming pod because this webhook permits reinvocation.
		// Don't recount this pod as assumed.
		scheduledPods, assumedPods, err := getPodsUsingNode(nodeName, pod)
		if err != nil {
			return nil, nil, err
		}

		var scheduledPodsRequestedMillis int64
		var assumedPodsRequestedMillis int64
		for _, scheduledPod := range scheduledPods {
			scheduledPodsRequestedMillis += calculatePodCPUMillisRequest(scheduledPod, true)
		}

		assumedPodNames := make([]string, len(assumedPods))
		for i, assumedPod := range assumedPods {
			assumedPodsRequestedMillis += calculatePodCPUMillisRequest(assumedPod, true)
			assumedPodNames[i] = assumedPod.Namespace + "/" + assumedPod.Name
		}

		// For the purposes of our calculations, we calculate assumed pods as if they were scheduled.
		scheduledPlusAssumedPodsRequestedMillis := scheduledPodsRequestedMillis + assumedPodsRequestedMillis

		addLogKeyPair("incomingPod", fmt.Sprintf("-n %v pod/%v", pod.Namespace, pod.Name))
		addLogKeyPair("incomingPodRequest", fmt.Sprintf("cpu=%v", incomingPodCpuMillis))

		addLogKeyPair("assessingNode", nodeName)
		addLogKeyPair("assumedPodsMillis", fmt.Sprintf("%v", assumedPodsRequestedMillis))
		addLogKeyPair("assumedPodNames", fmt.Sprintf("%#q", assumedPodNames))

		addExclusionReason := func(reason string) {
			exclusionReasons = append(exclusionReasons, reason)
		}

		m, metricsFound := nodeMetricsMap.Load(nodeName)
		if metricsFound {
			nodeMetrics := m.(*metricsv1b1.NodeMetrics)
			millisInUse := nodeMetrics.Usage.Cpu().MilliValue() // this is a measure metric of CPU usage (not requests/limits)
			measuredCpuUse := 100 * millisInUse / node.Status.Capacity.Cpu().MilliValue()
			addLogKeyPair("cpuUse:measured", fmt.Sprintf("%vm / %v%%", millisInUse, measuredCpuUse))

			millisInUse += assumedPodsRequestedMillis // assume these pods will consume their requested CPU
			assumedCpuUse := 100 * millisInUse / node.Status.Capacity.Cpu().MilliValue()
			addLogKeyPair("cpuUse:measured+assumedUse", fmt.Sprintf("%vm / %v%%", millisInUse, assumedCpuUse))

			millisInUsePlusPod := millisInUse + incomingPodCpuMillis

			// We must be careful here, because a single big pod could drive us over the desired
			// target cpu utilization. If it will, but the pod will have the majority of the CPU,
			// we should let it through.
			podWouldDrivePercent := 100 * incomingPodCpuMillis / millisInUsePlusPod

			predictedCpuUse := 100 * millisInUsePlusPod / node.Status.Capacity.Cpu().MilliValue()
			addLogKeyPair("cpuUse:measured+assumed+incoming", fmt.Sprintf("%vm / %v%%", millisInUsePlusPod, predictedCpuUse))
			addLogKeyPair("cpuUse:incomingPodShare", fmt.Sprintf("%v%%", podWouldDrivePercent))

			if predictedCpuUse > 60 && podWouldDrivePercent < 80 {
				// This node would be heavily utilized AND this pod would not be
				// the majority of the cause.
				addExclusionReason("Predicted CPU utilization is too high and incoming Pod would not own the majority")
			}
		} else {
			addLogKeyPair("nodeMetrics", "UNAVAILABLE")
		}

		scheduledPodsCapacityPercent := 100 * scheduledPodsRequestedMillis / node.Status.Capacity.Cpu().MilliValue()
		addLogKeyPair("cpuRequests:current", fmt.Sprintf("%vm / %v%%", scheduledPodsRequestedMillis, scheduledPodsCapacityPercent))

		scheduledPodsPlusAssumedCapacityPercent := 100 * scheduledPlusAssumedPodsRequestedMillis / node.Status.Capacity.Cpu().MilliValue()
		addLogKeyPair("cpuRequests:current+assumed", fmt.Sprintf("%vm / %v%%", scheduledPlusAssumedPodsRequestedMillis, scheduledPodsPlusAssumedCapacityPercent))

		predictedMillisPlusPod := scheduledPlusAssumedPodsRequestedMillis + incomingPodCpuMillis
		predictedMillisCapacityPercent := 100 * predictedMillisPlusPod / node.Status.Capacity.Cpu().MilliValue()
		addLogKeyPair("cpuRequests:current+assumed+incoming", fmt.Sprintf("%vm / %v%%", predictedMillisPlusPod, predictedMillisCapacityPercent))

		podWouldDriveRequestedPercent := 100 * incomingPodCpuMillis / predictedMillisPlusPod
		// what percentage of the use could this pod drive? See comment about big pods above.
		addLogKeyPair("cpuRequests:incomingPodShare", fmt.Sprintf("%v%%", podWouldDriveRequestedPercent))

		if predictedMillisCapacityPercent > 90 && podWouldDriveRequestedPercent < 90 {
			// We want requested high, but ideally not approach 100% because of
			// https://github.com/kubernetes/kubernetes/issues/106884#issuecomment-1005074672 .
			// When this issue is fixed (looks like with kube 1.23), we can ignore
			// pod requests / limits entirely & let the scheduler make the right decision.
			// Until then, this approach should reduce, but not eliminate the potential for OutOfCpu
			// being reported by the kubelet.
			addExclusionReason("Predicted CPU requests are too high and incoming Pod would not own the majority")
		}

		if len(scheduledPods) + len(assumedPods) > 130 {
			addExclusionReason("Too many pods have been scheduled to node")
		}

		if len(exclusionReasons) == 0 {
			filteredList = append(filteredList, node)
			addLogKeyPair("affinity", "true")
			klog.InfoS("Node included in pod affinity", logKeyPairs...)
			if len(filteredList) == filteredNodeCountTarget {
				// nodes excluded only because we have a candidate. They may or may not have capacity.
				quickExclude := workloadNodes[i+1:]
				// After we find the required viable node, ignore the rest
				execludedList = append(execludedList, quickExclude...)
				return filteredList, execludedList, nil
			}
		} else {
			execludedList = append(execludedList, node)
			exclusionMsg := fmt.Sprintf("%#q", exclusionReasons)
			addLogKeyPair("affinity", "false")
			addLogKeyPair("exclusionReasons", exclusionMsg)
			klog.InfoS("Node excluded from pod affinity", logKeyPairs...)
		}
	}
	return filteredList, execludedList, nil
}

func getNodeNamesInPreferredOrder(podClass PodClass, pod *corev1.Pod) ([]*corev1.Node, []*corev1.Node, error) {
	possibleNodes, err := getWorkloadNodes(podClass)
	if err != nil {
		return nil, nil, fmt.Errorf("error finding workload nodes: %v", err)
	}

	filteredNodes, excludedNodes, err := filterWorkloadNodes(possibleNodes, pod)
	if err != nil {
		return nil, nil, fmt.Errorf("error filtering workload nodes: %v", err)
	}

	if len(filteredNodes) > 0 {

		// filtered already come back sorted, but just in case.
		sort.Slice(filteredNodes, func(i, j int) bool {
			return strings.Compare(filteredNodes[i].Name, filteredNodes[j].Name) < 0
		})

		// the assumed pods concept is another part of the workaround
		// for https://github.com/kubernetes/kubernetes/issues/106884#issuecomment-1005074672 .
		// If we get a big rush of pods going into our hook, but the scheduler
		// takes awhile to schedule them, our cpu request calculation in the prioritization
		// calculation will be off. To reduce the risk of this, assume that the pod will
		// be successfully scheduled on the first node. Use this when consider the requests
		// budget.
		firstPreference := filteredNodes[0].Name
		assumedPodsMap := &sync.Map{}
		testPodMap, ok := assumedPodAssignment.Load(firstPreference)
		if ok {
			assumedPodsMap = testPodMap.(*sync.Map)
		}

		assumedSchedulingTime := time.Now() // approximately when do we think this pod would be scheduled
		// This webhook permits reinvocation (reinvocationPolicy: "IfNeeded").
		// Before adding this assumed pod, make sure we haven't already added it.
		// If we have, update the record with this updated Pod (requests may have changed).
		assumedPodsMap.Range(func(key interface{}, value interface{}) bool {
			existingAssumedPod := value.(*corev1.Pod)
			if existingAssumedPod.Namespace == pod.Namespace && existingAssumedPod.Name == pod.Name {
				assumedSchedulingTime = key.(time.Time)
				return false
			}
			return true
		})

		assumedPodsMap.Store(assumedSchedulingTime, pod)
		assumedPodAssignment.Store(firstPreference, assumedPodsMap)
	}

	return filteredNodes, excludedNodes, nil
}
