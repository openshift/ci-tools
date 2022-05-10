package main

import (
	"context"
	"encoding/json"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	clientretry "k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/util/taints"
	taintutils "k8s.io/kubernetes/pkg/util/taints"
	"math"
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
	SchedulerTaintName = "ci-workloads"
)

var (
	nodesInformer  cache.SharedIndexInformer
	podsInformer           cache.SharedIndexInformer
	sacrificeMu            sync.Mutex
	SchedulerTaint               = corev1.Taint{
		Key:    SchedulerTaintName,
		Effect: corev1.TaintEffectPreferNoSchedule,
	}
)


type Prioritization struct {
	context context.Context
	k8sClientSet *kubernetes.Clientset
}


const IndexPodsByNode = "IndexPodsByNode"
const IndexNodesByCiWorkload = "IndexNodesByCiWorkload"

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

	nodesInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			// Called on resource update and every resyncPeriod on existing resources.
			UpdateFunc: p.nodeUpdated,
		},
	)

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

var UpdateTaintBackoff = wait.Backoff{
	Steps:    5,
	Duration: 100 * time.Millisecond,
	Jitter:   1.0,
}

// PatchNodeTaints patches node's taints.
func (p* Prioritization)  PatchNodeTaints(nodeName string, oldNode *corev1.Node, newNode *corev1.Node) error {
	oldData, err := json.Marshal(oldNode)
	if err != nil {
		return fmt.Errorf("failed to marshal old node %#v for node %q: %v", oldNode, nodeName, err)
	}

	newTaints := newNode.Spec.Taints
	newNodeClone := oldNode.DeepCopy()
	newNodeClone.Spec.Taints = newTaints
	newData, err := json.Marshal(newNodeClone)
	if err != nil {
		return fmt.Errorf("failed to marshal new node %#v for node %q: %v", newNodeClone, nodeName, err)
	}

	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, corev1.Node{})
	if err != nil {
		return fmt.Errorf("failed to create patch for node %q: %v", nodeName, err)
	}

	_, err = p.k8sClientSet.CoreV1().Nodes().Patch(context.TODO(), nodeName, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
	return err
}

// AddOrUpdateTaintOnNode add taints to the node. If taint was added into node, it'll issue API calls
// to update nodes; otherwise, no API calls. Return error if any.
func (p* Prioritization) AddOrUpdateTaintOnNode(nodeName string, taints ...*corev1.Taint) error {
	if len(taints) == 0 {
		return nil
	}
	firstTry := true
	return clientretry.RetryOnConflict(UpdateTaintBackoff, func() error {
		var err error
		var oldNode *corev1.Node
		// First we try getting node from the API server cache, as it's cheaper. If it fails
		// we get it from etcd to be sure to have fresh data.
		if firstTry {
			oldNode, err = p.k8sClientSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{ResourceVersion: "0"})
			firstTry = false
		} else {
			oldNode, err = p.k8sClientSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
		}
		if err != nil {
			return err
		}

		var newNode *corev1.Node
		oldNodeCopy := oldNode
		updated := false
		for _, taint := range taints {
			curNewNode, ok, err := taintutils.AddOrUpdateTaint(oldNodeCopy, taint)
			if err != nil {
				return fmt.Errorf("failed to update taint of node")
			}
			updated = updated || ok
			newNode = curNewNode
			oldNodeCopy = curNewNode
		}
		if !updated {
			klog.Errorf("No taint update was actually made: %v", nodeName)
			return nil
		}
		return p.PatchNodeTaints(nodeName, oldNode, newNode)
	})
}

// RemoveTaintOffNode is for cleaning up taints temporarily added to node,
// won't fail if target taint doesn't exist or has been removed.
// If passed a node it'll check if there's anything to be done, if taint is not present it won't issue
// any API calls.
func (p* Prioritization) RemoveTaintOffNode(nodeName string, node *corev1.Node, taints ...*corev1.Taint) error {
	if len(taints) == 0 {
		return nil
	}
	// Short circuit for limiting amount of API calls.
	if node != nil {
		match := false
		for _, taint := range taints {
			if taintutils.TaintExists(node.Spec.Taints, taint) {
				match = true
				break
			}
		}
		if !match {
			return nil
		}
	}

	firstTry := true
	return clientretry.RetryOnConflict(UpdateTaintBackoff, func() error {
		var err error
		var oldNode *corev1.Node
		// First we try getting node from the API server cache, as it's cheaper. If it fails
		// we get it from etcd to be sure to have fresh data.
		if firstTry {
			oldNode, err = p.k8sClientSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{ResourceVersion: "0"})
			firstTry = false
		} else {
			oldNode, err = p.k8sClientSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
		}
		if err != nil {
			return err
		}

		var newNode *corev1.Node
		oldNodeCopy := oldNode
		updated := false
		for _, taint := range taints {
			curNewNode, ok, err := taintutils.RemoveTaint(oldNodeCopy, taint)
			if err != nil {
				return fmt.Errorf("failed to remove taint of node")
			}
			updated = updated || ok
			newNode = curNewNode
			oldNodeCopy = curNewNode
		}
		if !updated {
			return nil
		}
		return p.PatchNodeTaints(nodeName, oldNode, newNode)
	})
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
		node := items[i].(*corev1.Node)
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

