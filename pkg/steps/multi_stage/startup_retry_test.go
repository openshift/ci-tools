package multi_stage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	coreapi "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/utils/ptr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	testhelper "github.com/openshift/ci-tools/pkg/testhelper/kubernetes"
)

// startupPendingPod supplies complete never-started kubelet status.
func startupPendingPod() *coreapi.Pod {
	return &coreapi.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-post", Namespace: "ns", UID: "uid", ResourceVersion: "1", CreationTimestamp: metav1.NewTime(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)), Labels: map[string]string{steps.LabelMetadataStep: "post"}},
		Spec:       coreapi.PodSpec{NodeName: "failed-node", RestartPolicy: coreapi.RestartPolicyNever, Containers: []coreapi.Container{{Name: "test"}}},
		Status: coreapi.PodStatus{
			Phase:             coreapi.PodPending,
			Conditions:        []coreapi.PodCondition{{Type: coreapi.PodScheduled, Status: coreapi.ConditionTrue}, {Type: coreapi.PodReadyToStartContainers, Status: coreapi.ConditionFalse}},
			ContainerStatuses: []coreapi.ContainerStatus{{Name: "test", State: coreapi.ContainerState{Waiting: &coreapi.ContainerStateWaiting{Reason: "ContainerCreating"}}}},
		},
	}
}

// startupTimeoutEvent identifies the scheduled pod and the reporting kubelet.
func startupTimeoutEvent(pod *coreapi.Pod) *coreapi.Event {
	return &coreapi.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: string(pod.UID), Namespace: pod.Namespace},
		InvolvedObject: coreapi.ObjectReference{Kind: "Pod", Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID},
		Source:         coreapi.EventSource{Component: "kubelet", Host: pod.Spec.NodeName},
		Type:           coreapi.EventTypeWarning, Reason: "FailedCreatePodSandBox", Message: "Failed to create pod sandbox: context deadline exceeded", LastTimestamp: pod.CreationTimestamp,
	}
}

func TestNeverStartedPod(t *testing.T) {
	for _, name := range []string{"eligible", "eligible waiting init", "missing status", "extra status", "duplicate status", "unknown name", "running", "terminated", "last running", "last terminated", "last waiting", "container id", "restarted", "started", "ready", "image failure", "config failure", "unknown waiting", "empty state", "missing uid", "missing version", "missing timestamp", "unscheduled", "missing condition", "sandbox ready", "not scheduled", "unknown phase", "deleting", "init started", "missing init status", "restartable pod", "ephemeral container", "ephemeral status", "nil pod", "no containers", "unknown scheduling", "unknown sandbox", "init previously terminated"} {
		t.Run(name, func(t *testing.T) {
			pod := startupPendingPod()
			status := &pod.Status.ContainerStatuses[0]
			switch name {
			case "missing status":
				pod.Status.ContainerStatuses = nil
			case "extra status":
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, coreapi.ContainerStatus{Name: "other"})
			case "duplicate status":
				pod.Spec.Containers = append(pod.Spec.Containers, coreapi.Container{Name: "other"})
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, *status)
			case "unknown name":
				status.Name = "unknown"
			case "running":
				status.State.Running = &coreapi.ContainerStateRunning{}
			case "terminated":
				status.State.Terminated = &coreapi.ContainerStateTerminated{}
			case "last running":
				status.LastTerminationState.Running = &coreapi.ContainerStateRunning{}
			case "last terminated":
				status.LastTerminationState.Terminated = &coreapi.ContainerStateTerminated{}
			case "last waiting":
				status.LastTerminationState.Waiting = &coreapi.ContainerStateWaiting{}
			case "container id":
				status.ContainerID = "runtime://container"
			case "restarted":
				status.RestartCount = 1
			case "started":
				status.Started = ptr.To(true)
			case "ready":
				status.Ready = true
			case "image failure":
				status.State.Waiting.Reason = "ImagePullBackOff"
			case "config failure":
				status.State.Waiting.Reason = "CreateContainerConfigError"
			case "unknown waiting":
				status.State.Waiting.Reason = ""
			case "empty state":
				status.State = coreapi.ContainerState{}
			case "missing uid":
				pod.UID = ""
			case "missing version":
				pod.ResourceVersion = ""
			case "missing timestamp":
				pod.CreationTimestamp = metav1.Time{}
			case "unscheduled":
				pod.Spec.NodeName = ""
			case "missing condition":
				pod.Status.Conditions = nil
			case "sandbox ready":
				pod.Status.Conditions[1].Status = coreapi.ConditionTrue
			case "not scheduled":
				pod.Status.Conditions[0].Status = coreapi.ConditionFalse
			case "unknown phase":
				pod.Status.Phase = coreapi.PodUnknown
			case "deleting":
				pod.DeletionTimestamp = &pod.CreationTimestamp
			case "restartable pod":
				pod.Spec.RestartPolicy = coreapi.RestartPolicyOnFailure
			case "ephemeral container":
				pod.Spec.EphemeralContainers = []coreapi.EphemeralContainer{{}}
			case "ephemeral status":
				pod.Status.EphemeralContainerStatuses = []coreapi.ContainerStatus{{}}
			case "nil pod":
				pod = nil
			case "no containers":
				pod.Spec.Containers = nil
			case "unknown scheduling":
				pod.Status.Conditions[0].Status = coreapi.ConditionUnknown
			case "unknown sandbox":
				pod.Status.Conditions[1].Status = coreapi.ConditionUnknown
			case "init started", "missing init status", "eligible waiting init", "init previously terminated":
				pod.Spec.InitContainers = []coreapi.Container{{Name: "init"}}
				if name == "init started" {
					pod.Status.InitContainerStatuses = []coreapi.ContainerStatus{{Name: "init", State: coreapi.ContainerState{Running: &coreapi.ContainerStateRunning{}}}}
				}
				if name == "eligible waiting init" || name == "init previously terminated" {
					pod.Status.InitContainerStatuses = []coreapi.ContainerStatus{{Name: "init", State: coreapi.ContainerState{Waiting: &coreapi.ContainerStateWaiting{Reason: "PodInitializing"}}}}
					if name == "init previously terminated" {
						pod.Status.InitContainerStatuses[0].LastTerminationState.Terminated = &coreapi.ContainerStateTerminated{}
					}
				}
			}
			if got := neverStartedPod(pod); got != strings.HasPrefix(name, "eligible") {
				t.Errorf("neverStartedPod = %v", got)
			}
		})
	}
}

