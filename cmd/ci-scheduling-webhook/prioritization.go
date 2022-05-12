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
)


type Prioritization struct {
	context context.Context
	k8sClientSet *kubernetes.Clientset
}


const IndexPodsByNode = "IndexPodsByNode"
const IndexNodesByCiWorkload = "IndexNodesByCiWorkload"
const IndexNodesByTaint = "IndexNodesByTaint"

func (p *Prioritization) nodeUpdated(old, new interface{}) {
	oldNode := old.(*corev1.Node)
	newNode := new.(*corev1.Node)
	addP, removeP := taints.TaintSetDiff(newNode.Spec.Taints, oldNode.Spec.Taints)
	add := make([]string, len(addP))
	remove := make([]string, len(removeP))
	for i := range addP {
		add[i] = addP[i].Key
	}
	for i := range removeP {
		remove[i] = removeP[i].Key
	}
	current := make([]string, len(newNode.Spec.Taints))
	for i := range newNode.Spec.Taints {
		current[i] = newNode.Spec.Taints[i].Key
	}
	if len(add) > 0 || len(remove) > 0 {
		klog.Infof(
			"Node taints updated. %v adding(%#v); removing(%#v):  %#v",
			newNode.Name, add, remove, current,
		)
	}
}

func (p* Prioritization) initializePrioritization() error {

	informerFactory := informers.NewSharedInformerFactory(p.k8sClientSet, 0)
	nodesInformer = informerFactory.Core().V1().Nodes().Informer()

	nodesInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			// Called on resource update and every resyncPeriod on existing resources.
			UpdateFunc: p.nodeUpdated,
		},
	)

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

	err = nodesInformer.AddIndexers(map[string]cache.IndexFunc{
		IndexNodesByTaint: func(obj interface{}) ([]string, error) {
			node := obj.(*corev1.Node)
			taintKeys := make([]string, len(node.Spec.Taints))
			for i := range node.Spec.Taints {
				taintKeys[i] = node.Spec.Taints[i].Key
			}
			return taintKeys, nil
		},
	})

	if err != nil {
		return fmt.Errorf("unable to create new node informer index by taint: %v", err)
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

func (p* Prioritization) isNodeReady(node *corev1.Node) bool {
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
func (p* Prioritization) getWorkloadNodes(podClass PodClass) ([]*corev1.Node, error) {
	items, err := nodesInformer.GetIndexer().ByIndex(IndexNodesByCiWorkload, string(podClass))
	if err != nil {
		return nil, err
	}
	nodes := make([]*corev1.Node, 0)
	for i := range items {
		nodeByIndex := items[i].(*corev1.Node)
		nodeObj, exists, err := nodesInformer.GetIndexer().GetByKey(nodeByIndex.Name)

		if err != nil {
			klog.Errorf("Error trying to find node object %v: %v", nodeByIndex.Name, err)
			continue
		}

		if !exists {
			klog.Warningf("Node no longer exists: %v", nodeByIndex.Name)
			// If the node is cordoned or otherwise unavailable, don't
			// include it. We should only return viable nodes for new workloads.
			continue
		}

		node := nodeObj.(*corev1.Node)
		if !p.isNodeReady(node) {
			// If the node is cordoned or otherwise unavailable, don't
			// include it. We should only return viable nodes for new workloads.
			continue
		}

		nodes = append(nodes, node)
	}
	return nodes, nil
}

func (p* Prioritization) getPodsUsingNode(nodeName string) ([]*corev1.Pod, error) {
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

func (p* Prioritization) getNodeHostname(node *corev1.Node) string {
	val, ok := node.Labels[KubernetesHostnameLabelName]
	if ok {
		return val
	} else {
		return ""
	}
}

func (p* Prioritization) findHostnamesToSacrifice(podClass PodClass) ([]string, []string, error) {
	workloadNodes, err := p.getWorkloadNodes(podClass)  // find all nodes that are relevant to this workload class

	if err != nil {
		return nil, nil, fmt.Errorf("unable to find workload nodes for %v: %v", podClass, err)
	}

	if len(workloadNodes) <= 1 {
		return nil, nil, nil
	}

	sacrificialHostnames := make([]string, 0)

	toNodes := func(nodeObjs []interface{}) []*corev1.Node {
		nodes := make([]*corev1.Node, len(nodeObjs))
		for i, obj := range nodeObjs {
			node := obj.(*corev1.Node)
			nodes[i] = node
		}
		return nodes
	}

	v, err := nodesInformer.GetIndexer().ByIndex(IndexNodesByTaint, "DeletionCandidateOfClusterAutoscaler")
	if err != nil {
		return nil, nil, fmt.Errorf("unable to select sacrificial nodes: %#v", err)
	}

	nodes := toNodes(v)

	for _, node := range nodes {
		hostname := p.getNodeHostname(node)
		if len(hostname) == 0 {
			klog.Errorf("Unable to get %v label for node: %v", KubernetesHostnameLabelName, node.Name)
			continue
		}

		if val, ok := node.Labels[CiWorkloadLabelName]; !ok || val != string(podClass) {
			continue
		}

		sacrificialHostnames = append(sacrificialHostnames, hostname)
	}

	cachedPodCount := make(map[string]int) // maps node name to running pod count

	getCachedPodCount := func(nodeName string) int {
		if val, ok := cachedPodCount[nodeName]; ok {
			return val
		}

		pods, err := p.getPodsUsingNode(nodeName)
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

	// Sort first by podCount then by oldest. The goal is to always be psuedo-draining the node
	// with the fewest pods which is at least 15 minutes old. Sorting by oldest helps make this
	// search deterministic -- we want to report the same node consistently unless there is a node
	// with fewer pods.
	sort.Slice(workloadNodes, func(i, j int) bool {
		nodeI := workloadNodes[i]
		podsI := getCachedPodCount(nodeI.Name)
		nodeJ := workloadNodes[j]
		podsJ := getCachedPodCount(nodeJ.Name)
		if podsI < podsJ {
			return true
		} else if podsI == podsJ {
			return workloadNodes[i].CreationTimestamp.Time.Before(workloadNodes[j].CreationTimestamp.Time)
		} else {
			return false
		}
	})

	for _, node := range workloadNodes {
		hostname := p.getNodeHostname(node)
		if len(hostname) == 0 {
			klog.Errorf("Unable to get %v label for node: %v", KubernetesHostnameLabelName, node.Name)
			continue
		}
		if instanceType, ok := node.Labels["node.kubernetes.io/instance-type"]; ok {
			if instanceType != "m5.4xlarge" { // temporary hack to not antagonize experimental types
				continue
			}
		}
		if time.Now().Sub(node.CreationTimestamp.Time) > 15 * time.Minute {
			klog.Infof("Antagonising for podClass=%v pods=%v : %v", podClass, getCachedPodCount(node.Name), hostname)
			sacrificialHostnames = append(sacrificialHostnames, hostname)
			break
		}
	}

	sort.Strings(sacrificialHostnames)

	// Now we want to gently avoid the oldest 25% of nodes with anti-affinity. This should help
	// accelerate the autoscaler finding nodes when the cluster is high in capacity relative to
	// the workloads.
	sort.Slice(workloadNodes, func(i, j int) bool {
		return workloadNodes[i].CreationTimestamp.Time.Before(workloadNodes[j].CreationTimestamp.Time)
	})

	avoidanceHostnames := make([]string, 0)
	for _, avoidanceNode := range workloadNodes[:len(workloadNodes) / 4] {
		hostname := p.getNodeHostname(avoidanceNode)
		if len(hostname) == 0 {
			klog.Errorf("Unable to get %v label for node: %v", KubernetesHostnameLabelName, avoidanceNode.Name)
			continue
		}
		if instanceType, ok := avoidanceNode.Labels["node.kubernetes.io/instance-type"]; ok {
			if instanceType != "m5.4xlarge" { // temporary hack to not antagonize experimental types
				continue
			}
		}
		avoidanceHostnames = append(avoidanceHostnames, hostname)
	}

	klog.Infof("Sacrificial nodes for podClass %v: %v", podClass, sacrificialHostnames)
	klog.Infof("Avoidance nodes for podClass %v: %v", podClass, avoidanceHostnames)
	return sacrificialHostnames, avoidanceHostnames, nil
}
