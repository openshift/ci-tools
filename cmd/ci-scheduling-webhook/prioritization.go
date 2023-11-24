package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/util/taints"
)

type PodClass string

const (
	PodClassBuilds    PodClass = "builds"
	PodClassTests     PodClass = "tests"
	PodClassLongTests PodClass = "longtests"
	PodClassProwJobs  PodClass = "prowjobs"
	PodClassNone      PodClass = ""

	// MachineDeleteAnnotationKey When a machine is annotated with this and the machineset is scaled down,
	// it will target machines with this annotation to satisfy the change.
	OldMachineDeleteAnnotationKey = "machine.openshift.io/cluster-api-delete-machine"
	MachineDeleteAnnotationKey    = "machine.openshift.io/delete-machine"

	// NodeDisableScaleDownAnnotationKey makes the autoscaler ignore a node for scale down consideration.
	NodeDisableScaleDownAnnotationKey = "cluster-autoscaler.kubernetes.io/scale-down-disabled"

	// NodeMachineAnnotationKey Value is the machine name associated with this node
	NodeMachineAnnotationKey = "machine.openshift.io/machine"

	// CiSchedulingKeepNodeAnnotationKey is an annotation with "true" / "false" value which
	// can be used by humans to prevent specific nodes from being scaled down (or being avoided).
	CiSchedulingKeepNodeAnnotationKey = "ci-scheduling.ci.openshift.io/keep-node"
	// Also available as a label applicable by machineset
	CiSchedulingKeepNodeLabelKey = "ci-scheduling.ci.openshift.io/keep-node"

	// NodeMachineConfigurationStateAnnotationKey is an annotation machine the machine config
	// controller describing whether the machine is being updated or node.
	NodeMachineConfigurationStateAnnotationKey = "machineconfiguration.openshift.io/state"

	CiMachineSetClassLabelKey = "ci-machineset-class"
)

var (
	nodesInformer      cache.SharedIndexInformer
	podsInformer       cache.SharedIndexInformer
	machineSetResource = schema.GroupVersionResource{Group: "machine.openshift.io", Version: "v1beta1", Resource: "machinesets"}
	machineResource    = schema.GroupVersionResource{Group: "machine.openshift.io", Version: "v1beta1", Resource: "machines"}

	// If a node name exists in this map, scale down operations are being attempted for it.
	scalingDownNodesByClass = map[PodClass]*sync.Map{
		PodClassBuilds:    {},
		PodClassTests:     {},
		PodClassLongTests: {},
		PodClassProwJobs:  {},
	}
	scalingDownAddLock sync.Mutex

	// Locks used to make sure access to machineset and other races are prevented for scale down operations.
	nodeClassScaleDownLock = map[PodClass]*sync.Mutex{
		PodClassBuilds:    {},
		PodClassTests:     {},
		PodClassLongTests: {},
		PodClassProwJobs:  {},
	}

	nodeAvoidanceLock sync.Mutex
)

type Prioritization struct {
	context       context.Context
	k8sClientSet  *kubernetes.Clientset
	dynamicClient dynamic.Interface
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

func (p *Prioritization) initializePrioritization() error {

	informerFactory := informers.NewSharedInformerFactory(p.k8sClientSet, 0)
	nodesInformer = informerFactory.Core().V1().Nodes().Informer()

	_, err := nodesInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			// Called on resource update and every resyncPeriod on existing resources.
			UpdateFunc: p.nodeUpdated,
		},
	)

	if err != nil {
		return fmt.Errorf("unable to create new node informer: %w", err)
	}

	err = nodesInformer.AddIndexers(map[string]cache.IndexFunc{
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
		return fmt.Errorf("unable to create new node informer index: %w", err)
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
		return fmt.Errorf("unable to create new pod informer index: %w", err)
	}

	err = podsInformer.AddIndexers(map[string]cache.IndexFunc{
		IndexNodesByCiWorkload: func(obj interface{}) ([]string, error) {
			pod := obj.(*corev1.Pod)
			ciWorkloadClasses := make([]string, 0) // this should be
			if pod.Labels != nil {
				if workloadClass, ok := pod.Labels[CiWorkloadLabelName]; ok {
					ciWorkloadClasses = append(ciWorkloadClasses, workloadClass)
				}
			} else {
				ciWorkloadClasses = append(ciWorkloadClasses, fmt.Sprintf("%v", PodClassNone))
			}
			return ciWorkloadClasses, nil
		},
	})

	if err != nil {
		return fmt.Errorf("unable to create new pod informer index: %w", err)
	}

	stopCh := make(chan struct{})
	informerFactory.Start(stopCh) // runs in background
	informerFactory.WaitForCacheSync(stopCh)

	for podClass := range nodeClassScaleDownLock {
		// Setup a timer which will help scale down nodes supporting this pod class
		go p.pollNodeClassForScaleDown(podClass)
	}

	// go p.encourageSpotInstances()

	return nil
}