// startupProbeClient injects API failures while retaining the controller-runtime fake.
type startupProbeClient struct {
	ctrlruntimeclient.Client
	getErr, listErr error
}

func (client startupProbeClient) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, object ctrlruntimeclient.Object, options ...ctrlruntimeclient.GetOption) error {
	if client.getErr != nil {
		return client.getErr
	}
	return client.Client.Get(ctx, key, object, options...)
}

func (client startupProbeClient) List(ctx context.Context, list ctrlruntimeclient.ObjectList, options ...ctrlruntimeclient.ListOption) error {
	if client.listErr != nil {
		return client.listErr
	}
	return client.Client.List(ctx, list, options...)
}

// startupEventUID indexes fake events like the apiserver field selector.
func startupEventUID(object ctrlruntimeclient.Object) []string {
	return []string{string(object.(*coreapi.Event).InvolvedObject.UID)}
}

func TestStartupRetryPod(t *testing.T) {
	for _, name := range []string{"eligible", "fresh version", "command error", "cancelled error", "cancelled context", "pinned node", "no event", "wrong uid", "wrong node", "wrong source", "wrong reason", "not timeout", "stale event", "image failure", "workload started since timeout", "replaced uid", "get error", "event error", "wrong name", "wrong namespace", "wrong kind", "not warning", "fresh node mismatch", "fresh ephemeral status", "nil template", "nil pod"} {
		t.Run(name, func(t *testing.T) {
			pod := startupPendingPod()
			template := pod.DeepCopy()
			template.Spec.NodeName = ""
			current := pod.DeepCopy()
			event := startupTimeoutEvent(pod)
			runErr := results.ForReason(api.ReasonPending).ForError(errors.New("startup timeout"))
			client := startupProbeClient{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "nil template":
				template = nil
			case "nil pod":
				pod = nil
			case "command error":
				runErr = errors.New("command failed")
			case "cancelled error":
				runErr = context.Canceled
			case "cancelled context":
				cancel()
			case "pinned node":
				template.Spec.NodeName = pod.Spec.NodeName
			case "wrong uid":
				event.InvolvedObject.UID = "other"
			case "wrong node":
				event.Source.Host = "other"
			case "wrong source":
				event.Source.Component = "other"
			case "wrong reason":
				event.Reason = "FailedMount"
			case "not timeout":
				event.Message = "invalid sandbox configuration"
			case "stale event":
				event.LastTimestamp = metav1.Time{}
			case "image failure":
				current.Status.ContainerStatuses[0].State.Waiting.Reason = "ErrImagePull"
			case "workload started since timeout":
				current.Status.ContainerStatuses[0].LastTerminationState.Terminated = &coreapi.ContainerStateTerminated{}
			case "replaced uid":
				current.UID = "other"
			case "get error":
				client.getErr = errors.New("forbidden")
			case "event error":
				client.listErr = errors.New("forbidden")
			case "fresh version":
				current.ResourceVersion = "2"
			case "wrong name":
				event.InvolvedObject.Name = "other"
			case "wrong namespace":
				event.InvolvedObject.Namespace = "other"
			case "wrong kind":
				event.InvolvedObject.Kind = "Node"
			case "not warning":
				event.Type = coreapi.EventTypeNormal
			case "fresh node mismatch":
				current.Spec.NodeName = "other"
			case "fresh ephemeral status":
				current.Status.EphemeralContainerStatuses = []coreapi.ContainerStatus{{}}
			}
			objects := []ctrlruntimeclient.Object{current}
			if name != "no event" {
				objects = append(objects, event)
			}
			client.Client = fakeclient.NewClientBuilder().WithIndex(&coreapi.Event{}, "involvedObject.uid", startupEventUID).WithObjects(objects...).Build()
			replacement, err := startupRetryPod(ctx, client, template, pod, runErr)
			if (err != nil) != (name == "get error" || name == "event error") {
				t.Fatalf("unexpected error: %v", err)
			}
			if (replacement != nil) != (name == "eligible" || name == "fresh version") {
				t.Fatalf("unexpected replacement: %v", replacement)
			}
			if name == "fresh version" && pod.ResourceVersion != "2" {
				t.Fatal("fresh resource version not retained for deletion")
			}
		})
	}
}

