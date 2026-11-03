package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	coreapi "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/sets"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"sigs.k8s.io/prow/pkg/secretutil"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps"
)

// Startup collection limits bound cleanup requests and the projected artifact.
const (
	startupCleanupTimeout = 20 * time.Second
	startupRequestTimeout = 2 * time.Second
	startupLookback       = 15 * time.Minute
	startupMaxPods        = 32
	startupMaxNodes       = 3
	startupEventPageSize  = 100
	startupEventPages     = 2
	startupMaxEvents      = 20
	startupMaxText        = 253
	startupMaxBytes       = 64 * 1024
	startupSnapshotLimit  = 1000
)

// startupDiagnostics contains only observations associated with this run's pods.
type startupDiagnostics struct {
	Status          string        `json:"status"`
	PodEventsStatus string        `json:"pod_events_status"`
	Pods            []startupPod  `json:"pods,omitempty"`
	Nodes           []startupNode `json:"nodes,omitempty"`
	Truncated       bool          `json:"truncated"`
	Journals        string        `json:"journals"`
}

// startupPod associates a failed-start or unassigned pending pod with its node.
type startupPod struct {
	Name   string `json:"name"`
	Node   string `json:"node,omitempty"`
	Status string `json:"status"`
}

// startupNode excludes node metadata and free-form condition/event messages.
type startupNode struct {
	Name         string             `json:"name"`
	Status       string             `json:"status"`
	Conditions   []startupCondition `json:"conditions,omitempty"`
	EventsStatus string             `json:"events_status"`
	Events       []startupWarning   `json:"events,omitempty"`
	Truncated    bool               `json:"truncated"`
}

// startupCondition projects standard readiness/pressure status and timestamps.
type startupCondition struct {
	Type               coreapi.NodeConditionType `json:"type"`
	Status             coreapi.ConditionStatus   `json:"status"`
	LastHeartbeatTime  metav1.Time               `json:"last_heartbeat_time"`
	LastTransitionTime metav1.Time               `json:"last_transition_time"`
}

// startupWarning projects allowlisted runtime warnings, never their messages.
type startupWarning struct {
	Reason string      `json:"reason"`
	Source string      `json:"source"`
	Count  int32       `json:"count"`
	First  metav1.Time `json:"first"`
	Last   metav1.Time `json:"last"`
}

// startupReadStatus classifies API failures without disclosing error bodies.
func startupReadStatus(err error) string {
	if err == nil {
		return "available"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) || apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) {
		return "timeout"
	}
	if apierrors.IsForbidden(err) {
		return "forbidden"
	}
	if apierrors.IsUnauthorized(err) {
		return "unauthorized"
	}
	return "unavailable"
}

// startupFailureReason accepts startup failures rather than application failures.
func startupFailureReason(reason string) bool {
	switch reason {
	case "CreateContainerError", "CreateContainerConfigError", "FailedCreatePodSandBox", "ImageInspectError":
		return true
	}
	return false
}