func (p *Prioritization) encourageSpotInstances() { //nolint: unused

	onDemandBuildsMachineSetSelector := CiMachineSetClassLabelKey + "=builds"
	interruptibleBuildsMachineSetSelector := CiMachineSetClassLabelKey + "=interruptible-builds"

	machineSetClient := p.dynamicClient.Resource(machineSetResource).Namespace("openshift-machine-api") // connect or reconnect client on error
	lastOnDemandReplicas := int64(-1)
	for range time.Tick(10 * time.Second) {

		onDemandMachineSetList, err := machineSetClient.List(p.context, metav1.ListOptions{LabelSelector: onDemandBuildsMachineSetSelector})
		if err != nil {
			klog.Errorf("Error finding on demand machinesets to assess spot instance usage: %v", err)
			continue
		}

		var currentOnDemandReplicas int64
		for _, onDemandMachineSet := range onDemandMachineSetList.Items {
			msName := onDemandMachineSet.GetName()
			replicas, found, err := unstructured.NestedInt64(onDemandMachineSet.Object, "spec", "replicas")
			if err != nil {
				klog.Errorf("Error finding replicas in on demand machineset %v: %v", msName, err)
				continue
			}
			if !found {
				klog.Errorf("Did not finding replica count in on demand machineset %v", msName)
				continue
			}
			currentOnDemandReplicas += replicas
		}

		minimumInterruptibleReplicas := currentOnDemandReplicas
		interruptiblesToEncourage := int64(0)

		if lastOnDemandReplicas > -1 {
			// If there are more ondemand than when we checked last time, ensure that
			// spot instances are added to match.
			if currentOnDemandReplicas > lastOnDemandReplicas {
				interruptiblesToEncourage += currentOnDemandReplicas - lastOnDemandReplicas
			}
		}
		lastOnDemandReplicas = currentOnDemandReplicas

		interruptibleMachineSetList, err := machineSetClient.List(p.context, metav1.ListOptions{LabelSelector: interruptibleBuildsMachineSetSelector})
		if err != nil {
			klog.Errorf("Error finding interruptible machinesets to assess spot instance usage: %v", err)
			continue
		}

		adjustableInterruptibleMachineSets := make([]unstructured.Unstructured, 0)
		for _, interruptibleMachineSet := range interruptibleMachineSetList.Items {
			msName := interruptibleMachineSet.GetName()
			replicas, found, err := unstructured.NestedInt64(interruptibleMachineSet.Object, "spec", "replicas")
			if err != nil {
				klog.Errorf("Error finding replicas in interruptible machineset %v: %v", msName, err)
				continue
			}
			if !found {
				klog.Errorf("Did not finding replica count in interruptible machineset %v", msName)
				continue
			}
			availableReplicas, found, err := unstructured.NestedInt64(interruptibleMachineSet.Object, "status", "availableReplicas")
			if err != nil {
				klog.Errorf("Error finding available replicas in interruptible machineset %v: %v", msName, err)
				continue
			}
			if !found {
				klog.Errorf("Did not finding available replica count in interruptible machineset %v", msName)
				continue
			}

			if replicas > availableReplicas {
				// We are already requesting spot instances that have not been successfully provisioned.
				klog.Infof("Ignoring interruptible machineset because replica count is greater than available")
				continue
			}

			adjustableInterruptibleMachineSets = append(adjustableInterruptibleMachineSets, interruptibleMachineSet)
		}

		interruptibleMachineSetCount := len(adjustableInterruptibleMachineSets)
		if interruptibleMachineSetCount == 0 {
			klog.Infof("No interruptible machinesets which can be adjusted. Will check again in 5 minutes.")
			time.Sleep(5 * time.Minute)
			continue
		}

		var totalInterruptibleReplicas int64
		interruptibleReplicaCounts := make([]int64, interruptibleMachineSetCount)
		for i, interruptibleMachineSet := range adjustableInterruptibleMachineSets {
			msName := interruptibleMachineSet.GetName()
			replicas, found, err := unstructured.NestedInt64(interruptibleMachineSet.Object, "spec", "replicas")
			if err != nil {
				klog.Errorf("Error finding replicas in interruptible machineset %v: %v", msName, err)
				totalInterruptibleReplicas = -1
				break
			}
			if !found {
				klog.Errorf("Did not finding replica count in interruptible machineset %v", msName)
				totalInterruptibleReplicas = -1
				break
			}
			totalInterruptibleReplicas += replicas
			interruptibleReplicaCounts[i] = replicas
		}

		if totalInterruptibleReplicas < 0 {
			continue
		}

		targetInterruptibleReplicas := totalInterruptibleReplicas + interruptiblesToEncourage
		if minimumInterruptibleReplicas > targetInterruptibleReplicas {
			targetInterruptibleReplicas = minimumInterruptibleReplicas
		}

		interruptiblesToAllocate := targetInterruptibleReplicas - totalInterruptibleReplicas

		if interruptiblesToAllocate == 0 {
			klog.Infof("No additional interruptible instances are desired.")
			continue
		}

		// Randomly allocate the number of new desired interruptibles across interruptible machinesets.
		// We don't want the machinesets balanced necessarily. We want to use interruptible instances
		// wherever we can successfully get them. Random allocation will succeed in locations
		// with available instances and so gravitate in that direction after multiple iterations.
		for interruptiblesToAllocate > 0 {
			interruptibleReplicaCounts[rand.Intn(interruptibleMachineSetCount)]++
			interruptiblesToAllocate--
		}

		nodeClassScaleDownLock[PodClassBuilds].Lock()
		for i, interruptibleMachineSet := range adjustableInterruptibleMachineSets {
			msName := interruptibleMachineSet.GetName()

			if interruptibleReplicaCounts[i] > 50 {
				// Sanity check
				klog.Errorf("Refusing to increase interruptible machineset %v scale beyond 50", msName, err)
				continue
			}

			scaleUpPatch := []interface{}{
				map[string]interface{}{
					"op":    "replace",
					"path":  "/spec/replicas",
					"value": interruptibleReplicaCounts[i],
				},
			}
			scaleUpPayload, err := json.Marshal(scaleUpPatch)
			if err != nil {
				klog.Errorf("unable to marshal interruptible machineset %v scale up patch: %#v", msName, err)
				continue
			}

			_, err = machineSetClient.Patch(p.context, msName, types.JSONPatchType, scaleUpPayload, metav1.PatchOptions{})
			if err != nil {
				klog.Errorf("unable to patch interruptible machineset %v with scale down patch: %#v", msName, err)
				continue
			}

		}
		nodeClassScaleDownLock[PodClassBuilds].Unlock()
	}
}

func (p *Prioritization) pollNodeClassForScaleDown(podClass PodClass) {
	p.evaluateNodeClassScaleDown(podClass) // just for faster debug
	for range time.Tick(time.Minute) {
		p.evaluateNodeClassScaleDown(podClass)
	}
}