func TestPodOnDifferentNode(t *testing.T) {
	for _, name := range []string{"no affinity", "preferred only", "multiple alternatives", "empty selector", "empty term", "nil template"} {
		t.Run(name, func(t *testing.T) {
			if name == "nil template" {
				if podOnDifferentNode(nil, "failed-node") != nil {
					t.Fatal("nil template allowed a replacement")
				}
				return
			}
			pod := startupPendingPod()
			pod.Spec.NodeName = ""
			pod.Spec.NodeSelector = map[string]string{"kubernetes.io/arch": "amd64"}
			if name != "no affinity" {
				pod.Spec.Affinity = &coreapi.Affinity{PodAntiAffinity: &coreapi.PodAntiAffinity{}, NodeAffinity: &coreapi.NodeAffinity{PreferredDuringSchedulingIgnoredDuringExecution: []coreapi.PreferredSchedulingTerm{{Weight: 1, Preference: coreapi.NodeSelectorTerm{MatchExpressions: []coreapi.NodeSelectorRequirement{{Key: "pool", Operator: coreapi.NodeSelectorOpExists}}}}}}}
				pod.Spec.Affinity.PodAffinity = &coreapi.PodAffinity{RequiredDuringSchedulingIgnoredDuringExecution: []coreapi.PodAffinityTerm{{TopologyKey: "zone", LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}}}}}
				pod.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = []coreapi.PodAffinityTerm{{TopologyKey: "kubernetes.io/hostname"}}
			}
			switch name {
			case "multiple alternatives":
				pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &coreapi.NodeSelector{NodeSelectorTerms: []coreapi.NodeSelectorTerm{{MatchExpressions: []coreapi.NodeSelectorRequirement{{Key: "zone", Operator: coreapi.NodeSelectorOpIn, Values: []string{"a"}}}}, {MatchFields: []coreapi.NodeSelectorRequirement{{Key: "metadata.name", Operator: coreapi.NodeSelectorOpIn, Values: []string{"node-b"}}}}}}
			case "empty selector":
				pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &coreapi.NodeSelector{}
			case "empty term":
				pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &coreapi.NodeSelector{NodeSelectorTerms: []coreapi.NodeSelectorTerm{{}}}
			}
			original := pod.DeepCopy()
			replacement := podOnDifferentNode(pod, "failed-node")
			if diff := cmp.Diff(original, pod); diff != "" {
				t.Fatalf("mutated template (-want +got): %s", diff)
			}
			if replacement.Name == pod.Name || !strings.Contains(replacement.Name, "-startup-retry-") {
				t.Fatal("attempt name is not distinct")
			}
			if diff := cmp.Diff(pod.Spec.NodeSelector, replacement.Spec.NodeSelector); diff != "" {
				t.Fatal(diff)
			}
			selector := replacement.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
			for index, term := range selector.NodeSelectorTerms {
				if name == "empty term" {
					if len(term.MatchFields) != 0 {
						t.Fatal("empty term was broadened")
					}
					continue
				}
				want := coreapi.NodeSelectorRequirement{Key: "metadata.name", Operator: coreapi.NodeSelectorOpNotIn, Values: []string{"failed-node"}}
				if diff := cmp.Diff(want, term.MatchFields[len(term.MatchFields)-1]); diff != "" {
					t.Fatal(diff)
				}
				if name == "multiple alternatives" {
					if diff := cmp.Diff(pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[index].MatchExpressions, term.MatchExpressions); diff != "" {
						t.Fatal(diff)
					}
				}
			}
			if name == "empty selector" && len(selector.NodeSelectorTerms) != 0 {
				t.Fatal("empty selector was broadened")
			}
			if pod.Spec.Affinity != nil {
				restored := replacement.Spec.Affinity.DeepCopy()
				if pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
					restored.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = nil
				} else if name == "multiple alternatives" {
					for index := range restored.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
						term := &restored.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[index]
						term.MatchFields = term.MatchFields[:len(term.MatchFields)-1]
						if len(term.MatchFields) == 0 {
							term.MatchFields = nil
						}
					}
				}
				if diff := cmp.Diff(pod.Spec.Affinity, restored); diff != "" {
					t.Fatalf("existing affinity changed: %s", diff)
				}
			}
			if name != "no affinity" {
				if diff := cmp.Diff(pod.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution, replacement.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution); diff != "" {
					t.Fatal(diff)
				}
				if diff := cmp.Diff(pod.Spec.Affinity.PodAntiAffinity, replacement.Spec.Affinity.PodAntiAffinity); diff != "" {
					t.Fatal(diff)
				}
			}
		})
	}
}

