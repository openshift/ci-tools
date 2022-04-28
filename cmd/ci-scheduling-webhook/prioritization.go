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
	"sync"
	"time"
)

var (
	nodeMetricsMap sync.Map // map[nodeName string]*NodeMetrics
	podCpuRequests sync.Map // map[ns_slash_podName string]in64
	nodesInformer  cache.SharedIndexInformer
	podsInformer   cache.SharedIndexInformer
)

const IndexPodsByNode = "IndexPodsByNode"
const IndexNodesByCiWorkload = "IndexNodesByCiWorkload"

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
		//  2. calculated pod requests for pods that are no longer present
		for range time.Tick(time.Second * 60) {
			nodeMetricsMap.Range(func(key, value interface{}) bool {
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
			podCpuRequests.Range(func(key, value interface{}) bool {
				qualifiedPodName := key.(string)
				obj, ok, err := podsInformer.GetIndexer().GetByKey(qualifiedPodName)
				if err != nil {
					klog.Errorf("Error querying pod indexer: %v", err)
					return true
				}
				if ok {
					// Pod was found. But is it running?
					pod := obj.(*corev1.Pod)
					if pod.Status.Phase == corev1.PodPending || pod.Status.Phase == corev1.PodRunning {
						// Still running. Don't remove its metrics from our cache.
						return true
					}
				}
				// Pod has not been found or is no longer running.
				klog.InfoS("Removing pod request cache for absent pod", "pod", qualifiedPodName)
				podCpuRequests.Delete(key)
				return true
			})
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
func getWorkloadNodes(podClass string) ([]*corev1.Node, error) {
	items, err := nodesInformer.GetIndexer().ByIndex(IndexNodesByCiWorkload, podClass)
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

func getPodsUsingNode(nodeName string) ([]*corev1.Pod, error) {
	items, err := podsInformer.GetIndexer().ByIndex(IndexPodsByNode, nodeName)
	if err != nil {
		return nil, err
	}
	pods := make([]*corev1.Pod, 0)
	for i := range items {
		pod := items[i].(*corev1.Pod)

		if pod.Status.Phase == corev1.PodPending || pod.Status.Phase == corev1.PodRunning {
			// Count only pods which are consuming resources
			pods = append(pods, pod)
		}
	}
	return pods, nil
}

func computePodCPUMillisRequest(pod *corev1.Pod, useCache bool) int64 {
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

	podCpuRequests.Store(qualifiedPodName, podRequest)
	return podRequest
}

// filterWorkloadNodes will return a new slice after removing nodes which
// heavily utilized.
func filterWorkloadNodes(workloadNodes []*corev1.Node, pod *corev1.Pod) ([]*corev1.Node, []*corev1.Node, error) {
	// Don't use cache for incoming pod. It's possible it was recreated under the same name.
	incomingPodCpuMillis := computePodCPUMillisRequest(pod, false)
	filteredList := make([]*corev1.Node, 0)
	execludedList := make([]*corev1.Node, 0)

	for _, node := range workloadNodes {
		nodeName := node.Name
		exclusionReasons := make([]string, 0)

		logKeyPairs := make([]interface{}, 0)
		addLogKeyPair := func(key string, val string) {
			logKeyPairs = append(logKeyPairs, key)
			logKeyPairs = append(logKeyPairs, val)
		}

		addLogKeyPair("incomingPod", fmt.Sprintf("-n %v pod/%v", pod.Namespace, pod.Name))
		addLogKeyPair("assessingNode", nodeName)

		addExclusionReason := func(reason string) {
			exclusionReasons = append(exclusionReasons, reason)
		}

		m, metricsFound := nodeMetricsMap.Load(nodeName)
		if metricsFound {
			nodeMetrics := m.(*metricsv1b1.NodeMetrics)
			millisInUse := nodeMetrics.Usage.Cpu().MilliValue() // this is a measure metric of CPU usage (not requests/limits)
			measuredCpuUse := 100 * millisInUse / node.Status.Capacity.Cpu().MilliValue()
			addLogKeyPair("metricNodeCpu", fmt.Sprintf("%v%%", measuredCpuUse))

			millisPlusPod := millisInUse + incomingPodCpuMillis

			// We must be careful here, because a single big pod could drive us over the desired
			// target cpu utilization. If it will, but the pod will have the majority of the CPU,
			// we should let it through.
			podWouldDrivePercent := 100 * incomingPodCpuMillis / millisPlusPod

			predictedCpuUse := 100 * millisPlusPod / node.Status.Capacity.Cpu().MilliValue()
			addLogKeyPair("predictedNodeCpu", fmt.Sprintf("%v%%", predictedCpuUse))
			addLogKeyPair("predictedPodCpuOwnership", fmt.Sprintf("%v%%", podWouldDrivePercent))

			if predictedCpuUse > 60 && podWouldDrivePercent < 80 {
				// This node would be heavily utilized AND this pod would not be
				// the majority of the cause.
				addExclusionReason("Predicted CPU utilization is too high and incoming Pod would not own the majority")
			}
		} else {
			addLogKeyPair("nodeMetrics", "UNAVAILABLE")
		}

		scheduledPods, err := getPodsUsingNode(nodeName)
		if err != nil {
			return nil, nil, err
		}

		var scheduledPodsRequestedMillis int64
		for _, pod := range scheduledPods {
			scheduledPodsRequestedMillis += computePodCPUMillisRequest(pod, true)
		}

		scheduledPodsAllocatablePercent := 100 * scheduledPodsRequestedMillis / node.Status.Capacity.Cpu().MilliValue()
		addLogKeyPair("scheduledCpuRequests", fmt.Sprintf("%v%%", scheduledPodsAllocatablePercent))

		scheduledMillisPlusPod := scheduledPodsRequestedMillis + incomingPodCpuMillis
		// what percentage of the use could this pod drive? See comment about big pods above.
		podWouldDriveRequestedPercent := 100 * incomingPodCpuMillis / scheduledMillisPlusPod
		addLogKeyPair("predictedPodCpuRequestsOwnership", fmt.Sprintf("%v%%", podWouldDriveRequestedPercent))

		percentCpuRequested := 100 * scheduledMillisPlusPod / node.Status.Capacity.Cpu().MilliValue()
		addLogKeyPair("predictedCpuRequests", fmt.Sprintf("%v%%", percentCpuRequested))

		if percentCpuRequested > 90 && podWouldDriveRequestedPercent < 90 {
			// We want requested high, but ideally not approach 100% because of
			// https://github.com/kubernetes/kubernetes/issues/106884#issuecomment-1005074672 .
			// When this issue is fixed (looks like with kube 1.23), we can ignore
			// pod requests / limits entirely & let the scheduler make the right decision.
			// Until then, this approach should reduce, but not eliminate the potential for OutOfCpu
			// being reported by the kubelet.
			addExclusionReason("Predicted CPU requests are too high and incoming Pod would not own the majority")
		}

		if len(exclusionReasons) == 0 {
			filteredList = append(filteredList, node)
			addLogKeyPair("affinity", "true")
			klog.InfoS("Node included in pod affinity", logKeyPairs...)
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

func getNodeNamesInPreferredOrder(podClass string, pod *corev1.Pod) ([]string, []*corev1.Node, error) {
	possibleNodes, err := getWorkloadNodes(podClass)
	if err != nil {
		return nil, nil, fmt.Errorf("error finding workload nodes: %v", err)
	}

	filteredNodes, excludedNodes, err := filterWorkloadNodes(possibleNodes, pod)
	if err != nil {
		return nil, nil, fmt.Errorf("error filtering workload nodes: %v", err)
	}

	preferredOrderNodeNames := make([]string, len(filteredNodes))
	for i := range filteredNodes {
		preferredOrderNodeNames[i] = filteredNodes[i].Name
	}

	sort.Strings(preferredOrderNodeNames)
	return preferredOrderNodeNames, excludedNodes, nil
}
