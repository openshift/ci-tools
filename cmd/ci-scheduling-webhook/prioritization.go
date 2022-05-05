package main

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/util/taints"
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
	nodesInformer  cache.SharedIndexInformer
	podsInformer           cache.SharedIndexInformer
	sacrificeMu            sync.Mutex
)

const IndexPodsByNode = "IndexPodsByNode"
const IndexNodesByCiWorkload = "IndexNodesByCiWorkload"

func initializePrioritization(_ context.Context, k8sClientSet *kubernetes.Clientset) error {

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

func getNodeHostname(node *corev1.Node) string {
	val, ok := node.Labels[KubernetesHostnameLabelName]
	if ok {
		return val
	} else {
		return ""
	}
}

func findHostnamesToSacrifice(podClass PodClass) ([]string, error) {
	workloadNodes, err := getWorkloadNodes(podClass)  // find all nodes that are relevant to this workload class

	if err != nil {
		return nil, fmt.Errorf("unable to find workload nodes for %v: %v", podClass, err)
	}

	if len(workloadNodes) <= 1 {
		return nil, nil
	}

	const maxScaleSize = 80 // this must be in agreement with the machineautoscaler configuration
	maxSacrificialNodeCount := len(workloadNodes) / 10

	if len(workloadNodes) + maxSacrificialNodeCount >= maxScaleSize {
		// we are approaching the maximum scale of nodes for this podClass.
		// and if everything is busy, the machineautoscaler would not be able
		// to accommodate sacrificial nodes by scaling up. Limit our involvement to
		// 1 node. Also, if len/10 hits zero, make sure we are targeting at least 1.
		maxSacrificialNodeCount = 1
		klog.Warning("Limiting sacrificial nodes as system is reaching maximum scaling limit")
	}

	if  maxSacrificialNodeCount == 0 {
		// if len/10 hits zero, make sure we are targeting at least 1.
		maxSacrificialNodeCount = 1
	}

	for _, node := range workloadNodes {
		if time.Now().Sub(node.CreationTimestamp.Time) < 15 * time.Minute {
			// the cluster has scaled up recently. We have no business trying to
			// scale nodes down.
			klog.Infof("Limiting sacrificial nodes as system has new node: %v", node.Name)
			return nil, nil
		}
	}

	sacrificeMu.Lock()
	defer sacrificeMu.Unlock()

	cachedPodCount := make(map[string]int) // maps node name to running pod count

	getCachedPodCount := func(nodeName string) int {
		if val, ok := cachedPodCount[nodeName]; ok {
			return val
		}

		pods, err := getPodsUsingNode(nodeName)
		if err != nil {
			klog.Errorf("Unable to get pod count for node: %v: %v", nodeName, err)
			return 255
		}

		classedPodCount := 0  // we only care about pods relevant to CI (i.e. ignore daemonsets)
		for _, pod := range pods {
			if _, ok := pod.Labels[CiWorkloadLabelName]; ok {
				classedPodCount++
			}
		}

		cachedPodCount[nodeName] = classedPodCount
		return classedPodCount
	}

	sortNodeSliceByPodCountThenName := func(s *[]*corev1.Node, lowToHigh bool) {
		ns := *s
		sort.Slice(ns, func(i, j int) bool {
			pi := getCachedPodCount(ns[i].Name)
			pj := getCachedPodCount(ns[j].Name)
			if pi == pj {
				// For nodes with the same pod count, make sure we return in a deterministic order
				return strings.Compare(ns[i].Name, ns[j].Name) < 0
			}
			val :=  pi < pj
			if lowToHigh {
				return val
			} else {
				return val
			}
		})
	}

	sortNodeSliceByPodCountThenName(&workloadNodes, true)
	sacrificialHostnames := make([]string, 0)
	sacrificialOutcomes := make([]string, 0)

	for _, node := range workloadNodes {
		hostname := getNodeHostname(node)
		if len(hostname) == 0 {
			klog.Errorf("Unable to get %v label for node: %v", KubernetesHostnameLabelName, node.Name)
			continue
		}
		sacrificialHostnames = append(sacrificialHostnames, hostname)
		sacrificialOutcomes = append(sacrificialOutcomes, fmt.Sprintf("%v=%v", hostname, getCachedPodCount(node.Name)))
		if len(sacrificialHostnames) >= maxSacrificialNodeCount {
			break
		}
	}


	klog.Infof("Current sacrificial nodes for podClass %v: %v", podClass, sacrificialOutcomes)
	return sacrificialHostnames, nil
}