// startupRunClient drives real runner waits with a deterministic fake pod executor.
type startupRunClient struct {
	*testhelper.FakePodClient
	failures             int
	scenario             string
	deleteAttempted      bool
	cancel               context.CancelFunc
	expectPreconditions  bool
	preconditionsChecked bool
	watchRequested       atomic.Bool
}

func (client *startupRunClient) WithNewLoggingClient() kubernetes.PodClient { return client }

func (client *startupRunClient) Create(ctx context.Context, object ctrlruntimeclient.Object, options ...ctrlruntimeclient.CreateOption) error {
	pod, ok := object.(*coreapi.Pod)
	if !ok {
		return client.FakePodClient.Create(ctx, object, options...)
	}
	if len(client.CreatedPods) > 0 && !client.deleteAttempted {
		return errors.New("replacement created before old pod deletion")
	}
	if len(client.CreatedPods) > 0 && client.scenario == "replacement create error" {
		client.CreatedPods = append(client.CreatedPods, pod.DeepCopy())
		return apierrors.NewUnauthorized("replacement create unauthorized")
	}
	if client.scenario == "initial command failure" || (len(client.CreatedPods) > 0 && client.scenario == "replacement command failure") {
		client.Failures = sets.New(pod.Name)
		return client.FakePodClient.Create(ctx, object, options...)
	}
	if len(client.CreatedPods) < client.failures {
		pending := startupPendingPod()
		pod.UID = types.UID(fmt.Sprintf("uid-%d", len(client.CreatedPods)))
		pod.Spec.NodeName = pending.Spec.NodeName
		pod.CreationTimestamp = pending.CreationTimestamp
		pod.Status = pending.Status
		if client.scenario == "initial image failure" {
			pod.Status.ContainerStatuses[0].State.Waiting.Reason = "ErrImagePull"
		}
		client.Pending.Insert(pod.Name)
		if client.scenario != "generic pending" {
			if err := client.LoggingClient.Create(ctx, startupTimeoutEvent(pod)); err != nil {
				return err
			}
		}
	}
	return client.FakePodClient.Create(ctx, object, options...)
}