func (p *Prioritization) isNodeSchedulable(node *corev1.Node) bool {
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
func (p *Prioritization) getWorkloadNodes(podClass PodClass, schedulableNodesOnly bool, minNodeAge time.Duration) ([]*corev1.Node, error) {
	items, err := nodesInformer.GetIndexer().ByIndex(IndexNodesByCiWorkload, string(podClass))
	if err != nil {
		return nil, err
	}
	nodes := make([]*corev1.Node, 0)
	now := time.Now()
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
		if schedulableNodesOnly && !p.isNodeSchedulable(node) {
			// If the node is cordoned or otherwise unavailable, don't
			// include it. We should only return viable nodes for new workloads.
			continue
		}

		// If machine config is doing anything with the node, ignore it.
		// machineconfig will taint the node and try to drain it. If we uncordon
		// during that time, more pods will be scheduled and redrained leading to
		// ci workload pods being deleted unexpectedly.
		if mcState, ok := node.Annotations[NodeMachineConfigurationStateAnnotationKey]; ok {
			if strings.ToLower(mcState) != "done" {
				klog.Warningf("Node %v does not have valid configuration yet %v is in state %v; waiting for done state", nodeByIndex.Name, NodeMachineConfigurationStateAnnotationKey, mcState)
				continue
			}
		} else {
			klog.Errorf("Unable to find %v annotation for node: %v", NodeMachineConfigurationStateAnnotationKey, nodeByIndex.Name)
			continue
		}

		if node.Annotations != nil {
			if val, ok := node.Annotations[CiSchedulingKeepNodeAnnotationKey]; ok {
				keepNode, _ := strconv.ParseBool(val)
				if keepNode {
					// If the node should be kept, hide it from all calculations about workloads.
					// This prevents it from being scaled down or avoided.
					continue
				}
			}
		}

		if node.Labels != nil {
			if val, ok := node.Labels[CiSchedulingKeepNodeLabelKey]; ok {
				keepNode, _ := strconv.ParseBool(val)
				if keepNode {
					// If the node should be kept, hide it from all calculations about workloads.
					// This prevents it from being scaled down or avoided.
					continue
				}
			}
		}

		if now.Sub(node.CreationTimestamp.Time) < minNodeAge {
			// node does not meet caller's criteria
			continue
		}

		nodes = append(nodes, node)
	}
	return nodes, nil
}

func (p *Prioritization) isPodActive(pod *corev1.Pod, within time.Duration) bool {
	active := pod.Status.Phase == corev1.PodPending || pod.Status.Phase == corev1.PodRunning
	if !active {
		if len(pod.Finalizers) > 0 {
			return true
		}
		// For the sake of timing conditions, like this, allow pods to be considered
		// active within to caller's window:
		// https://github.com/openshift/ci-tools/blob/361bb525d35f7fc5ec8eed87d5014b61a99300fc/pkg/steps/template.go#L577
		// count the pod as active for several minutes after actual termination.
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Terminated == nil {
				return true
			}
			if time.Since(cs.State.Terminated.FinishedAt.Time) < within {
				return true
			}
		}
	}
	return active
}

func (p *Prioritization) getPodsUsingNode(nodeName string, classedPodsOnly bool, activeWithin time.Duration) ([]*corev1.Pod, error) { //nolint: unparam
	items, err := podsInformer.GetIndexer().ByIndex(IndexPodsByNode, nodeName)
	if err != nil {
		return nil, err
	}

	pods := make([]*corev1.Pod, 0)
	for i := range items {
		pod := items[i].(*corev1.Pod)

		if classedPodsOnly {
			if _, ok := pod.Labels[CiWorkloadLabelName]; !ok {
				continue
			}
		}

		if p.isPodActive(pod, activeWithin) {
			// Count only pods which are consuming resources
			pods = append(pods, pod)
		}
	}

	return pods, nil
}

func (p *Prioritization) getNodeHostname(node *corev1.Node) string {
	val, ok := node.Labels[KubernetesHostnameLabelName]
	if ok {
		return val
	} else {
		return ""
	}
}

