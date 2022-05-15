package main

import (
	"context"
	"encoding/json"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/util/taints"
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
	PodClassLongTests PodClass = "longtests"
	PodClassProwJobs PodClass = "prowjobs"
	PodClassNone PodClass = ""

	// When a machine is annotated with this and the machineset is scaled down,
	// it will target machines with this annotation to satisfy the change.
	MachineDeleteAnnotationKey         = "machine.openshift.io/cluster-api-delete-machine"
	NodeDisableScaleDownLabelKey      = "cluster-autoscaler.kubernetes.io/scale-down-disabled"
	PodMachineAnnotationKey           = "machine.openshift.io/machine"
	MachineInstanceStateAnnotationKey = "machine.openshift.io/instance-state"
)

var (
	nodesInformer  cache.SharedIndexInformer
	podsInformer           cache.SharedIndexInformer
	machineSetResource = schema.GroupVersionResource{Group: "machine.openshift.io", Version: "v1beta1", Resource: "machinesets"}
	machineResource = schema.GroupVersionResource{Group: "machine.openshift.io", Version: "v1beta1", Resource: "machines"}
	scaleDownMutex sync.Mutex
)


type Prioritization struct {
	context context.Context
	k8sClientSet *kubernetes.Clientset
	dynamicClient dynamic.Interface
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
		add[i] = fmt.Sprintf("%v=%v", addP[i].Key, addP[i].Effect)
	}
	for i := range removeP {
		remove[i] = fmt.Sprintf("%v=%v", removeP[i].Key, removeP[i].Effect)
	}
	current := make([]string, len(newNode.Spec.Taints))
	for i := range newNode.Spec.Taints {
		taint := newNode.Spec.Taints[i]
		current[i] = fmt.Sprintf("%v=%v", taint.Key, taint.Effect)
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

func (p* Prioritization) isPodActive(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodPending || pod.Status.Phase == corev1.PodRunning
}


func (p* Prioritization) getPodsUsingNode(nodeName string) ([]*corev1.Pod, error) {
	items, err := podsInformer.GetIndexer().ByIndex(IndexPodsByNode, nodeName)
	if err != nil {
		return nil, err
	}

	pods := make([]*corev1.Pod, 0)
	for i := range items {
		pod := items[i].(*corev1.Pod)
		if p.isPodActive(pod) {
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

func (p* Prioritization) runNodeAvoidance(podClass PodClass) ([]*corev1.Node, error) {
	scaleDownMutex.Lock()
	defer scaleDownMutex.Unlock()
	workloadNodes, err := p.getWorkloadNodes(podClass)  // find all nodes that are relevant to this workload class

	if err != nil {
		return nil, fmt.Errorf("unable to find workload nodes for %v: %v", podClass, err)
	}

	if len(workloadNodes) <= 1 {
		return nil, nil
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

	avoidanceNodes := make([]*corev1.Node, 0)

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

	precludeNodes := make([]*corev1.Node, 0)

	for _, node := range workloadNodes {

		for _, taint := range node.Spec.Taints {
			// If we find a workload node that has out NoSchedule taint, we need might be able to delete it.
			if taint.Key == CiWorkloadAvoidanceTaintName && taint.Effect == corev1.TaintEffectNoSchedule {

				abortScaleDown := func(err error) {
					// We can't leave nodes in NoSchedule with a custom taint. This confuses the autoscalers
					// simulated fit calculations.
					klog.Errorf("Aborting scale down of node %v due to: %v", node.Name, err)
					_ = p.setNodeAvoidanceState(node, CiAvoidanceStatePreferNoSchedule) // might was well use it for something
				}

				// podCount := getCachedPodCount(node.Name)
				podCount := 0

				// The node was tainted before we arrived and there a no pods on currently. We can trigger a scale down!
				queriedPods, err := p.k8sClientSet.CoreV1().Pods(metav1.NamespaceAll).List(p.context, metav1.ListOptions{
					FieldSelector:        fmt.Sprintf("spec.nodeName=%v", node.Name),
				})

				if err !=  nil {
					abortScaleDown(fmt.Errorf("unable to determine real-time pods for node: %#v", err))
					continue
				}

				for _, queriedPod := range queriedPods.Items {
					_, ok := queriedPod.Labels[CiWorkloadLabelName] // only count CI workload pods
					if ok && p.isPodActive(&queriedPod) {
						podCount++
					}
				}

				if podCount != 0 {
					abortScaleDown(fmt.Errorf("found non zero real-time pod count: %v", podCount))
					continue
				}

				klog.Warningf("Preparing for scale down of tainted node with %v=%v and pods=%v: %v", taint.Key, taint.Effect, podCount, node.Name)
				err = p.scaleDown(node)
				if err != nil {
					abortScaleDown(fmt.Errorf("unable to scale down node %v: %v", node.Name, err))
					continue
				}
			}
		}

		maxTargets := int(math.Ceil(float64(len(workloadNodes)) / 4)) // find appox 25% of nodes

		if time.Now().Sub(node.CreationTimestamp.Time) > 15 * time.Minute {
			if len(precludeNodes) == 0 {
				// this is the most likely node to be scaled down next.
				// don't let pods schedule to it.
				precludeNodes = append(precludeNodes, node)
			}

			if p.getNodeAvoidanceState(node) == CiAvoidanceStateNoSchedule && getCachedPodCount(node.Name) != 0 {
				// If the webhook is shutdown after we label, but before the taint is applied, there
				// will be no "update" to the node to trigger the taint application when we start back up.
				// fall back to PreferNoSchedule.
				err := p.setNodeAvoidanceState(node, CiAvoidanceStatePreferNoSchedule)
				if err != nil {
					klog.Errorf("Unable to turn on PreferNoSchedule avoidance for node %v: %#v", node.Name, err)
				}
			}

			if len(avoidanceNodes) >= maxTargets {
				err := p.setNodeAvoidanceState(node, CiAvoidanceStateOff)
				if err != nil {
					klog.Errorf("Unable to turn off avoidance for node %v: %#v", node.Name, err)
				}
			} else {
				avoidanceNodes = append(avoidanceNodes, node)
				if getCachedPodCount(node.Name) == 0 {
					err := p.setNodeAvoidanceState(node, CiAvoidanceStateNoSchedule)
					if err != nil {
						klog.Errorf("Unable to turn on NoSchedule avoidance for node %v: %#v", node.Name, err)
					}
					precludeNodes = append(precludeNodes, node)
				} else {
					err := p.setNodeAvoidanceState(node, CiAvoidanceStatePreferNoSchedule)
					if err != nil {
						klog.Errorf("Unable to turn on PreferNoSchedule avoidance for node %v: %#v", node.Name, err)
					}
				}
			}
		} else {
			klog.Infof("Ignoring node %v for podClass %v since it is too young", node.Name, podClass)
		}
	}

	avoidanceInfo := make([]string, 0)
	for _, node := range avoidanceNodes {
		avoidanceInfo = append(avoidanceInfo, fmt.Sprintf("%v:%v", node.Name, getCachedPodCount(node.Name)))
	}

	precludeInfo := make([]string, 0)
	for _, node := range precludeNodes {
		precludeInfo = append(precludeInfo, fmt.Sprintf("%v:%v", node.Name, getCachedPodCount(node.Name)))
	}

	klog.Infof("Avoidance info for podClass %v ; precludes: %v ; avoiding: %v", podClass, precludeInfo, avoidanceInfo)
	return precludeNodes, nil
}

type patchStringValue struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string   `json:"value"`
}

// scaleDown - caller must hold scaleDownMutex
func (p* Prioritization) scaleDown(node *corev1.Node) error {
	if _, ok := node.Labels[CiWorkloadLabelName]; !ok {
		return fmt.Errorf("will not scale down non-ci-workload node")
	}

	machineKey, ok := node.Annotations[PodMachineAnnotationKey]
	if !ok {
		return fmt.Errorf("could not find machine annotation associated with node: %v", node.Name)
	}
	components := strings.Split(machineKey, "/")

	machinesetClient := p.dynamicClient.Resource(machineSetResource).Namespace(components[0])
	machineClient := p.dynamicClient.Resource(machineResource).Namespace(components[0])

	machineName := components[1]
	machineObj, err := machineClient.Get(p.context, machineName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("unable to get machine for scale down node %v / machine %v: %#v", node.Name, machineName, err)
	}

	machineAnnotations, found, err := unstructured.NestedMap(machineObj.UnstructuredContent(), "metadata", "annotations")
	if err != nil {
		return fmt.Errorf("could not get machine annotations node %v / machine %v: %#v", node.Name, machineName, err)
	}

	if !found {
		return fmt.Errorf("unable to find machine annotations node %v / machine %v: %#v", node.Name, machineName, err)
	}

	instanceState, ok := machineAnnotations[MachineInstanceStateAnnotationKey]

	if !ok || instanceState.(string) != "running" {
		return fmt.Errorf("unable to scale down machine which is not in the running state node %v / machine %v", node.Name, machineName)
	}

	_, ok = machineAnnotations[MachineDeleteAnnotationKey]
	if ok {
		// This is an important check as it will prevent us from trying to scale down a machineset
		// multiple times for the same machine.
		klog.Infof("will not attempt to scale down machine - it is already annotated for deletion: %v", machineName)
		return nil
	}

	if node.Spec.Unschedulable == true {
		klog.Infof("will not attempt to scale down machine - it is Unschedulable; node %v / machine %v", node.Name, machineName)
		return nil
	}

	machineMetadata, found, err := unstructured.NestedMap(machineObj.UnstructuredContent(), "metadata")
	if err != nil {
		return fmt.Errorf("could not get machine metadata for node %v / machine %v: %#v", node.Name, machineName, err)
	}

	machineOwnerReferencesInterface, ok := machineMetadata["ownerReferences"]
	if !ok {
		return fmt.Errorf("could not find machineset ownerReferences associated with machine: %v node: %v", machineName, node.Name)
	}

	machineOwnerReferences := machineOwnerReferencesInterface.([]interface{})

	var machineSetName string
	for _, ownerInterface := range machineOwnerReferences {
		owner := ownerInterface.(map[string]interface{})
		ownerKind := owner["kind"].(string)
		if ownerKind == "MachineSet" {
			machineSetName = owner["name"].(string)
		}
	}

	if len(machineSetName) == 0 {
		return fmt.Errorf("unable to find machineset name in machine owner references: %v node: %v", machineName, node.Name)
	}

	ms, err := machinesetClient.Get(p.context, machineSetName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("unable to get machineset %v: %#v", machineSetName, err)
	}

	replicas, found, err := unstructured.NestedInt64(ms.UnstructuredContent(), "spec", "replicas")
	if err != nil || !found{
		return fmt.Errorf("unable to get current replicas in machineset %v: %#v", machineSetName, err)
	}

	replicas--
	if replicas < 1 {
		return fmt.Errorf("refusing to fully scale down machineset %v: %#v", machineSetName, err)
	}

	klog.Infof("About to scale down machineset %v to %v replicas", machineSetName, replicas)

	setDeletionAnnotation := func(op string) error {
		deletionAnnotationPatch := []interface{}{
			map[string]interface{}{
				"op":    op,
				"path":  "/metadata/annotations/" + strings.ReplaceAll(MachineDeleteAnnotationKey, "/", "~1"),
				"value": "true",
			},
		}

		deletionPayload, err := json.Marshal(deletionAnnotationPatch)
		if err != nil {
			return fmt.Errorf("unable to marshal machine annotation deletion patch: %#v", err)
		}

		_, err = machineClient.Patch(p.context, machineName, types.JSONPatchType, deletionPayload, metav1.PatchOptions{})
		if err != nil {
			return fmt.Errorf("error patching machine annotations: %v", err)
		}

		return nil
	}

	err = setDeletionAnnotation("add")
	if err != nil {
		return fmt.Errorf("error setting deletion annotation for machine: %#v", err)
	}

	scaleDownPatch := []interface{}{
		map[string]interface{}{
			"op":    "replace",
			"path":  "/spec/replicas",
			"value": replicas,
		},
	}

	scaleDownPayload, err := json.Marshal(scaleDownPatch)
	if err != nil {
		_ = setDeletionAnnotation("remove") // try to remove the annotation we added
		return fmt.Errorf("unable to marshal machineset scale down patch: %#v", err)
	}

	_, err = machinesetClient.Patch(p.context, machineSetName, types.JSONPatchType, scaleDownPayload, metav1.PatchOptions{})
	if err != nil {
		_ = setDeletionAnnotation("remove") // try to remove the annotation we added
		return fmt.Errorf("error patching machineset: %v", err)
	}

	klog.Infof("Triggered scale down machineset %v to %v replicas with target node: %v", machineSetName, replicas, node.Name)
	return nil
}

func (p* Prioritization) getNodeAvoidanceState(node *corev1.Node) string {
	if val, ok := node.Labels[CiWorkloadAvoidanceLabelName]; ok {
		return val
	}
	return CiAvoidanceStateOff
}

func (p* Prioritization) setNodeAvoidanceState(node *corev1.Node, newState string) error {
	currentState := p.getNodeAvoidanceState(node)
	if currentState == newState {
		// already set, no need to update
		return nil
	}

	payload := []patchStringValue{{
		Op:    "add",
		Path:  "/metadata/labels/" + CiWorkloadAvoidanceLabelName,
		Value: newState,
	}}

	payloadBytes, _ := json.Marshal(payload)
	_, err := p.k8sClientSet.CoreV1().Nodes().Patch(p.context, node.Name, types.JSONPatchType, payloadBytes, metav1.PatchOptions{})
	if err == nil {
		klog.Infof("Avoidance label state changed (old state %v) to %v for node: %v", currentState, newState, node.Name)
	} else {
		klog.Errorf("Failed to change avoidance label (old state %v) to %v for node %v: %#v", currentState, newState, node.Name, err)
	}
	return err
}


func (p* Prioritization) findHostnamesToPreclude(podClass PodClass) ([]string, error) {
	workloadNodes, err := p.getWorkloadNodes(podClass)  // find all nodes that are relevant to this workload class

	if err != nil {
		return nil, fmt.Errorf("unable to find workload nodes for %v: %v", podClass, err)
	}

	if len(workloadNodes) <= 1 {
		return nil, nil
	}

	hostnamesToPreclude := make([]string, 0)

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
		return nil, fmt.Errorf("unable to select nodes to preclude: %#v", err)
	}

	precludeNode := func(node *corev1.Node) {
		hostname := p.getNodeHostname(node)
		if len(hostname) == 0 {
			klog.Errorf("Unable to get %v label for node: %v", KubernetesHostnameLabelName, node.Name)
			return
		}
		if val, ok := node.Labels[CiWorkloadLabelName]; !ok || val != string(podClass) {
			return
		}
		hostnamesToPreclude = append(hostnamesToPreclude, hostname)
	}

	autoscalerTaintedNodes := toNodes(v)

	// Do not allow pods to be scheduled to nodes tainted by the autoscaler.
	// Help keep them idle if we can to encourage scale down.
	for _, node := range autoscalerTaintedNodes {
		precludeNode(node)
	}

	nodesToPreclude, err := p.runNodeAvoidance(podClass)
	if err != nil {
		klog.Warningf("Error during node avoidance process: %#v", err)
	} else {
		for _, nodeToPreclude := range nodesToPreclude {
			precludeNode(nodeToPreclude)
		}
	}

	klog.Infof("Precluding hostnames for podClass %v: %v", podClass, hostnamesToPreclude)
	return hostnamesToPreclude, nil
}