func (client *startupRunClient) Delete(ctx context.Context, object ctrlruntimeclient.Object, options ...ctrlruntimeclient.DeleteOption) error {
	client.deleteAttempted = true
	if client.expectPreconditions {
		deleteOptions := &ctrlruntimeclient.DeleteOptions{}
		for _, option := range options {
			option.ApplyToDelete(deleteOptions)
		}
		if deleteOptions.Preconditions == nil || deleteOptions.Preconditions.UID == nil || *deleteOptions.Preconditions.UID != object.GetUID() || deleteOptions.Preconditions.ResourceVersion == nil || *deleteOptions.Preconditions.ResourceVersion != object.GetResourceVersion() {
			return errors.New("missing fresh UID/resource version preconditions")
		}
		client.preconditionsChecked = true
	}
	if client.scenario == "version conflict" {
		return apierrors.NewConflict(coreapi.Resource("pods"), object.GetName(), errors.New("resource version changed"))
	}
	if client.scenario == "delete error" {
		return errors.New("delete forbidden")
	}
	if client.scenario == "cancel before replacement" {
		client.cancel()
	}
	if client.scenario == "deletion unconfirmed" || client.scenario == "deletion still pending" || client.scenario == "started during deletion" || client.scenario == "terminal during deletion" || client.scenario == "unknown during deletion" {
		return nil
	}
	return client.FakePodClient.Delete(ctx, object, options...)
}

func (client *startupRunClient) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, object ctrlruntimeclient.Object, options ...ctrlruntimeclient.GetOption) error {
	if client.deleteAttempted && (client.scenario == "terminal during deletion" || client.scenario == "unknown during deletion") {
		if err := client.FakePodClient.Get(ctx, key, object, options...); err != nil {
			return err
		}
		pod := object.(*coreapi.Pod)
		pod.DeletionTimestamp = &pod.CreationTimestamp
		pod.Status.ContainerStatuses[0] = coreapi.ContainerStatus{Name: "test", State: coreapi.ContainerState{Terminated: &coreapi.ContainerStateTerminated{Reason: "ContainerStatusUnknown"}}}
		if client.scenario == "terminal during deletion" {
			pod.Status.Phase = coreapi.PodFailed
		}
		return nil
	}
	if client.scenario == "existing pod execution" && len(client.CreatedPods) == 0 {
		pod := startupPendingPod()
		pod.Status.ContainerStatuses[0].State.Running = &coreapi.ContainerStateRunning{}
		pod.DeepCopyInto(object.(*coreapi.Pod))
		return nil
	}
	if client.deleteAttempted && client.scenario == "deletion unconfirmed" {
		return errors.New("cannot confirm deletion")
	}
	if client.deleteAttempted && client.scenario == "deletion still pending" {
		err := client.FakePodClient.Get(ctx, key, object, options...)
		client.cancel()
		return err
	}
	if client.deleteAttempted && client.scenario == "started during deletion" {
		if err := client.FakePodClient.Get(ctx, key, object, options...); err != nil {
			return err
		}
		object.(*coreapi.Pod).Status.ContainerStatuses[0].State.Running = &coreapi.ContainerStateRunning{}
		return nil
	}
	return client.FakePodClient.Get(ctx, key, object, options...)
}

func (client *startupRunClient) Watch(ctx context.Context, list ctrlruntimeclient.ObjectList, options ...ctrlruntimeclient.ListOption) (watch.Interface, error) {
	client.watchRequested.Store(true)
	if client.scenario == "execution before waiting" {
		pod := client.CreatedPods[0].DeepCopy()
		watcher := watch.NewRaceFreeFake()
		started := pod.DeepCopy()
		started.Status.ContainerStatuses[0].State.Running = &coreapi.ContainerStateRunning{}
		watcher.Modify(started)
		watcher.Modify(pod)
		return watcher, nil
	}
	if client.scenario == "initial watch failure" {
		return nil, apierrors.NewUnauthorized("watch unauthorized")
	}
	if client.scenario == "initial watch error event" {
		watcher := watch.NewRaceFreeFake()
		watcher.Error(&metav1.Status{Reason: metav1.StatusReasonUnauthorized, Code: 401})
		return watcher, nil
	}
	return client.FakePodClient.Watch(ctx, list, options...)
}