func (p *Prioritization) evaluateNodeScaleDown(podClass PodClass, node *corev1.Node) {

	// Prevent multiple evaluations on the same node at the same time
	scalingDownAddLock.Lock()
	scalingDownNodes := scalingDownNodesByClass[podClass]
	if _, ok := scalingDownNodes.Load(node.Name); ok {
		// work is ongoing for this node in another thread. Nothing to do.
		scalingDownAddLock.Unlock()
		return
	}
	scalingDownNodes.Store(node.Name, node)
	defer scalingDownNodes.Delete(node.Name)
	scalingDownAddLock.Unlock()

	klog.Infof("Evaluating second stage of scale down for podClass %v node: %v", podClass, node.Name)

	podCount := 0

	// Final live query to see if there are really no pods vs shared informer indexer used for quick checks.
	queriedPods, err := p.k8sClientSet.CoreV1().Pods(metav1.NamespaceAll).List(p.context, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%v", node.Name),
	})

	if err != nil {
		klog.Errorf("Unable to determine real-time pods for node %v: %#v", node.Name, err)
		return // Try again later
	}

	for _, queriedPod := range queriedPods.Items {
		_, ok := queriedPod.Labels[CiWorkloadLabelName] // only count CI workload pods
		if ok && p.isPodActive(&queriedPod, 0) {
			podCount++
		}
	}

	if podCount != 0 {
		klog.Errorf("found non zero real-time pod count %v for %v", podCount, node.Name)
		return // Try again later
	}

	klog.Warningf("Triggering final stage of scale down for podClass %v node: %v", podClass, node.Name)
	machineSetNamespace, machineSetName, machineName, err := p.scaleDown(podClass, node)
	if err != nil {
		// Keep the node cordoned and try again later.
		klog.Errorf("Unable to scale down node %v: %v", node.Name, err)
		return
	}

	// Hold in this method waiting for this node to disappear. This method holds a lock
	// which will prevent the code from trying to scale down this particular node again,
	// but it will allow attempts on other nodes to proceed.
	// There are three ways out of this loop:
	// - 1h timeout  (machineapi controller is wedged?)
	// - Node disappears
	// - Machineset says that it is reconciled AND machine is in the "running" phase

	for i := 0; i < 60; i++ {
		time.Sleep(1 * time.Minute)

		_, exists, err := nodesInformer.GetIndexer().GetByKey(node.Name)
		if err != nil {
			klog.Errorf("Error checking scaled down node %v existence: %v", node.Name, err)
		} else {
			if !exists {
				// Success! This node should no longer show up in the avoidance nodes,
				klog.Infof("Successfully scaled down node: %v", node.Name)
				return
			} else {
				klog.Infof("Check [%v] - node %v still exists after scale down attempt. This is fine if it is in the process of shutting down.", i, node.Name)
			}
		}

		machineSetClient := p.dynamicClient.Resource(machineSetResource).Namespace(machineSetNamespace)
		ms, err := machineSetClient.Get(p.context, machineSetName, metav1.GetOptions{})
		if err != nil {
			klog.Errorf("Error finding machineset %v after scale down attempt for %v existence: %v", machineSetName, node.Name, err)
			continue
		}

		// Check if machineset status.replicas matches spec.replicas. Should be eventually consistent.
		replicas, found, err := unstructured.NestedInt64(ms.UnstructuredContent(), "spec", "replicas")
		if err != nil || !found {
			klog.Errorf("unable to get current replicas in machineset %v: %#v", machineSetName, err)
			continue
		}

		statusReplicas, found, err := unstructured.NestedInt64(ms.UnstructuredContent(), "status", "replicas")
		if err != nil || !found {
			klog.Errorf("unable to get current status.replicas in machineset %v: %#v", machineSetName, err)
			continue
		}

		if replicas != statusReplicas {
			klog.Warningf("existing replicas (%v) != status.replicas (%v) in machineset %v ; still waiting for scale down of %v", replicas, statusReplicas, machineSetName, node.Name)
			continue
		}

		// Check if machineset status.readyReplicas matches spec.replicas. Should be eventually consistent or not present
		// if replicas == 0.
		readyReplicas, found, err := unstructured.NestedInt64(ms.UnstructuredContent(), "status", "readyReplicas")
		if err != nil {
			klog.Errorf("unable to get current status.readyReplicas in machineset %v: %#v", machineSetName, err)
			continue
		}

		machinePhase, _, _, err := p.getMachinePhase(machineSetNamespace, machineName)
		if err != nil {
			klog.Errorf("unable to get machine phase for machine %v / node %v: %v", machineName, node.Name, err)
			continue
		}

		if found && replicas != readyReplicas {
			klog.Warningf("existing replicas (%v) != status.readyReplicas (%v) in machineset %v ; still waiting for scale down of %v", replicas, readyReplicas, machineSetName, node.Name)
			continue
		} else {
			if machinePhase == "running" {
				// If these values match and our machine is "running", the machineset thinks it is reconciled, so we are clear to try again if the nodes
				// is still present.
				klog.Errorf("machineset %v appears reconciled but node %v was not removed -- will try again later", machineSetName, node.Name)
				return
			}
		}

	}

	// Its possible some other node was annotated with the deletion annotation and was chosen by
	// the machine controller to scale down instead of our node. Allow other scale down attempts.
	klog.Errorf("Expected node %v to have disappeared after scale down attempt -- will try again later", node.Name)
}