func (p* Prioritization) setPreferNoSchedule(ctx context.Context, nodeName string, doTaint bool) error {

	node, err := p.k8sClientSet.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Unable to get node %v to taint it %v: %v", nodeName, doTaint, err)
		return err
	}

	klog.InfoS("Changing taint of node", "nodeName", nodeName, "taint", SchedulerTaintName, "doTaint", doTaint)

	if doTaint {
		err := p.AddOrUpdateTaintOnNode(nodeName, &SchedulerTaint)
		if err != nil {
			klog.Errorf("Unable to apply taint %v to %v: %v", SchedulerTaintName, nodeName, err)
		}
	} else {
		err := p.RemoveTaintOffNode(nodeName, node, &SchedulerTaint)
		if err != nil {
			klog.Errorf("Unable to remove taint %v to %v: %v", SchedulerTaintName, nodeName, err)
		}
	}

	return nil
}

func (p* Prioritization) findHostnamesToSacrifice(podClass PodClass) ([]string, error) {
	workloadNodes, err := p.getWorkloadNodes(podClass)  // find all nodes that are relevant to this workload class

	if err != nil {
		return nil, fmt.Errorf("unable to find workload nodes for %v: %v", podClass, err)
	}

	if len(workloadNodes) <= 1 {
		return nil, nil
	}

	const maxScaleSize = 80 // this must be in agreement with the machineautoscaler configuration
	maxSacrificialNodeCount := int(math.Ceil(float64(len(workloadNodes)) / 10))

	if len(workloadNodes) + maxSacrificialNodeCount >= maxScaleSize {
		// we are approaching the maximum scale of nodes for this podClass.
		// and if everything is busy, the machineautoscaler would not be able
		// to accommodate sacrificial nodes by scaling up. Limit our involvement to
		// 1 node. Also, if len/10 hits zero, make sure we are targeting at least 1.
		maxSacrificialNodeCount = 1
		klog.Warningf("Limiting sacrificial nodes for podClass %v as system is reaching maximum scaling limit", podClass)
	}

	if  maxSacrificialNodeCount == 0 {
		// if len/10 hits zero, make sure we are targeting at least 1.
		maxSacrificialNodeCount = 1
	}

	sacrificeMu.Lock()
	defer sacrificeMu.Unlock()

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
		hostname := p.getNodeHostname(node)
		if len(hostname) == 0 {
			klog.Errorf("Unable to get %v label for node: %v", KubernetesHostnameLabelName, node.Name)
			continue
		}

		podCount := getCachedPodCount(node.Name)

		if podCount < 2 && time.Now().Sub(node.CreationTimestamp.Time) > 15 * time.Minute {
			if !taints.TaintExists(node.Spec.Taints, &SchedulerTaint) {
				klog.Infof("Tainting podClass %v node with PreferredNoSchedule taint (pods=%v) (existing taints: %#v): %v", podClass, podCount, node.Spec.Taints, node.Name)
				p.setPreferNoSchedule(p.context, node.Name, true)
			}
		} else {
			if taints.TaintExists(node.Spec.Taints, &SchedulerTaint) {
				klog.Infof("Removing PreferredNoSchedule taint from podClass %s node (pods=%v): %v", podClass, podCount, node.Name)
				p.setPreferNoSchedule(p.context, node.Name, false)
			}
		}

		sacrificialHostnames = append(sacrificialHostnames, hostname)
		sacrificialOutcomes = append(sacrificialOutcomes, fmt.Sprintf("%v=%v", hostname, podCount))
		if len(sacrificialHostnames) >= maxSacrificialNodeCount {
			break
		}
	}

	for _, node := range workloadNodes {
		if time.Now().Sub(node.CreationTimestamp.Time) < 15 * time.Minute {
			// the cluster has scaled up recently. We have no business trying to
			// scale nodes down.
			klog.Infof("Eliminating sacrificial nodes for podClass %s as system has new node: %v", podClass, node.Name)
			sacrificialHostnames = nil
			break
		}
	}

	klog.Infof("Targeted sacrificial nodes for podClass %v: %v", podClass, sacrificialOutcomes)
	klog.Infof("Actual sacrificial nodes for podClass %v: %v", podClass, sacrificialHostnames)
	return sacrificialHostnames, nil
}