func (client *startupRunClient) List(ctx context.Context, list ctrlruntimeclient.ObjectList, options ...ctrlruntimeclient.ListOption) error {
	if err := client.FakePodClient.List(ctx, list, options...); err != nil {
		return err
	}
	if pods, ok := list.(*coreapi.PodList); ok {
		if !client.watchRequested.Load() && client.scenario != "initial watch failure" {
			pods.Items = nil
			return nil
		}
		for index := range pods.Items {
			pod := &pods.Items[index]
			if err := client.FakePodClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(pod), pod); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestRunPodsStartupRetry(t *testing.T) {
	for _, testCase := range []struct {
		name, phase        string
		failures, attempts int
		wantErr            bool
		finalError         string
		noPreconditions    bool
	}{
		{name: "first failure then success", phase: "post", failures: 1, attempts: 2},
		{name: "exhaustion", phase: "post", failures: 2, attempts: 2, wantErr: true, finalError: "startup-retry-"},
		{name: "pre never retried", phase: "pre", failures: 1, attempts: 1, wantErr: true},
		{name: "test never retried", phase: "test", failures: 1, attempts: 1, wantErr: true},
		{name: "delete error", phase: "post", failures: 1, attempts: 1, wantErr: true},
		{name: "version conflict", phase: "post", failures: 1, attempts: 1, wantErr: true, finalError: "resource version changed"},
		{name: "deletion unconfirmed", phase: "post", failures: 1, attempts: 1, wantErr: true},
		{name: "deletion still pending", phase: "post", failures: 1, attempts: 1, wantErr: true, finalError: "context canceled"},
		{name: "started during deletion", phase: "post", failures: 1, attempts: 1, wantErr: true, finalError: "no longer has never-started status"},
		{name: "terminal during deletion", phase: "post", failures: 1, attempts: 1, wantErr: true, finalError: "no longer has never-started status"},
		{name: "unknown during deletion", phase: "post", failures: 1, attempts: 1, wantErr: true, finalError: "no longer has never-started status"},
		{name: "cancel before replacement", phase: "post", failures: 1, attempts: 1, wantErr: true},
		{name: "replacement create error", phase: "post", failures: 1, attempts: 2, wantErr: true, finalError: "replacement create unauthorized"},
		{name: "replacement command failure", phase: "post", failures: 1, attempts: 2, wantErr: true, finalError: "failed containers: test"},
		{name: "initial command failure", phase: "post", failures: 1, attempts: 1, wantErr: true, noPreconditions: true},
		{name: "initial image failure", phase: "post", failures: 1, attempts: 1, wantErr: true, noPreconditions: true},
		{name: "initial watch failure", phase: "post", failures: 1, attempts: 1, wantErr: true, noPreconditions: true},
		{name: "initial watch error event", phase: "post", failures: 1, attempts: 1, wantErr: true, noPreconditions: true},
		{name: "execution before waiting", phase: "post", failures: 1, attempts: 1, wantErr: true, noPreconditions: true},
		{name: "existing pod execution", phase: "post", failures: 1, attempts: 1, wantErr: true, noPreconditions: true},
		{name: "generic pending", phase: "post", failures: 1, attempts: 1, wantErr: true, noPreconditions: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backing := fakeclient.NewClientBuilder().WithIndex(&coreapi.Pod{}, "metadata.name", fakePodNameIndexer).WithIndex(&coreapi.Event{}, "involvedObject.uid", startupEventUID).Build()
			executor := &testhelper.FakePodExecutor{LoggingClient: loggingclient.New(backing, nil), Pending: sets.New[string]()}
			client := &startupRunClient{FakePodClient: &testhelper.FakePodClient{FakePodExecutor: executor, PendingTimeout: time.Nanosecond}, failures: testCase.failures, scenario: testCase.name, cancel: cancel}
			client.expectPreconditions = testCase.phase == "post" && !testCase.noPreconditions
			step := &multiStageTestStep{name: "test", client: client, config: &api.ReleaseBuildConfiguration{}, subLock: &sync.Mutex{}}
			pod := startupPendingPod()
			pod.ObjectMeta = metav1.ObjectMeta{Name: "test-post", Namespace: "ns", Labels: pod.Labels}
			pod.Spec.NodeName = ""
			pod.Status = coreapi.PodStatus{}
			err := step.runPods(ctx, testCase.phase, []coreapi.Pod{*pod}, nil)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("unexpected result: %v", err)
			}
			if testCase.finalError != "" && !strings.Contains(err.Error(), testCase.finalError) {
				t.Fatalf("final error missing: %v", err)
			}
			if client.expectPreconditions && !client.preconditionsChecked {
				t.Fatal("conditional deletion not checked")
			}
			if len(client.CreatedPods) != testCase.attempts {
				t.Fatalf("created %d pods, want %d", len(client.CreatedPods), testCase.attempts)
			}
			if len(step.subSteps) != testCase.attempts || !*step.subSteps[0].Failed {
				t.Fatal("first attempt failure not retained")
			}
			if testCase.attempts == 2 {
				if len(step.subTests) == 0 || step.subTests[0].FailureOutput == nil {
					t.Fatal("startup failure missing from JUnit")
				}
				if *step.subSteps[1].Failed != testCase.wantErr {
					t.Fatal("final attempt status not retained")
				}
				if testCase.wantErr && (!strings.Contains(err.Error(), "Failed to create pod sandbox") || len(step.subTests) < 2 || step.subTests[len(step.subTests)-1].FailureOutput == nil) {
					t.Fatal("original or final attempt failure lost")
				}
				if !strings.Contains(step.subTests[0].FailureOutput.Output, "Failed to create pod sandbox") {
					t.Fatal("startup event missing from JUnit")
				}
				if client.CreatedPods[1].Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchFields[0].Values[0] != "failed-node" {
					t.Fatal("replacement does not exclude failed node")
				}
			}
		})
	}
}