// evaluateNodeClassScaleDown is called by a single thread, periodically, to see what
// nodes should be updated in order to scale down or to encourage scale down conditions.
func (p *Prioritization) evaluateNodeClassScaleDown(podClass PodClass) {

	// First, check to see if any nodes have been targeted for scale down in this class.
	// Nodes which have been targeted have getNodeAvoidanceState of TaintEffectNoSchedule
	// and they are actually cordoned on the cluster.
	// Make sure the nodes are at least 15 minutes old, or you might catch one that is cordoned
	// during initialization.
	allWorkloadNodes, err := p.getWorkloadNodes(podClass, false, 15*time.Minute)
	if err != nil {
		klog.Errorf("Error finding workload nodes for scale down assessment of podClass %v: %v", podClass, err)
		return
	}

	for _, node := range allWorkloadNodes {

		if _, ok := node.Labels["spot-io"]; ok {
			// This is a spot.io node. It is responsible for scale down, so ignore it.
			continue
		}

		if p.getNodeAvoidanceState(node) == corev1.TaintEffectNoSchedule {
			pods, err := p.getPodsUsingNode(node.Name, true, 0)
			if err != nil {
				klog.Errorf("Unable to check pod count during class scale down eval for node %v: %#v", node.Name, err)
				return
			}

			if len(pods) == 0 {
				// We set NoSchedule in a previous loop and there are still no pods on the
				// node (e.g. a race between our patch and a pod being scheduled might
				// have violated that expectation). Time to try scale it down if the operation
				// is not already underway.
				scalingDownNodes := scalingDownNodesByClass[podClass]
				if _, ok := scalingDownNodes.Load(node.Name); !ok { // avoid spawning a thread if it appears work is in progress for this node already
					go p.evaluateNodeScaleDown(podClass, node)
				}
			} else {
				klog.Warningf("Pods are still running on node targeted for scale down: %v", node.Name)
			}
		}
	}

	nodeNamesUnderActiveScaleDown := make([]string, 0)
	scalingDownNodes := scalingDownNodesByClass[podClass]
	scalingDownNodes.Range(func(key, value interface{}) bool {
		nodeNamesUnderActiveScaleDown = append(nodeNamesUnderActiveScaleDown, fmt.Sprintf("%v", key))
		return true
	})

	if len(nodeNamesUnderActiveScaleDown) > 0 {
		klog.Infof("Active attempts to scale down the following %v nodes are underway: %v", podClass, nodeNamesUnderActiveScaleDown)
	}

	// Now we want to look at nodes that are schedulable / active. Taint / cordon these nodes to help
	// a portion of them become idle and targets for scale down.

	// find all nodes that are relevant to this workload class and at least x minutes old
	workloadNodes, err := p.getWorkloadNodesInAvoidanceOrder(podClass)
	if err != nil {
		klog.Errorf("Error finding avoidance workload nodes for scale down assessment of podClass %v: %v", podClass, err)
		return
	}

	if len(workloadNodes) == 0 {
		// There is nothing to consider scaling down at present
		return
	}

	avoidanceNodes := make([]*corev1.Node, 0)
	maxAvoidanceTargets := int(math.Ceil(float64(len(workloadNodes)) / 4)) // find appox 25% of nodes
	avoidanceInfo := make([]string, 0)

	for _, node := range workloadNodes {

		if len(avoidanceNodes) >= maxAvoidanceTargets {
			// Allow any remaining node to be scheduled if it is beyond our
			// maximum target count.
			err := p.setNodeAvoidanceState(node, podClass, TaintEffectNone)
			if err != nil {
				klog.Errorf("Unable to turn off avoidance for node %v: %#v", node.Name, err)
			}
		} else {
			// Otherwise, we want to encourage pods away from this node.
			pods, err := p.getPodsUsingNode(node.Name, true, 0)
			if err != nil {
				klog.Errorf("Unable to check pod count during class scale down eval for node %v: %#v", node.Name, err)
				continue
			}

			avoidanceNodes = append(avoidanceNodes, node)

			activeAvoidanceEffect := p.getNodeAvoidanceState(node)
			if len(pods) == 0 {
				// This is a ready / schedulable node with no pods. Set it up for scale down on the
				// next call of this method.
				err := p.setNodeAvoidanceState(node, podClass, corev1.TaintEffectNoSchedule)
				if err != nil {
					klog.Errorf("Unable to turn on NoSchedule avoidance for node %v: %#v", node.Name, err)
				} else {
					activeAvoidanceEffect = corev1.TaintEffectNoSchedule
				}
			} else {
				// The node is the in top 25% of nodes close to being able to scale down. Encourage pods
				// not to land on it unless necessary.
				err := p.setNodeAvoidanceState(node, podClass, corev1.TaintEffectPreferNoSchedule)
				if err != nil {
					klog.Errorf("Unable to turn on PreferNoSchedule avoidance for node %v: %#v", node.Name, err)
				} else {
					activeAvoidanceEffect = corev1.TaintEffectPreferNoSchedule
				}
			}

			avoidanceInfo = append(avoidanceInfo, fmt.Sprintf("%v;pods=%v;avoidance=%v", node.Name, len(pods), activeAvoidanceEffect))
		}
	}

	klog.Infof("Avoidance info for podClass %v ; avoiding: %v", podClass, avoidanceInfo)
}

func (p *Prioritization) getWorkloadNodesInAvoidanceOrder(podClass PodClass) ([]*corev1.Node, error) {
	// find all nodes that are relevant to this workload class and have been around at least x minutes.
	workloadNodes, err := p.getWorkloadNodes(podClass, true, 15*time.Minute)

	if err != nil {
		return nil, fmt.Errorf("unable to find workload nodes for %v: %w", podClass, err)
	}

	if len(workloadNodes) <= 1 {
		// Nothing to put in order. There is either 1 or zero nodes to avoid.
		return workloadNodes, nil
	}

	cachedPodCount := make(map[string]int) // maps node name to running pod count
	getCachedPodCount := func(node *corev1.Node) int {
		nodeName := node.Name
		if val, ok := cachedPodCount[nodeName]; ok {
			return val
		}

		// For the purposes of node avoidance, we only want to look at pods that are
		// actively running (activeWithin 0s).
		pods, err := p.getPodsUsingNode(nodeName, true, 0)
		if err != nil {
			klog.Errorf("Unable to get pod count for node: %v: %v", nodeName, err)
			return 255
		}

		classedPodCount := len(pods)

		if spot_io_lifecycle, ok := node.Labels["spotinst.io/node-lifecycle"]; ok && spot_io_lifecycle == "spot" {
			// Ensure spot instances always sort AFTER on demand instances; i.e. favor
			// eliminating the more expensive on demand instance.
			classedPodCount += 100
		}

		cachedPodCount[nodeName] = classedPodCount
		return classedPodCount
	}

	// Sort first by podCount then by oldest. The goal is to always be pseuedo-draining the node
	// with the fewest pods which is at least 15 minutes old. Sorting by oldest helps make this
	// search deterministic -- we want to report the same node consistently unless there is a node
	// with fewer pods.
	sort.Slice(workloadNodes, func(i, j int) bool {
		nodeI := workloadNodes[i]
		podsI := getCachedPodCount(nodeI)
		nodeJ := workloadNodes[j]
		podsJ := getCachedPodCount(nodeJ)
		if podsI < podsJ {
			return true
		} else if podsI == podsJ {
			return workloadNodes[i].CreationTimestamp.Time.Before(workloadNodes[j].CreationTimestamp.Time)
		} else {
			return false
		}
	})

	return workloadNodes, nil
}

func (p *Prioritization) findNodesToPreclude(podClass PodClass) ([]*corev1.Node, error) {
	nodeAvoidanceLock.Lock()
	defer nodeAvoidanceLock.Unlock()

	workloadNodes, err := p.getWorkloadNodesInAvoidanceOrder(podClass)

	if err != nil {
		return nil, fmt.Errorf("unable to get sorted workload nodes for %v: %w", podClass, err)
	}

	if len(workloadNodes) <= 1 {
		// A pod is about to be scheduled, there is no reason to try to avoid nodes
		// if there is only 1 or 0 to consider (there may also be young nodes,
		// but we ignore those for the purposes of avoidance).
		return nil, nil
	}

	precludeNodes := make([]*corev1.Node, 0)

	// this is the most likely node to be scaled down next.
	// don't let pods schedule in order to help our scale
	// down loop eliminate it.
	precludeNodes = append(precludeNodes, workloadNodes[0])

	return precludeNodes, nil
}

