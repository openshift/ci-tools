package multi_stage

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	coreapi "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/watch"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/results"
)

// startupAttemptClient remembers API failures that client-go may otherwise retry internally.
type startupAttemptClient struct {
	kubernetes.PodClient
	apiFailed           atomic.Bool
	watchEstablished    atomic.Bool
	executionObserved   atomic.Bool
	existingPodObserved atomic.Bool
	creating            bool
}

// Create records API failures during pod creation, including failures retried by the creation helper.
func (client *startupAttemptClient) Create(ctx context.Context, object ctrlruntimeclient.Object, options ...ctrlruntimeclient.CreateOption) error {
	err := client.PodClient.Create(ctx, object, options...)
	if err != nil {
		client.apiFailed.Store(true)
	}
	return err
}

// Get records failed pod reads during an attempt.
func (client *startupAttemptClient) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, object ctrlruntimeclient.Object, options ...ctrlruntimeclient.GetOption) error {
	pod, isPod := object.(*coreapi.Pod)
	if object == nil || (isPod && pod == nil) {
		client.apiFailed.Store(true)
		return fmt.Errorf("pod read requires an object")
	}
	err := client.PodClient.Get(ctx, key, object, options...)
	if err != nil && !(client.creating && apierrors.IsNotFound(err)) {
		client.apiFailed.Store(true)
	}
	if err == nil && client.creating {
		client.existingPodObserved.Store(true)
	}
	if pod, ok := object.(*coreapi.Pod); err == nil && ok && podExecutionObserved(pod) {
		client.executionObserved.Store(true)
	}
	return err
}

// List records failed lists during an attempt.
func (client *startupAttemptClient) List(ctx context.Context, list ctrlruntimeclient.ObjectList, options ...ctrlruntimeclient.ListOption) error {
	pods, isPods := list.(*coreapi.PodList)
	if list == nil || (isPods && pods == nil) {
		client.apiFailed.Store(true)
		return fmt.Errorf("pod list requires an object")
	}
	err := client.PodClient.List(ctx, list, options...)
	if err != nil {
		client.apiFailed.Store(true)
	}
	if err == nil && isPods {
		for index := range pods.Items {
			if podExecutionObserved(&pods.Items[index]) {
				client.executionObserved.Store(true)
			}
		}
	}
	return err
}

// Watch records both request failures and error events without changing watcher retry policy.
func (client *startupAttemptClient) Watch(ctx context.Context, list ctrlruntimeclient.ObjectList, options ...ctrlruntimeclient.ListOption) (watch.Interface, error) {
	upstream, err := client.PodClient.Watch(ctx, list, options...)
	if err != nil {
		client.apiFailed.Store(true)
		return nil, err
	}
	if upstream == nil {
		client.apiFailed.Store(true)
		return nil, fmt.Errorf("pod watch returned no stream")
	}
	client.watchEstablished.Store(true)
	events := make(chan watch.Event)
	proxy := watch.NewProxyWatcher(events)
	go forwardStartupEvents(ctx, upstream, proxy, events, client)
	return proxy, nil
}

// forwardStartupEvents forwards watch events until cancellation, stop, or upstream completion.
func forwardStartupEvents(ctx context.Context, upstream watch.Interface, proxy *watch.ProxyWatcher, events chan<- watch.Event, client *startupAttemptClient) {
	defer close(events)
	defer upstream.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-proxy.StopChan():
			return
		case event, open := <-upstream.ResultChan():
			if !open {
				return
			}
			if event.Type == watch.Error {
				client.apiFailed.Store(true)
			}
			pod, isPod := event.Object.(*coreapi.Pod)
			if (event.Type == watch.Added || event.Type == watch.Modified || event.Type == watch.Deleted) && (!isPod || pod == nil) {
				client.apiFailed.Store(true)
				return
			}
			if isPod && podExecutionObserved(pod) {
				client.executionObserved.Store(true)
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			case <-proxy.StopChan():
				return
			}
		}
	}
}

// podExecutionObserved treats execution evidence or an absent pod as permanently unsafe for replay.
func podExecutionObserved(pod *coreapi.Pod) bool {
	if pod == nil {
		return true
	}
	if pod.Status.Phase == coreapi.PodRunning || pod.Status.Phase == coreapi.PodSucceeded || pod.Status.Phase == coreapi.PodFailed {
		return true
	}
	for _, statuses := range [][]coreapi.ContainerStatus{pod.Status.ContainerStatuses, pod.Status.InitContainerStatuses, pod.Status.EphemeralContainerStatuses} {
		for _, status := range statuses {
			if status.State.Running != nil || status.State.Terminated != nil || status.LastTerminationState.Running != nil || status.LastTerminationState.Terminated != nil || status.ContainerID != "" || status.RestartCount != 0 || status.Ready || (status.Started != nil && *status.Started) {
				return true
			}
		}
	}
	return false
}

// startupDeletionClient rejects execution evidence observed while waiting for the original pod to disappear.
type startupDeletionClient struct {
	ctrlruntimeclient.Client
	uid types.UID
}

// Get requires positive never-started evidence until disappearance; terminal or unknown status fails closed.
func (client startupDeletionClient) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, object ctrlruntimeclient.Object, options ...ctrlruntimeclient.GetOption) error {
	pod, ok := object.(*coreapi.Pod)
	if !ok || pod == nil {
		return fmt.Errorf("startup retry deletion requires a pod object")
	}
	if err := client.Client.Get(ctx, key, object, options...); err != nil {
		return err
	}
	if pod.UID != client.uid {
		return fmt.Errorf("startup retry pod identity changed during deletion")
	}
	observed := pod.DeepCopy()
	observed.DeletionTimestamp = nil
	if !neverStartedPod(observed) {
		return fmt.Errorf("startup retry pod no longer has never-started status")
	}
	return nil
}