func TestPodExecutionObserved(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		phase  coreapi.PodPhase
		status coreapi.ContainerStatus
		want   bool
	}{
		{name: "waiting", phase: coreapi.PodPending, status: coreapi.ContainerStatus{State: coreapi.ContainerState{Waiting: &coreapi.ContainerStateWaiting{Reason: "ContainerCreating"}}}},
		{name: "unknown"},
		{name: "running phase", phase: coreapi.PodRunning, want: true},
		{name: "succeeded phase", phase: coreapi.PodSucceeded, want: true},
		{name: "failed phase", phase: coreapi.PodFailed, want: true},
		{name: "running", status: coreapi.ContainerStatus{State: coreapi.ContainerState{Running: &coreapi.ContainerStateRunning{}}}, want: true},
		{name: "terminated", status: coreapi.ContainerStatus{State: coreapi.ContainerState{Terminated: &coreapi.ContainerStateTerminated{}}}, want: true},
		{name: "previously running", status: coreapi.ContainerStatus{LastTerminationState: coreapi.ContainerState{Running: &coreapi.ContainerStateRunning{}}}, want: true},
		{name: "previously terminated", status: coreapi.ContainerStatus{LastTerminationState: coreapi.ContainerState{Terminated: &coreapi.ContainerStateTerminated{}}}, want: true},
		{name: "container id", status: coreapi.ContainerStatus{ContainerID: "runtime://container"}, want: true},
		{name: "restarted", status: coreapi.ContainerStatus{RestartCount: 1}, want: true},
		{name: "ready", status: coreapi.ContainerStatus{Ready: true}, want: true},
		{name: "started", status: coreapi.ContainerStatus{Started: ptr.To(true)}, want: true},
		{name: "nil pod", want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, kind := range []string{"regular", "init", "ephemeral"} {
				pod := &coreapi.Pod{Status: coreapi.PodStatus{Phase: testCase.phase}}
				if testCase.name == "nil pod" {
					if !podExecutionObserved(nil) {
						t.Fatal("nil observation allowed replay")
					}
					continue
				}
				switch kind {
				case "regular":
					pod.Status.ContainerStatuses = []coreapi.ContainerStatus{testCase.status}
				case "init":
					pod.Status.InitContainerStatuses = []coreapi.ContainerStatus{testCase.status}
				case "ephemeral":
					pod.Status.EphemeralContainerStatuses = []coreapi.ContainerStatus{testCase.status}
				}
				if got := podExecutionObserved(pod); got != testCase.want {
					t.Fatalf("%s execution observed = %t, want %t", kind, got, testCase.want)
				}
			}
		})
	}
}