func (p *Prioritization) getMachinePhase(machineNamespace string, machineName string) (machinePhase string, machineExists bool, machineObj *unstructured.Unstructured, err error) {
	machineClient := p.dynamicClient.Resource(machineResource).Namespace(machineNamespace)

	machineObj, err = machineClient.Get(p.context, machineName, metav1.GetOptions{})
	if err != nil {
		if kerrors.IsNotFound(err) {
			return "", false, nil, nil
		}
		return "", true, nil, fmt.Errorf("unable to get machine for scale down machine %v: %#w", machineName, err)
	}

	machinePhase, found, err := unstructured.NestedString(machineObj.UnstructuredContent(), "status", "phase")
	if !found || err != nil {
		return "", true, machineObj, fmt.Errorf("could not get machine phase machine %v: %#w", machineName, err)
	}

	machinePhase = strings.ToLower(machinePhase)
	return machinePhase, true, machineObj, nil
}

// scaleDown should be called by only one thread at a time. It assesses a node which has been staged for
// safe scale down (e.g. is running with the NoSchedule taint). Final checks are performed.
func (p *Prioritization) scaleDown(podClass PodClass, node *corev1.Node) (machineSetNamespace string, machineSetName string, machineName string, err error) {
	if _, ok := node.Labels[CiWorkloadLabelName]; !ok {
		// Just a sanity check
		return "", "", "", fmt.Errorf("will not scale down non-ci-workload node")
	}

	machineKey, ok := node.Annotations[NodeMachineAnnotationKey]
	if !ok {
		return "", "", "", fmt.Errorf("could not find machine annotation associated with node: %v", node.Name)
	}
	components := strings.Split(machineKey, "/")
	machineSetNamespace = components[0]
	machineSetClient := p.dynamicClient.Resource(machineSetResource).Namespace(machineSetNamespace)
	machineClient := p.dynamicClient.Resource(machineResource).Namespace(machineSetNamespace)

	machineName = components[1]

	_, machineExists, _, err := p.getMachinePhase(machineSetNamespace, machineName)

	if !machineExists {
		return machineSetNamespace, "", machineName, nil
	}

	if err != nil {
		return machineSetNamespace, "", machineName, fmt.Errorf("error checking machine phase %v / node %v: %w", machineName, node.Name, err)
	}

	for {
		// Wait until terminated pods are X minutes old so that prow / ci-operator have a change to check final status
		// extract logs / etc (they poll).
		pods, err := p.getPodsUsingNode(node.Name, true, 5*time.Minute)
		if err != nil {
			klog.Errorf("Unable to query for pod age requirement. Encountered error: %w", err)
			break
		}
		if len(pods) == 0 {
			break
		}
		klog.Infof("Waiting for all terminated pods on machine %v / node %v to have been so for several minutes' %v remaining", machineName, node.Name, len(pods))
		time.Sleep(1 * time.Minute)
	}

	_, machineExists, machineObj, err := p.getMachinePhase(machineSetNamespace, machineName)

	if !machineExists {
		return machineSetNamespace, "", machineName, nil
	}

	if err != nil {
		return machineSetNamespace, "", machineName, fmt.Errorf("error checking machine phase %v / node %v: %w", machineName, node.Name, err)
	}

	machineMetadata, found, err := unstructured.NestedMap(machineObj.UnstructuredContent(), "metadata")
	if !found || err != nil {
		return machineSetNamespace, "", machineName, fmt.Errorf("could not get machine metadata for node %v / machine %v: %#w", node.Name, machineName, err)
	}

	machineOwnerReferencesInterface, ok := machineMetadata["ownerReferences"]
	if !ok {
		return machineSetNamespace, "", machineName, fmt.Errorf("could not find machineset ownerReferences associated with machine: %v node: %v", machineName, node.Name)
	}

	machineOwnerReferences := machineOwnerReferencesInterface.([]interface{})

	for _, ownerInterface := range machineOwnerReferences {
		owner := ownerInterface.(map[string]interface{})
		ownerKind := owner["kind"].(string)
		if ownerKind == "MachineSet" {
			machineSetName = owner["name"].(string)
		}
	}

	if len(machineSetName) == 0 {
		return machineSetNamespace, "", machineName, fmt.Errorf("unable to find machineset name in machine owner references: %v node: %v", machineName, node.Name)
	}

	_, err = machineSetClient.Get(p.context, machineSetName, metav1.GetOptions{})
	if err != nil {
		return machineSetNamespace, machineSetName, machineName, fmt.Errorf("unable to get machineset %v: %#w", machineSetName, err)
	}

	// setting this Taint is the point of no return -- if successful, we will try to scale down indefinitely.
	// This taint is set to work around a DNS bug where DNS pods need time to gracefully shutdown before a
	// drain operation. Draining without a graceful termination period causes brief outages in DNS.
	// https://issues.redhat.com/browse/OCPBUGS-488 is intended to fix this behavior.
	err = p.setNoExecuteTaint(node.Name, podClass)
	if err != nil {
		return machineSetNamespace, machineSetName, machineName, fmt.Errorf("unable to set NoExecute node %v: %#w", node.Name, err)
	}

	klog.Infof("Sleeping to allow graceful DNS pod termination on %v / %v", machineName, node.Name)
	time.Sleep(40 * time.Second)

	attempt := 0
	for {
		if attempt > 0 {
			time.Sleep(10 * time.Second)
		}

		klog.Infof("Setting machine deletion annotation on machine %v for node %v [attempt=%v]", machineName, node.Name, attempt)
		deletionAnnotationsPatch := []interface{}{
			map[string]interface{}{
				"op":    "add",
				"path":  "/metadata/annotations/" + strings.ReplaceAll(MachineDeleteAnnotationKey, "/", "~1"),
				"value": "true",
			},
			map[string]interface{}{
				"op":    "add",
				"path":  "/metadata/annotations/" + strings.ReplaceAll(OldMachineDeleteAnnotationKey, "/", "~1"),
				"value": "true",
			},
		}

		deletionPayload, err := json.Marshal(deletionAnnotationsPatch)
		if err != nil {
			klog.Errorf("Unable to marshal machine %v annotation deletion patch: %#w", machineName, err)
			continue
		}

		_, err = machineClient.Patch(p.context, machineName, types.JSONPatchType, deletionPayload, metav1.PatchOptions{})
		if err != nil {
			if kerrors.IsNotFound(err) {
				klog.Warningf("Machine %v has disappeared -- canceling scaledown", machineName)
				return machineSetNamespace, machineSetName, machineName, nil
			}
			klog.Errorf("Unable to apply machine %v annotation %v deletion patch: %#w", machineName, MachineDeleteAnnotationKey, err)
			continue
		}

		break
	}

	// We will now interact with the machineset for this pod class. Hold a lock until we successfully
	// get rid of this machine or initiate its deletion.
	nodeClassScaleDownLock[podClass].Lock()
	defer nodeClassScaleDownLock[podClass].Unlock()

	attempt = 0
	for {
		if attempt > 0 {
			time.Sleep(10 * time.Second)
		}

		ms, err := machineSetClient.Get(p.context, machineSetName, metav1.GetOptions{})
		if err != nil {
			if kerrors.IsNotFound(err) {
				klog.Errorf("Machineset %v has disappeared -- canceling scaledown", machineSetName)
				return machineSetNamespace, machineSetName, machineName, nil
			}
			klog.Errorf("Unable to get machineset %v: %#w", machineSetName, err)
			continue
		}

		klog.Infof("Trying to scale down machineset %v in order to eliminate machine %v / node %v [attempt %v]", machineSetName, machineName, node.Name, attempt)
		attempt++

		replicas, found, err := unstructured.NestedInt64(ms.UnstructuredContent(), "spec", "replicas")
		if err != nil || !found {
			klog.Errorf("unable to get current replicas in machineset %v: %#w", machineSetName, err)
			continue
		}

		// When replicas is reduced, the machine should through different phases (deleting / shutting down). Don't
		// interact with machineset while the machine is not in the running state -- a previous scan may have already
		// decremented the replica count. This check should prevent it from happening again while the
		// machine shuts down.

		machinePhase, machineExists, _, err := p.getMachinePhase(machineSetNamespace, machineName)

		if err != nil {
			klog.Errorf("Error trying to determine machine phase %v / node %v: %w", machineName, node.Name, err)
			continue
		}

		if machinePhase == "deleting" {
			// This is treated as a successful scale down
			klog.Infof("Machine is in deleting state %v / node %v", machineName, node.Name)
			return machineSetNamespace, machineSetName, machineName, nil
		}

		if !machineExists {
			// This is also treated as a successful scale down
			klog.Infof("Machine %v no longer exists according to API / node %v", machineName, node.Name)
			return machineSetNamespace, machineSetName, machineName, nil
		}

		if machinePhase != "running" {
			klog.Infof("Waiting until machine phase is running or machine is deleted; machine %v / node %v is in phase %v", machineName, node.Name, machinePhase)
			continue
		}

		// There's no indication that the machine is scaling down. Commit to decrementing the replica count.
		replicas--

		if replicas < 0 {
			// This is unexpected -- something has changed replicas and we don't think it was us.
			klog.Errorf("computed replicas < 0 for machineset %v ; aborting this scale down due to race", machineSetName)
			return machineSetNamespace, machineSetName, machineName, nil
		}

		klog.Infof("Scaling down machineset %v to %v replicas in order to eliminate machine %v / node %v", machineSetName, replicas, machineName, node.Name)

		scaleDownPatch := []interface{}{
			map[string]interface{}{
				"op":    "replace",
				"path":  "/spec/replicas",
				"value": replicas,
			},
		}

		scaleDownPayload, err := json.Marshal(scaleDownPatch)
		if err != nil {
			klog.Errorf("unable to marshal machineset scale down patch: %#w", err)
			continue
		}

		_, err = machineSetClient.Patch(p.context, machineSetName, types.JSONPatchType, scaleDownPayload, metav1.PatchOptions{})
		if err != nil {
			klog.Errorf("unable to patch machineset %v with scale down patch: %#w", machineSetName, err)
			continue
		}

		// The machine was annotated for deletion and a corresponding decrement to the machineset was applied.
		// This method is done. Returning from this method releases a lock which allows other machines in this
		// class to scale down. Waiting too long in this method means that the number of cordoned machines may
		// grow faster than they can be scaled done.
		return machineSetNamespace, machineSetName, machineName, nil
	}
}