// failedStartupPod checks statuses and exact, recent pod-event associations.
func failedStartupPod(pod *coreapi.Pod, events *coreapi.EventList, now time.Time) bool {
	if pod.Status.Phase != coreapi.PodPending && pod.Status.Phase != coreapi.PodFailed {
		return false
	}
	failure := startupFailureReason(pod.Status.Reason)
	mainContainers := sets.New[string]()
	for _, container := range pod.Spec.Containers {
		mainContainers.Insert(container.Name)
	}
	for _, status := range append(append([]coreapi.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		if status.State.Running != nil || status.LastTerminationState.Terminated != nil || status.RestartCount != 0 || (status.Started != nil && *status.Started) {
			return false
		}
		if status.State.Terminated != nil && (status.State.Terminated.ExitCode != 0 || mainContainers.Has(status.Name)) {
			return false
		}
		if status.State.Waiting != nil && startupFailureReason(status.State.Waiting.Reason) {
			failure = true
		}
	}
	if events != nil && pod.UID != "" {
		for index, event := range events.Items {
			if index == startupSnapshotLimit {
				break
			}
			if event.InvolvedObject.Kind == "Pod" && event.InvolvedObject.Namespace == pod.Namespace && event.InvolvedObject.Name == pod.Name && event.InvolvedObject.UID == pod.UID && event.Type == coreapi.EventTypeWarning && startupFailureReason(event.Reason) && recentStartupEvent(event, now.Add(-startupLookback), now) {
				failure = true
			}
		}
	}
	return failure
}

// recentStartupEvent checks the most recent observation, including event series.
func recentStartupEvent(event coreapi.Event, since, now time.Time) bool {
	_, last, _ := startupEventTimes(event)
	return !last.IsZero() && !last.Time.Before(since) && !last.Time.After(now)
}

// startupEventTimes normalizes legacy and series event timestamps and counts.
func startupEventTimes(event coreapi.Event) (metav1.Time, metav1.Time, int32) {
	first, last, count := event.FirstTimestamp, event.LastTimestamp, event.Count
	if first.IsZero() {
		first = metav1.NewTime(event.EventTime.Time)
	}
	if last.IsZero() {
		last = first
	}
	if event.Series != nil {
		last = metav1.NewTime(event.Series.LastObservedTime.Time)
		count = event.Series.Count
	}
	return first, last, count
}

// startupRuntimeWarning accepts only warnings explicitly referring to this node.
func startupRuntimeWarning(event coreapi.Event, node *coreapi.Node, since, now time.Time) bool {
	if node.UID == "" || event.InvolvedObject.Kind != "Node" || event.InvolvedObject.Namespace != "" || event.InvolvedObject.Name != node.Name || event.InvolvedObject.UID != node.UID || event.Type != coreapi.EventTypeWarning || !recentStartupEvent(event, since, now) {
		return false
	}
	if event.Source.Component != "kubelet" && event.ReportingController != "kubelet" {
		return false
	}
	switch event.Reason {
	case "ContainerGCFailed", "ImageGCFailed", "FreeDiskSpaceFailed", "InvalidDiskCapacity", "SystemOOM", "EvictionThresholdMet", "FailedNodeAllocatableEnforcement", "NodeNotReady":
		return true
	}
	return false
}

// collectStartupNode reads one assigned node and at most two event pages.
func collectStartupNode(ctx context.Context, client coreclientset.CoreV1Interface, name string, since, now time.Time) startupNode {
	result := startupNode{Name: name, EventsStatus: "unavailable"}
	if err := ctx.Err(); err != nil {
		result.Status = startupReadStatus(err)
		return result
	}
	requestCtx, cancel := context.WithTimeout(ctx, startupRequestTimeout)
	node, err := client.Nodes().Get(requestCtx, name, metav1.GetOptions{})
	cancel()
	result.Status = startupReadStatus(err)
	if err != nil {
		return result
	}
	if node.Name != name || node.UID == "" {
		result.Status = "unavailable"
		return result
	}
	seen := sets.New[coreapi.NodeConditionType]()
	for _, condition := range node.Status.Conditions {
		switch condition.Type {
		case coreapi.NodeReady, coreapi.NodeMemoryPressure, coreapi.NodeDiskPressure, coreapi.NodePIDPressure:
		default:
			continue
		}
		if seen.Has(condition.Type) || (condition.Status != coreapi.ConditionTrue && condition.Status != coreapi.ConditionFalse && condition.Status != coreapi.ConditionUnknown) {
			continue
		}
		seen.Insert(condition.Type)
		result.Conditions = append(result.Conditions, startupCondition{condition.Type, condition.Status, condition.LastHeartbeatTime, condition.LastTransitionTime})
	}
	options := metav1.ListOptions{Limit: startupEventPageSize, FieldSelector: fields.Set{"involvedObject.kind": "Node", "involvedObject.name": name, "involvedObject.uid": string(node.UID), "type": coreapi.EventTypeWarning}.AsSelector().String()}
	for page := 0; page < startupEventPages; page++ {
		if err := ctx.Err(); err != nil {
			result.EventsStatus = startupReadStatus(err)
			return result
		}
		requestCtx, cancel := context.WithTimeout(ctx, startupRequestTimeout)
		events, err := client.Events("").List(requestCtx, options)
		cancel()
		result.EventsStatus = startupReadStatus(err)
		if err != nil {
			return result
		}
		for index, event := range events.Items {
			if index >= startupEventPageSize {
				result.Truncated = true
				break
			}
			if !startupRuntimeWarning(event, node, since, now) {
				continue
			}
			if len(result.Events) == startupMaxEvents {
				result.Truncated = true
				break
			}
			first, last, count := startupEventTimes(event)
			result.Events = append(result.Events, startupWarning{Reason: event.Reason, Source: "kubelet", Count: count, First: first, Last: last})
		}
		if result.Truncated || events.Continue == "" {
			return result
		}
		options.Continue = events.Continue
	}
	result.Truncated = true
	return result
}

// collectStartupDiagnostics reuses namespace snapshots and never reads other pods.
func collectStartupDiagnostics(ctx context.Context, client coreclientset.CoreV1Interface, namespace, jobID string, pods *coreapi.PodList, podErr error, events *coreapi.EventList, now time.Time) startupDiagnostics {
	result := startupDiagnostics{Status: startupReadStatus(podErr), Journals: "unavailable: no approved sanitized collection path"}
	result.PodEventsStatus = "unavailable"
	if events != nil {
		result.PodEventsStatus = "available"
		result.Truncated = len(events.Items) > startupSnapshotLimit || events.Continue != ""
	}
	if podErr != nil || pods == nil {
		if podErr == nil {
			result.Status = "unavailable"
		}
		return result
	}
	if jobID == "" {
		result.Status = "missing_run_identity"
		return result
	}
	result.Status = "no_relevant_nodes"
	result.Truncated = result.Truncated || pods.Continue != "" || len(pods.Items) > startupSnapshotLimit
	nodes := sets.New[string]()
	for index := range pods.Items {
		if index == startupSnapshotLimit {
			break
		}
		pod := &pods.Items[index]
		if pod.Namespace != namespace || pod.Labels[steps.LabelJobID] != jobID || pod.Labels[steps.CreatedByCILabel] != "true" || (pod.Status.Phase != coreapi.PodPending && pod.Status.Phase != coreapi.PodFailed) {
			continue
		}
		status := "failed_start"
		if pod.Spec.NodeName == "" && pod.Status.Phase == coreapi.PodPending {
			status = "missing_assignment"
		} else if !failedStartupPod(pod, events, now) {
			continue
		}
		if pod.Spec.NodeName == "" {
			status = "missing_assignment"
		}
		if len(result.Pods) == startupMaxPods {
			result.Truncated = true
			break
		}
		result.Pods = append(result.Pods, startupPod{Name: pod.Name, Node: pod.Spec.NodeName, Status: status})
		if pod.Spec.NodeName != "" {
			nodes.Insert(pod.Spec.NodeName)
		}
	}
	for index, name := range sets.List(nodes) {
		if index == startupMaxNodes {
			result.Truncated = true
			break
		}
		result.Status = "collected"
		node := collectStartupNode(ctx, client, name, now.Add(-startupLookback), now)
		result.Truncated = result.Truncated || node.Truncated
		result.Nodes = append(result.Nodes, node)
	}
	return result
}

// marshalStartupDiagnostics censors strings before JSON encoding and caps output.
func marshalStartupDiagnostics(result startupDiagnostics, censor secretutil.Censorer) ([]byte, error) {
	for index := range result.Pods {
		result.Truncated = result.Truncated || len(result.Pods[index].Name) > startupMaxText || len(result.Pods[index].Node) > startupMaxText
		result.Pods[index].Name = censorStartupText(result.Pods[index].Name, censor)
		result.Pods[index].Node = censorStartupText(result.Pods[index].Node, censor)
	}
	for index := range result.Nodes {
		result.Truncated = result.Truncated || len(result.Nodes[index].Name) > startupMaxText
		result.Nodes[index].Name = censorStartupText(result.Nodes[index].Name, censor)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil || len(data) <= startupMaxBytes {
		return data, err
	}
	result.Truncated = true
	result.Nodes = nil
	data, err = json.MarshalIndent(result, "", "  ")
	if err != nil || len(data) <= startupMaxBytes {
		return data, err
	}
	result.Pods = nil
	return json.MarshalIndent(result, "", "  ")
}

// censorStartupText redacts before truncation so escaped secrets cannot leak.
func censorStartupText(value string, censor secretutil.Censorer) string {
	data := []byte(value)
	censor.Censor(&data)
	if len(data) > startupMaxText {
		data = data[:startupMaxText]
	}
	return string(data)
}

// saveStartupDiagnostics writes the allowlisted artifact through the usual censor.
func saveStartupDiagnostics(result startupDiagnostics, censor secretutil.Censorer) error {
	data, err := marshalStartupDiagnostics(result, censor)
	if err != nil {
		return err
	}
	return api.SaveArtifact(censor, filepath.Join(api.NamespaceDir, "node-startup.json"), data)
}