func TestStartupAttemptClientGet(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		object ctrlruntimeclient.Object
	}{
		{name: "nil object"},
		{name: "typed nil pod", object: (*coreapi.Pod)(nil)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := &startupAttemptClient{}
			err := client.Get(context.Background(), ctrlruntimeclient.ObjectKey{}, testCase.object)
			if err == nil || err.Error() != "pod read requires an object" || !client.apiFailed.Load() {
				t.Fatalf("nil pod read did not fail closed: %v", err)
			}
		})
	}
}

func TestStartupAttemptClientList(t *testing.T) {
	for _, testCase := range []struct {
		name string
		list ctrlruntimeclient.ObjectList
	}{
		{name: "nil list"},
		{name: "typed nil pod list", list: (*coreapi.PodList)(nil)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := &startupAttemptClient{}
			err := client.List(context.Background(), testCase.list)
			if err == nil || err.Error() != "pod list requires an object" || !client.apiFailed.Load() {
				t.Fatalf("nil pod list did not fail closed: %v", err)
			}
		})
	}
}

func TestStartupDeletionClientGet(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		object ctrlruntimeclient.Object
	}{
		{name: "nil object"},
		{name: "typed nil pod", object: (*coreapi.Pod)(nil)},
		{name: "non pod", object: &coreapi.Node{}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := startupDeletionClient{}
			err := client.Get(context.Background(), ctrlruntimeclient.ObjectKey{}, testCase.object)
			if err == nil || err.Error() != "startup retry deletion requires a pod object" {
				t.Fatalf("invalid deletion observation did not fail closed: %v", err)
			}
		})
	}
}

func TestForwardStartupEvents(t *testing.T) {
	for _, name := range []string{"error event", "cancel", "stop", "upstream closed", "blocked cancel", "blocked stop", "nil pod", "typed nil pod", "invalid pod"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstreamEvents := make(chan watch.Event)
			upstream := watch.NewProxyWatcher(upstreamEvents)
			events := make(chan watch.Event)
			proxy := watch.NewProxyWatcher(events)
			client := &startupAttemptClient{}
			go forwardStartupEvents(ctx, upstream, proxy, events, client)
			if strings.HasPrefix(name, "blocked") {
				select {
				case upstreamEvents <- watch.Event{Type: watch.Error, Object: &metav1.Status{Code: 401}}:
				case <-time.After(time.Second):
					t.Fatal("forwarder did not receive blocked event")
				}
			}
			switch name {
			case "nil pod", "typed nil pod", "invalid pod":
				event := watch.Event{Type: watch.Added}
				if name == "typed nil pod" {
					var pod *coreapi.Pod
					event.Object = pod
				}
				if name == "invalid pod" {
					event.Object = &metav1.Status{}
				}
				select {
				case upstreamEvents <- event:
				case <-time.After(time.Second):
					t.Fatal("forwarder did not receive malformed event")
				}
				select {
				case _, open := <-events:
					if open || !client.apiFailed.Load() {
						t.Fatal("malformed pod event was not rejected")
					}
				case <-time.After(time.Second):
					t.Fatal("malformed pod event blocked shutdown")
				}
			case "error event":
				select {
				case upstreamEvents <- watch.Event{Type: watch.Error, Object: &metav1.Status{Code: 401}}:
				case <-time.After(time.Second):
					t.Fatal("forwarder did not receive error event")
				}
				select {
				case event := <-events:
					if event.Type != watch.Error || !client.apiFailed.Load() {
						t.Fatal("watch error not retained")
					}
				case <-time.After(time.Second):
					t.Fatal("event forwarding blocked")
				}
				proxy.Stop()
			case "cancel", "blocked cancel":
				cancel()
			case "stop", "blocked stop":
				proxy.Stop()
			case "upstream closed":
				close(upstreamEvents)
			}
			if strings.HasPrefix(name, "blocked") {
				select {
				case <-upstream.StopChan():
				case <-time.After(time.Second):
					t.Fatal("blocked forwarder did not stop before draining")
				}
			}
			deadline := time.After(time.Second)
			for {
				select {
				case _, open := <-events:
					if !open {
						select {
						case <-upstream.StopChan():
						default:
							t.Fatal("upstream watch not stopped")
						}
						return
					}
				case <-deadline:
					t.Fatal("watch forwarding goroutine leaked")
				}
			}
		})
	}
}