const TaintEffectNone corev1.TaintEffect = "None"

func (p *Prioritization) getNodeAvoidanceState(node *corev1.Node) corev1.TaintEffect {
	avoidanceState := TaintEffectNone

	for _, taint := range node.Spec.Taints {
		if taint.Key == CiWorkloadPreferNoScheduleTaintName {
			avoidanceState = corev1.TaintEffectPreferNoSchedule
			break
		}
	}

	if node.Spec.Unschedulable {
		avoidanceState = corev1.TaintEffectNoSchedule
	}

	return avoidanceState
}

func (p *Prioritization) setNodeCordoned(node *corev1.Node, cordoned bool) error {
	if node.Spec.Unschedulable == cordoned {
		// we are already at the desired state
		return nil
	}

	cordonPatch := []interface{}{
		map[string]interface{}{
			"op":    "replace",
			"path":  "/spec/unschedulable",
			"value": cordoned,
		},
	}

	payloadBytes, _ := json.Marshal(cordonPatch)
	_, err := p.k8sClientSet.CoreV1().Nodes().Patch(p.context, node.Name, types.JSONPatchType, payloadBytes, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("failed to change cordoned state for node %v to %v: %#w", node.Name, cordoned, err)
	}

	klog.Infof("Set node %v to cordoned=%v", node.Name, cordoned)
	return nil
}