// startupRetryPod prepares a replacement only for a kubelet sandbox timeout before any container started.
func startupRetryPod(ctx context.Context, client ctrlruntimeclient.Client, template, pod *coreapi.Pod, runErr error) (*coreapi.Pod, error) {
	if template == nil || ctx.Err() != nil || template.Spec.NodeName != "" || !slices.Contains(results.Reasons(runErr), string(api.ReasonPending)) || !neverStartedPod(pod) {
		return nil, nil
	}
	current := &coreapi.Pod{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(pod), current); err != nil {
		return nil, fmt.Errorf("checking startup retry pod: %w", err)
	}
	if current.UID != pod.UID || current.Spec.NodeName != pod.Spec.NodeName || !neverStartedPod(current) {
		return nil, nil
	}
	events := &coreapi.EventList{}
	if err := client.List(ctx, events, &ctrlruntimeclient.ListOptions{
		Namespace:     pod.Namespace,
		FieldSelector: fields.OneTermEqualSelector("involvedObject.uid", string(pod.UID)),
	}); err != nil {
		return nil, fmt.Errorf("checking startup retry events: %w", err)
	}
	for _, event := range events.Items {
		if event.InvolvedObject.Kind != "Pod" || event.InvolvedObject.UID != current.UID || event.InvolvedObject.Name != current.Name || event.InvolvedObject.Namespace != current.Namespace ||
			event.Source.Component != "kubelet" || event.Source.Host != current.Spec.NodeName || event.Type != coreapi.EventTypeWarning ||
			event.Reason != "FailedCreatePodSandBox" || !strings.Contains(event.Message, "context deadline exceeded") ||
			event.LastTimestamp.Before(&current.CreationTimestamp) {
			continue
		}
		current.DeepCopyInto(pod)
		return podOnDifferentNode(template, current.Spec.NodeName), nil
	}
	return nil, nil
}

// neverStartedPod requires complete waiting states, with no evidence of current or previous execution.
func neverStartedPod(pod *coreapi.Pod) bool {
	if pod == nil || pod.UID == "" || pod.ResourceVersion == "" || pod.CreationTimestamp.IsZero() || pod.DeletionTimestamp != nil || pod.Status.Phase != coreapi.PodPending || pod.Spec.NodeName == "" || len(pod.Spec.Containers) == 0 || pod.Spec.RestartPolicy != coreapi.RestartPolicyNever || len(pod.Spec.EphemeralContainers) != 0 || len(pod.Status.EphemeralContainerStatuses) != 0 {
		return false
	}
	scheduled, sandboxMissing := false, false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == coreapi.PodScheduled && condition.Status == coreapi.ConditionTrue {
			scheduled = true
		}
		if condition.Type == coreapi.PodReadyToStartContainers && condition.Status == coreapi.ConditionFalse {
			sandboxMissing = true
		}
	}
	return scheduled && sandboxMissing && neverStartedContainers(pod.Spec.Containers, pod.Status.ContainerStatuses) && neverStartedContainers(pod.Spec.InitContainers, pod.Status.InitContainerStatuses)
}

// neverStartedContainers rejects absent, ambiguous, or previously executed container states.
func neverStartedContainers(containers []coreapi.Container, statuses []coreapi.ContainerStatus) bool {
	if len(containers) != len(statuses) {
		return false
	}
	for _, container := range containers {
		matches := 0
		for _, status := range statuses {
			if status.Name != container.Name {
				continue
			}
			matches++
			if status.State.Waiting == nil || status.State.Running != nil || status.State.Terminated != nil ||
				status.LastTerminationState.Waiting != nil || status.LastTerminationState.Running != nil || status.LastTerminationState.Terminated != nil ||
				status.ContainerID != "" || status.RestartCount != 0 || status.Ready || (status.Started != nil && *status.Started) ||
				(status.State.Waiting.Reason != "ContainerCreating" && status.State.Waiting.Reason != "PodInitializing") {
				return false
			}
		}
		if matches != 1 {
			return false
		}
	}
	return true
}

// podOnDifferentNode preserves scheduling constraints and adds a node exclusion, or returns nil for an absent template.
func podOnDifferentNode(template *coreapi.Pod, node string) *coreapi.Pod {
	if template == nil {
		return nil
	}
	pod := template.DeepCopy()
	pod.Name = fmt.Sprintf("%.230s-startup-retry-%s", template.Name, utilrand.String(5))
	if pod.Spec.Affinity == nil {
		pod.Spec.Affinity = &coreapi.Affinity{}
	}
	if pod.Spec.Affinity.NodeAffinity == nil {
		pod.Spec.Affinity.NodeAffinity = &coreapi.NodeAffinity{}
	}
	affinity := pod.Spec.Affinity.NodeAffinity
	if affinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		affinity.RequiredDuringSchedulingIgnoredDuringExecution = &coreapi.NodeSelector{NodeSelectorTerms: []coreapi.NodeSelectorTerm{{}}}
	}
	for index := range affinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		term := &affinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[index]
		if len(term.MatchExpressions) == 0 && len(term.MatchFields) == 0 && template.Spec.Affinity != nil && template.Spec.Affinity.NodeAffinity != nil && template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
			continue
		}
		term.MatchFields = append(term.MatchFields, coreapi.NodeSelectorRequirement{Key: "metadata.name", Operator: coreapi.NodeSelectorOpNotIn, Values: []string{node}})
	}
	return pod
}