func (p *Prioritization) setNodeAvoidanceState(node *corev1.Node, podClass PodClass, desiredEffect corev1.TaintEffect) error {
	nodeTaints := node.Spec.Taints
	if nodeTaints == nil {
		nodeTaints = make([]corev1.Taint, 0)
	}

	foundEffect := TaintEffectNone

	if node.Spec.Unschedulable {
		foundEffect = corev1.TaintEffectNoSchedule
	}

	if foundEffect == corev1.TaintEffectNoSchedule {
		// Never uncordon nodes. This gets really complex if someone is manually cordoning nodes.
		// Just avoid the complexity.
		klog.Errorf("Attempt to new avoidance state %v for node %v targeted for scale down", desiredEffect, node.Name)
		return nil
	}

	// We enforce NoSchedule avoidance with cordon. CiWorkloadPreferNoScheduleTaintName
	// will be set to unless desiredEffect == TaintEffectNone
	_ = p.setNodeCordoned(node, desiredEffect == corev1.TaintEffectNoSchedule)

	// PreferNoSchedule is implemented as a custom taint. Depending on
	// caller's request, add or remove that taint.
	foundPreferNoScheduleIndex := -1
	for i, taint := range nodeTaints {
		if taint.Key == CiWorkloadPreferNoScheduleTaintName {
			foundPreferNoScheduleIndex = i
			if !node.Spec.Unschedulable {
				foundEffect = corev1.TaintEffectPreferNoSchedule
			}
		}
	}

	modified := false // whether there is reason to patch the node taints

	if foundPreferNoScheduleIndex == -1 && desiredEffect != TaintEffectNone {
		// Both non-none avoidance levels should set the PreferNoSchedule taint.
		nodeTaints = append(nodeTaints, corev1.Taint{
			Key:    CiWorkloadPreferNoScheduleTaintName,
			Value:  fmt.Sprintf("%v", podClass),
			Effect: corev1.TaintEffectPreferNoSchedule,
		})
		modified = true
	}

	if foundPreferNoScheduleIndex >= 0 && desiredEffect == TaintEffectNone {
		// remove our taint from the list
		nodeTaints = append(nodeTaints[:foundPreferNoScheduleIndex], nodeTaints[foundPreferNoScheduleIndex+1:]...)
		modified = true
	}

	if modified {
		taintMap := map[string][]corev1.Taint{
			"taints": nodeTaints,
		}
		unstructuredTaints, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&taintMap)
		if err != nil {
			return fmt.Errorf("error decoding modified taints to unstructured data: %w", err)
		}

		patch := map[string]interface{}{
			"op":    "add",
			"path":  "/spec/taints",
			"value": unstructuredTaints["taints"],
		}

		patchEntries := make([]map[string]interface{}, 0)
		patchEntries = append(patchEntries, patch)

		payloadBytes, _ := json.Marshal(patchEntries)
		_, err = p.k8sClientSet.CoreV1().Nodes().Patch(p.context, node.Name, types.JSONPatchType, payloadBytes, metav1.PatchOptions{})
		if err != nil {
			return fmt.Errorf("failed to change avoidance taint (existing effect [%v]) to %v for node %v: %#w", foundEffect, desiredEffect, node.Name, err)
		}
	}

	if desiredEffect != foundEffect {
		klog.Infof("Avoidance taint state changed (old effect [%v]) to %v for node: %v", foundEffect, desiredEffect, node.Name)
	}

	return nil
}

func (p *Prioritization) setNoExecuteTaint(nodeName string, podClass PodClass) error {
	nodeObj, exists, err := nodesInformer.GetIndexer().GetByKey(nodeName)

	if err != nil {
		return fmt.Errorf("error getting node to set NoExecute: %w", err)
	}

	if !exists {
		return fmt.Errorf("node targeted for NoExecute no longer exists")
	}

	node := nodeObj.(*corev1.Node)
	nodeTaints := node.Spec.Taints
	if nodeTaints == nil {
		nodeTaints = make([]corev1.Taint, 0)
	}

	// See if NoExecute is already set
	for _, taint := range nodeTaints {
		if taint.Key == CiWorkloadPreferNoExecuteTaintName {
			// Nothing to do if the taint exists
			return nil
		}
	}

	nodeTaints = append(nodeTaints, corev1.Taint{
		Key:    CiWorkloadPreferNoExecuteTaintName,
		Value:  fmt.Sprintf("%v", podClass),
		Effect: corev1.TaintEffectNoExecute,
	})

	taintMap := map[string][]corev1.Taint{
		"taints": nodeTaints,
	}
	unstructuredTaints, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&taintMap)
	if err != nil {
		return fmt.Errorf("error decoding modified taints to unstructured data: %w", err)
	}

	patch := map[string]interface{}{
		"op":    "add",
		"path":  "/spec/taints",
		"value": unstructuredTaints["taints"],
	}

	patchEntries := make([]map[string]interface{}, 0)
	patchEntries = append(patchEntries, patch)

	payloadBytes, _ := json.Marshal(patchEntries)
	_, err = p.k8sClientSet.CoreV1().Nodes().Patch(p.context, node.Name, types.JSONPatchType, payloadBytes, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("failed to change set NoExecute taint for node %v: %#w", node.Name, err)
	}

	return nil
}

func (p *Prioritization) findHostnamesToPreclude(podClass PodClass) []string {
	hostnamesToPreclude := make([]string, 0)
	nodesToPreclude, err := p.findNodesToPreclude(podClass)
	if err != nil {
		klog.Warningf("Error during node avoidance process: %#v", err)
	} else {
		for _, nodeToPreclude := range nodesToPreclude {
			hostname := p.getNodeHostname(nodeToPreclude)
			if len(hostname) == 0 {
				klog.Errorf("Unable to get %v label for node: %v", KubernetesHostnameLabelName, nodeToPreclude.Name)
				continue
			}
			hostnamesToPreclude = append(hostnamesToPreclude, hostname)
		}
	}
	klog.Infof("Precluding hostnames for podClass %v: %v", podClass, hostnamesToPreclude)
	return hostnamesToPreclude
}
