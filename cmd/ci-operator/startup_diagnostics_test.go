package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/interrupt"
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/steps"
)

// startupTestTime supplies a fixed observation time without live cluster state.
func startupTestTime() time.Time { return time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC) }

// startupTestPod creates a namespace-local pod that failed before starting.
func startupTestPod(name, node string) coreapi.Pod {
	return coreapi.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "run", UID: types.UID(name), Labels: map[string]string{steps.LabelJobID: "run-id", steps.CreatedByCILabel: "true"}},
		Spec:       coreapi.PodSpec{NodeName: node, Containers: []coreapi.Container{{Name: "test"}}},
		Status:     coreapi.PodStatus{Phase: coreapi.PodPending, ContainerStatuses: []coreapi.ContainerStatus{{Name: "test", State: coreapi.ContainerState{Waiting: &coreapi.ContainerStateWaiting{Reason: "CreateContainerError"}}}}},
	}
}

// startupTestNode creates a node with deliberately private non-projected fields.
func startupTestNode() *coreapi.Node {
	return &coreapi.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker", UID: "node-uid", Labels: map[string]string{"private": "never-emit"}, Annotations: map[string]string{"private": "never-emit"}},
		Status: coreapi.NodeStatus{Addresses: []coreapi.NodeAddress{{Address: "never-emit"}}, Conditions: []coreapi.NodeCondition{
			{Type: coreapi.NodeReady, Status: coreapi.ConditionFalse, Reason: "never-emit", Message: "never-emit", LastTransitionTime: metav1.NewTime(startupTestTime())},
			{Type: "private-condition-never-emit", Status: coreapi.ConditionTrue},
			{Type: coreapi.NodeReady, Status: coreapi.ConditionTrue},
			{Type: coreapi.NodeDiskPressure, Status: "never-emit"},
		}},
	}
}

// startupTestEvent creates a recent kubelet runtime warning with a private message.
func startupTestEvent() coreapi.Event {
	return coreapi.Event{
		InvolvedObject: coreapi.ObjectReference{Kind: "Node", Name: "worker", UID: "node-uid"},
		Type:           coreapi.EventTypeWarning, Reason: "ContainerGCFailed", Message: "never-emit",
		Source: coreapi.EventSource{Component: "kubelet", Host: "never-emit"}, Count: 2,
		FirstTimestamp: metav1.NewTime(startupTestTime().Add(-time.Minute)), LastTimestamp: metav1.NewTime(startupTestTime()),
	}
}

func TestStartupReadStatus(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"success", nil, "available"},
		{"forbidden", apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "private", errors.New("never-emit")), "forbidden"},
		{"unauthorized", apierrors.NewUnauthorized("never-emit"), "unauthorized"},
		{"missing", apierrors.NewNotFound(schema.GroupResource{Resource: "nodes"}, "private"), "unavailable"},
		{"error", errors.New("never-emit"), "unavailable"},
		{"canceled", fmt.Errorf("wrapped: %w", context.Canceled), "canceled"},
		{"deadline", context.DeadlineExceeded, "timeout"},
		{"server timeout", apierrors.NewTimeoutError("never-emit", 0), "timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := startupReadStatus(test.err); got != test.want {
				t.Errorf("want %q, got %q", test.want, got)
			}
		})
	}
}

func TestStartupFailureReason(t *testing.T) {
	for _, test := range []struct {
		reason string
		want   bool
	}{
		{"CreateContainerError", true}, {"CreateContainerConfigError", true}, {"FailedCreatePodSandBox", true}, {"ImageInspectError", true},
		{"CrashLoopBackOff", false}, {"ContainerCreating", false}, {"Error", false}, {"", false},
	} {
		t.Run(test.reason, func(t *testing.T) {
			if got := startupFailureReason(test.reason); got != test.want {
				t.Errorf("want %v, got %v", test.want, got)
			}
		})
	}
}

func TestFailedStartupPod(t *testing.T) {
	for _, test := range []struct {
		name       string
		phase      coreapi.PodPhase
		waiting    string
		running    bool
		terminated bool
		restarted  bool
		sandbox    bool
		wrongUID   bool
		old        bool
		eventField string
		initOK     bool
		history    bool
		want       bool
	}{
		{name: "create error", phase: coreapi.PodPending, waiting: "CreateContainerError", want: true},
		{name: "inspect error", phase: coreapi.PodPending, waiting: "ImageInspectError", want: true},
		{name: "sandbox warning", phase: coreapi.PodPending, sandbox: true, want: true},
		{name: "other pod warning", phase: coreapi.PodPending, sandbox: true, wrongUID: true},
		{name: "old sandbox warning", phase: coreapi.PodPending, sandbox: true, old: true},
		{name: "wrong pod name", phase: coreapi.PodPending, sandbox: true, eventField: "name"},
		{name: "wrong pod namespace", phase: coreapi.PodPending, sandbox: true, eventField: "namespace"},
		{name: "wrong pod kind", phase: coreapi.PodPending, sandbox: true, eventField: "kind"},
		{name: "normal pod warning", phase: coreapi.PodPending, sandbox: true, eventField: "type"},
		{name: "future pod warning", phase: coreapi.PodPending, sandbox: true, eventField: "future"},
		{name: "empty pod UID", phase: coreapi.PodPending, sandbox: true, eventField: "uid"},
		{name: "event scan limit", phase: coreapi.PodPending, sandbox: true, eventField: "limit"},
		{name: "completed init", phase: coreapi.PodPending, waiting: "CreateContainerError", initOK: true, want: true},
		{name: "termination history", phase: coreapi.PodPending, waiting: "CreateContainerError", history: true},
		{name: "running", phase: coreapi.PodRunning, waiting: "CreateContainerError"},
		{name: "completed", phase: coreapi.PodSucceeded, waiting: "CreateContainerError"},
		{name: "active init", phase: coreapi.PodPending, waiting: "CreateContainerError", running: true},
		{name: "application terminated", phase: coreapi.PodFailed, waiting: "CreateContainerError", terminated: true},
		{name: "restarted", phase: coreapi.PodPending, waiting: "CreateContainerError", restarted: true},
		{name: "healthy pending", phase: coreapi.PodPending, waiting: "ContainerCreating"},
	} {
		t.Run(test.name, func(t *testing.T) {
			pod := startupTestPod("test", "worker")
			pod.Status.Phase = test.phase
			pod.Status.ContainerStatuses[0].State.Waiting.Reason = test.waiting
			if test.initOK {
				pod.Status.InitContainerStatuses = []coreapi.ContainerStatus{{Name: "init", State: coreapi.ContainerState{Terminated: &coreapi.ContainerStateTerminated{ExitCode: 0}}}}
			}
			if test.history {
				pod.Status.ContainerStatuses[0].LastTerminationState.Terminated = &coreapi.ContainerStateTerminated{}
			}
			if test.eventField == "uid" {
				pod.UID = ""
			}
			if test.running {
				pod.Status.InitContainerStatuses = []coreapi.ContainerStatus{{Name: "init", State: coreapi.ContainerState{Running: &coreapi.ContainerStateRunning{}}}}
			}
			if test.terminated {
				pod.Status.ContainerStatuses[0].State.Terminated = &coreapi.ContainerStateTerminated{ExitCode: 1}
			}
			if test.restarted {
				pod.Status.ContainerStatuses[0].RestartCount = 1
			}
			events := &coreapi.EventList{}
			if test.sandbox {
				event := startupTestEvent()
				event.InvolvedObject = coreapi.ObjectReference{Kind: "Pod", Namespace: "run", Name: "test", UID: pod.UID}
				event.Reason = "FailedCreatePodSandBox"
				switch test.eventField {
				case "name":
					event.InvolvedObject.Name = "other"
				case "namespace":
					event.InvolvedObject.Namespace = "other"
				case "kind":
					event.InvolvedObject.Kind = "Node"
				case "type":
					event.Type = coreapi.EventTypeNormal
				case "future":
					event.LastTimestamp = metav1.NewTime(startupTestTime().Add(time.Second))
				case "limit":
					events.Items = make([]coreapi.Event, startupSnapshotLimit)
				}
				if test.wrongUID {
					event.InvolvedObject.UID = "other"
				}
				if test.old {
					event.LastTimestamp = metav1.NewTime(startupTestTime().Add(-time.Hour))
				}
				events.Items = append(events.Items, event)
			}
			if got := failedStartupPod(&pod, events, startupTestTime()); got != test.want {
				t.Errorf("want %v, got %v", test.want, got)
			}
		})
	}
}

func TestStartupEventTimes(t *testing.T) {
	for _, test := range []struct {
		name   string
		series bool
		modern bool
	}{{name: "legacy"}, {name: "modern", modern: true}, {name: "series", modern: true, series: true}} {
		t.Run(test.name, func(t *testing.T) {
			event := startupTestEvent()
			wantFirst, wantLast, wantCount := event.FirstTimestamp, event.LastTimestamp, event.Count
			if test.modern {
				event.FirstTimestamp, event.LastTimestamp = metav1.Time{}, metav1.Time{}
				event.EventTime = metav1.NewMicroTime(startupTestTime())
				wantFirst = metav1.NewTime(startupTestTime())
				wantLast = wantFirst
			}
			if test.series {
				event.Series = &coreapi.EventSeries{Count: 5, LastObservedTime: metav1.NewMicroTime(startupTestTime().Add(time.Minute))}
				wantLast = metav1.NewTime(event.Series.LastObservedTime.Time)
				wantCount = 5
			}
			first, last, count := startupEventTimes(event)
			if diff := cmp.Diff([]any{wantFirst, wantLast, wantCount}, []any{first, last, count}); diff != "" {
				t.Errorf("unexpected times (-want +got): %s", diff)
			}
		})
	}
}

func TestRecentStartupEvent(t *testing.T) {
	for _, test := range []struct {
		name   string
		offset time.Duration
		zero   bool
		want   bool
	}{{name: "recent", want: true}, {name: "boundary", offset: -startupLookback, want: true}, {name: "old", offset: -time.Hour}, {name: "future", offset: time.Second}, {name: "unknown", zero: true}} {
		t.Run(test.name, func(t *testing.T) {
			event := startupTestEvent()
			event.LastTimestamp = metav1.NewTime(startupTestTime().Add(test.offset))
			if test.zero {
				event.FirstTimestamp, event.LastTimestamp = metav1.Time{}, metav1.Time{}
			}
			if got := recentStartupEvent(event, startupTestTime().Add(-startupLookback), startupTestTime()); got != test.want {
				t.Errorf("want %v, got %v", test.want, got)
			}
		})
	}
}

// TestStartupRuntimeWarning checks exact node association, optional UID and provenance.
func TestStartupRuntimeWarning(t *testing.T) {
	for _, test := range []struct {
		name, field, value string
		want               bool
	}{
		{name: "allowed", want: true}, {name: "kubelet omits UID", field: "uid", want: true}, {name: "other node", field: "name", value: "other"}, {name: "other UID", field: "uid", value: "other"},
		{name: "pod event", field: "kind", value: "Pod"}, {name: "namespace", field: "namespace", value: "other"},
		{name: "normal", field: "type", value: coreapi.EventTypeNormal}, {name: "unapproved reason", field: "reason", value: "private"},
		{name: "unapproved source", field: "source", value: "private"}, {name: "reporting controller", field: "reporter", value: "kubelet", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := startupTestEvent()
			switch test.field {
			case "name":
				event.InvolvedObject.Name = test.value
			case "uid":
				event.InvolvedObject.UID = types.UID(test.value)
			case "kind":
				event.InvolvedObject.Kind = test.value
			case "namespace":
				event.InvolvedObject.Namespace = test.value
			case "type":
				event.Type = test.value
			case "reason":
				event.Reason = test.value
			case "source":
				event.Source.Component = test.value
			case "reporter":
				event.Source.Component = ""
				event.ReportingController = test.value
			}
			if got := startupRuntimeWarning(event, startupTestNode(), startupTestTime().Add(-startupLookback), startupTestTime()); got != test.want {
				t.Errorf("want %v, got %v", test.want, got)
			}
		})
	}
}

// startupReactor supplies bounded node/event responses and API failures.
type startupReactor struct {
	nodeError    error
	eventError   error
	events       []coreapi.Event
	continuation bool
	pages        int
}

// react handles only reads; every unexpected action fails the test through its error.
func (reactor *startupReactor) react(action clienttesting.Action) (bool, runtime.Object, error) {
	if action.GetVerb() == "get" && action.GetResource().Resource == "nodes" {
		return true, startupTestNode(), reactor.nodeError
	}
	if action.GetVerb() == "list" && action.GetResource().Resource == "events" {
		reactor.pages++
		selector := action.(clienttesting.ListAction).GetListRestrictions().Fields.String()
		options := action.(clienttesting.ListActionImpl).ListOptions
		if action.GetNamespace() != "" || options.Limit != startupEventPageSize || (reactor.pages > 1 && options.Continue != "next") || selector != "involvedObject.kind=Node,involvedObject.name=worker,type=Warning" {
			return true, nil, errors.New("missing strict selector")
		}
		list := &coreapi.EventList{Items: reactor.events}
		if reactor.continuation {
			list.Continue = "next"
		}
		return true, list, reactor.eventError
	}
	return true, nil, errors.New("unexpected API action")
}

// TestCollectStartupNode exercises bounded selectors, defensive responses and read failures.
func TestCollectStartupNode(t *testing.T) {
	for _, test := range []struct {
		name                   string
		nodeErr, eventErr      error
		count                  int
		continuation           bool
		canceled               bool
		expired                bool
		inFlight               bool
		timeout                bool
		eventOnly              bool
		noise                  bool
		nilNode, nilEvents     bool
		wantStatus, wantEvents string
		wantCount, wantPages   int
		truncated              bool
	}{
		{name: "available sanitized", count: 1, wantStatus: "available", wantEvents: "available", wantCount: 1, wantPages: 1},
		{name: "nil node response", nilNode: true, wantStatus: "unavailable", wantEvents: "unavailable"},
		{name: "nil events response", nilEvents: true, wantStatus: "available", wantEvents: "unavailable"},
		{name: "node forbidden", nodeErr: apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "worker", errors.New("never-emit")), wantStatus: "forbidden", wantEvents: "unavailable"},
		{name: "event list forbidden", eventErr: apierrors.NewForbidden(schema.GroupResource{Resource: "events"}, "", errors.New("never-emit")), wantStatus: "available", wantEvents: "forbidden", wantPages: 1},
		{name: "event list unavailable", eventErr: errors.New("never-emit"), wantStatus: "available", wantEvents: "unavailable", wantPages: 1},
		{name: "node timeout", nodeErr: context.DeadlineExceeded, wantStatus: "timeout", wantEvents: "unavailable"},
		{name: "canceled", canceled: true, wantStatus: "canceled", wantEvents: "unavailable"},
		{name: "expired", expired: true, wantStatus: "timeout", wantEvents: "unavailable"},
		{name: "in flight cancellation", inFlight: true, wantStatus: "canceled", wantEvents: "unavailable"},
		{name: "in flight get timeout", inFlight: true, timeout: true, wantStatus: "timeout", wantEvents: "unavailable"},
		{name: "in flight list timeout", inFlight: true, timeout: true, eventOnly: true, wantStatus: "available", wantEvents: "timeout"},
		{name: "server ignores page limit", count: startupEventPageSize + 1, noise: true, wantStatus: "available", wantEvents: "available", wantPages: 1, truncated: true},
		{name: "second page results", count: 1, continuation: true, wantStatus: "available", wantEvents: "available", wantCount: 2, wantPages: 2, truncated: true},
		{name: "event cap", count: startupMaxEvents + 1, wantStatus: "available", wantEvents: "available", wantCount: startupMaxEvents, wantPages: 1, truncated: true},
		{name: "page cap", continuation: true, wantStatus: "available", wantEvents: "available", wantPages: startupEventPages, truncated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reactor := &startupReactor{nodeError: test.nodeErr, eventError: test.eventErr, continuation: test.continuation}
			for index := 0; index < test.count; index++ {
				event := startupTestEvent()
				if test.noise {
					event.Reason = "never-emit"
				}
				reactor.events = append(reactor.events, event)
			}
			client := kubefake.NewClientset()
			client.PrependReactor("*", "*", reactor.react)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.canceled {
				cancel()
			}
			if test.expired {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithDeadline(ctx, startupTestTime())
				defer deadlineCancel()
			}
			var reader coreclientset.CoreV1Interface = client.CoreV1()
			if test.nilNode || test.nilEvents {
				reader = startupNilCore{CoreV1Interface: reader, nilNode: test.nilNode}
			}
			transport := &startupCancelTransport{cancel: cancel, timeout: test.timeout, eventOnly: test.eventOnly}
			if test.inFlight {
				var err error
				reader, err = coreclientset.NewForConfig(&rest.Config{Host: "https://fixture.invalid", Transport: transport})
				if err != nil {
					t.Fatal(err)
				}
			}
			result := collectStartupNode(ctx, reader, "worker", startupTestTime().Add(-startupLookback), startupTestTime())
			if test.inFlight && !transport.bounded {
				t.Error("request has no deadline")
			}
			if test.inFlight {
				wantRequests := 1
				if test.eventOnly {
					wantRequests = 2
				}
				if transport.requests != wantRequests {
					t.Errorf("unexpected requests after failure: %d", transport.requests)
				}
			}
			if diff := cmp.Diff([]any{test.wantStatus, test.wantEvents, test.wantCount, test.wantPages, test.truncated}, []any{result.Status, result.EventsStatus, len(result.Events), reactor.pages, result.Truncated}); diff != "" {
				t.Errorf("unexpected collection (-want +got): %s", diff)
			}
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "never-emit") {
				t.Fatalf("private node/event content escaped: %s", data)
			}
			if result.Status == "available" && len(result.Conditions) != 1 {
				t.Errorf("expected one canonical condition, got %v", result.Conditions)
			}
			if (test.canceled || test.expired) && len(client.Actions()) != 0 {
				t.Error("queried API after cancellation")
			}
		})
	}
}

func TestCollectStartupDiagnostics(t *testing.T) {
	for _, test := range []struct {
		missingCreated, falseCreated                   bool
		name                                           string
		count                                          int
		healthy, unassigned, otherNamespace, duplicate bool
		podErr                                         error
		nilPods                                        bool
		wantNodes, wantPods                            int
		wantStatus                                     string
		truncated                                      bool
		otherRun, noLabels, noIdentity, continuation   bool
	}{
		{name: "failed startup", count: 1, wantNodes: 1, wantPods: 1, wantStatus: "collected"},
		{name: "duplicate nodes", count: 2, duplicate: true, wantNodes: 1, wantPods: 2, wantStatus: "collected"},
		{name: "healthy", count: 1, healthy: true, wantStatus: "no_relevant_nodes"},
		{name: "unassigned", count: 1, unassigned: true, wantPods: 1, wantStatus: "no_relevant_nodes"},
		{name: "unrelated workload", count: 1, otherNamespace: true, wantStatus: "no_relevant_nodes"},
		{name: "other run same namespace", count: 1, otherRun: true, wantStatus: "no_relevant_nodes"},
		{name: "no ownership labels", count: 1, noLabels: true, wantStatus: "no_relevant_nodes"},
		{name: "correct job but missing created label", count: 1, missingCreated: true, wantStatus: "no_relevant_nodes"},
		{name: "correct job but false created label", count: 1, falseCreated: true, wantStatus: "no_relevant_nodes"},
		{name: "missing identity", count: 1, noIdentity: true, wantStatus: "missing_run_identity"},
		{name: "snapshot scan limit", count: startupSnapshotLimit + 1, healthy: true, wantStatus: "no_relevant_nodes", truncated: true},
		{name: "snapshot continuation", continuation: true, wantStatus: "no_relevant_nodes", truncated: true},
		{name: "empty", wantStatus: "no_relevant_nodes"},
		{name: "list failure", podErr: errors.New("never-emit"), wantStatus: "unavailable"},
		{name: "nil snapshot", nilPods: true, wantStatus: "unavailable"},
		{name: "node cap", count: startupMaxNodes + 1, wantNodes: startupMaxNodes, wantPods: startupMaxNodes + 1, wantStatus: "collected", truncated: true},
		{name: "pod cap", count: startupMaxPods + 1, duplicate: true, wantNodes: 1, wantPods: startupMaxPods, wantStatus: "collected", truncated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			pods := &coreapi.PodList{}
			if test.continuation {
				pods.Continue = "next"
			}
			for index := 0; index < test.count; index++ {
				pod := startupTestPod(fmt.Sprintf("pod-%d", index), fmt.Sprintf("worker-%d", index))
				if test.duplicate || test.count == 1 {
					pod.Spec.NodeName = "worker"
				}
				if test.healthy {
					pod.Status.Phase = coreapi.PodRunning
				}
				if test.unassigned {
					pod.Spec.NodeName = ""
				}
				if test.otherNamespace {
					pod.Namespace = "other"
				}
				if test.otherRun {
					pod.Labels[steps.LabelJobID] = "other-run"
				}
				if test.noLabels {
					pod.Labels = nil
				}
				if test.missingCreated {
					delete(pod.Labels, steps.CreatedByCILabel)
				}
				if test.falseCreated {
					pod.Labels[steps.CreatedByCILabel] = "false"
				}
				pods.Items = append(pods.Items, pod)
			}
			if test.nilPods {
				pods = nil
			}
			client := kubefake.NewClientset(startupTestNode())
			jobID := "run-id"
			if test.noIdentity {
				jobID = ""
			}
			result := collectStartupDiagnostics(context.Background(), client.CoreV1(), "run", jobID, pods, test.podErr, nil, startupTestTime())
			if diff := cmp.Diff([]any{test.wantStatus, test.wantNodes, test.wantPods, test.truncated}, []any{result.Status, len(result.Nodes), len(result.Pods), result.Truncated}); diff != "" {
				t.Errorf("unexpected collection (-want +got): %s", diff)
			}
			if test.wantNodes == 0 && len(client.Actions()) != 0 {
				t.Error("queried nodes for an irrelevant snapshot")
			}
			gets := 0
			for _, action := range client.Actions() {
				if action.GetVerb() == "get" && action.GetResource().Resource == "nodes" {
					gets++
				}
			}
			if gets != test.wantNodes {
				t.Errorf("expected %d node GETs, got %d", test.wantNodes, gets)
			}
			if test.unassigned && result.Pods[0].Status != "missing_assignment" {
				t.Error("missing explicit assignment status")
			}
			if !strings.Contains(result.Journals, "unavailable") {
				t.Error("journal availability is misleading")
			}
		})
	}
}

func TestCensorStartupText(t *testing.T) {
	for _, test := range []struct{ name, value, secret, want string }{
		{"plain", "pod", "", "pod"},
		{"escaped secret", "secret\n\"quoted\"", "secret\n\"quoted\"", "CENSORED"},
		{"bounded", strings.Repeat("x", startupMaxText+1), "", strings.Repeat("x", startupMaxText)},
	} {
		t.Run(test.name, func(t *testing.T) {
			censor := secrets.NewDynamicCensor()
			if test.secret != "" {
				censor.AddSecrets(test.secret)
			}
			got := censorStartupText(test.value, &censor)
			if test.secret != "" {
				if strings.Contains(got, "secret") || strings.Contains(got, "quoted") {
					t.Fatalf("secret leaked: %q", got)
				}
				return
			}
			if got != test.want {
				t.Errorf("want %q, got %q", test.want, got)
			}
		})
	}
}

// TestStartupAlias checks local ordinal reuse without deriving aliases from names.
func TestStartupAlias(t *testing.T) {
	for _, test := range []struct{ name string }{{name: "internal-worker.example.invalid"}, {name: ""}} {
		t.Run(test.name, func(t *testing.T) {
			aliases := map[string]string{}
			want := "node-1"
			if test.name == "" {
				want = ""
			}
			for iteration := 0; iteration < 2; iteration++ {
				if got := startupAlias(test.name, "node", aliases); got != want {
					t.Errorf("want %q, got %q", want, got)
				}
			}
		})
	}
}

// TestMarshalStartupDiagnostics checks unconfigured-secret privacy, association and caps.
func TestMarshalStartupDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name      string
		oversized bool
		noSecrets bool
	}{{name: "censor before encode"}, {name: "bounded output", oversized: true}, {name: "private hostnames without secrets", noSecrets: true}} {
		t.Run(test.name, func(t *testing.T) {
			secret := "private\n\"quoted\""
			censor := secrets.NewDynamicCensor()
			censor.AddSecrets(secret)
			if test.noSecrets {
				secret = "internal-worker.private.example.invalid"
				censor = secrets.NewDynamicCensor()
			}
			result := startupDiagnostics{Pods: []startupPod{{Name: secret, Node: secret}}, Nodes: []startupNode{{Name: secret}}}
			if !test.noSecrets {
				censor.AddSecrets("node-1", "pod-1")
			}
			if test.oversized {
				result.Nodes[0].Events = make([]startupWarning, startupMaxBytes)
			}
			data, err := marshalStartupDiagnostics(result, &censor)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(data) || len(data) > startupMaxBytes || strings.Contains(string(data), "private") || strings.Contains(string(data), "quoted") {
				t.Fatalf("unsafe output: bytes=%d valid=%v", len(data), json.Valid(data))
			}
			var decoded startupDiagnostics
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Truncated != test.oversized {
				t.Errorf("unexpected truncation: %v", decoded.Truncated)
			}
			if test.noSecrets {
				for _, forbidden := range []string{secret, base64.StdEncoding.EncodeToString([]byte(secret)), base64.RawURLEncoding.EncodeToString([]byte(secret)), hex.EncodeToString([]byte(secret)), url.QueryEscape(secret)} {
					if strings.Contains(string(data), forbidden) {
						t.Fatal("raw or reversibly encoded hostname escaped")
					}
				}
				if decoded.Pods[0].Name != "pod-1" || decoded.Pods[0].Node != "node-1" || decoded.Nodes[0].Name != decoded.Pods[0].Node || result.Pods[0].Name != secret {
					t.Fatal("aliases lost association or mutated the source snapshot")
				}
			} else if strings.Contains(string(data), "node-1") || strings.Contains(string(data), "pod-1") {
				t.Fatal("alias bypassed censoring before encoding")
			}
		})
	}
}

func TestSaveStartupDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name    string
		blocked bool
	}{{name: "writes artifact"}, {name: "write failure", blocked: true}} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if test.blocked {
				dir = filepath.Join(dir, "file")
				if err := os.WriteFile(dir, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("ARTIFACTS", dir)
			censor := secrets.NewDynamicCensor()
			err := saveStartupDiagnostics(startupDiagnostics{Status: "no_relevant_nodes"}, &censor)
			if (err != nil) != test.blocked {
				t.Fatalf("unexpected write error: %v", err)
			}
			if err != nil && err.Error() != "writing node startup diagnostics: unavailable" {
				t.Fatalf("unsafe or missing save context: %v", err)
			}
			if !test.blocked {
				data, err := os.ReadFile(filepath.Join(dir, api.NamespaceDir, "node-startup.json"))
				if err != nil {
					t.Fatal(err)
				}
				if !json.Valid(data) {
					t.Error("invalid JSON artifact")
				}
			}
		})
	}
}

// startupCancelTransport checks request deadlines and cancels an in-flight read.
type startupCancelTransport struct {
	cancel    context.CancelFunc
	bounded   bool
	timeout   bool
	eventOnly bool
	requests  int
}

// RoundTrip simulates a canceled read without sleeps, credentials, or a server.
func (transport *startupCancelTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.requests++
	deadline, ok := request.Context().Deadline()
	transport.bounded = ok && time.Until(deadline) <= startupRequestTimeout && time.Until(deadline) > 0
	if transport.eventOnly && request.URL.Path == "/api/v1/nodes/worker" {
		return (&startupArtifactTransport{}).RoundTrip(request)
	}
	if !transport.timeout {
		transport.cancel()
	}
	<-request.Context().Done()
	return nil, request.Context().Err()
}

// startupArtifactTransport returns only synthetic public-shaped namespace data.
type startupArtifactTransport struct {
	denied              bool
	bounded             bool
	eventDenied         bool
	eventTimeout        bool
	nodeTimeout         bool
	artifactDir         string
	snapshotsBeforeNode bool
	legacyDeadline      bool
	legacyTimeout       time.Duration
	nodeBounded         bool
}

// startupGraphFailure supplies the original failure without executing a test step.
type startupGraphFailure struct{ err error }

// run returns the exact execution error to the lifecycle handler.
func (failure startupGraphFailure) run() []error { return []error{failure.err} }

// RoundTrip supplies deterministic artifact snapshots and refuses other reads.
func (transport *startupArtifactTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	deadline, bounded := request.Context().Deadline()
	transport.bounded = transport.bounded || bounded
	switch request.URL.Path {
	case "/api/v1/namespaces/run/pods", "/api/v1/namespaces/run/events", "/apis/build.openshift.io/v1/namespaces/run/builds", "/apis/image.openshift.io/v1/namespaces/run/imagestreams":
		if transport.legacyTimeout == 0 {
			transport.legacyDeadline = transport.legacyDeadline || bounded
		} else if !bounded || time.Until(deadline) <= startupRequestTimeout || time.Until(deadline) > transport.legacyTimeout {
			transport.legacyDeadline = true
		}
	}
	if transport.eventTimeout && request.URL.Path == "/api/v1/namespaces/run/events" {
		return nil, context.DeadlineExceeded
	}
	status := http.StatusOK
	var object any = &coreapi.EventList{}
	switch request.URL.Path {
	case "/api/v1/namespaces/run/pods":
		object = &coreapi.PodList{Items: []coreapi.Pod{startupTestPod("test", "worker")}}
	case "/api/v1/nodes/worker":
		transport.nodeBounded = bounded && time.Until(deadline) <= startupRequestTimeout
		transport.snapshotsBeforeNode = true
		for _, name := range []string{"pods.json", "events.json", "builds.json", "imagestreams.json"} {
			if _, err := os.Stat(filepath.Join(transport.artifactDir, api.NamespaceDir, name)); err != nil {
				transport.snapshotsBeforeNode = false
			}
		}
		if transport.nodeTimeout {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}
		object = startupTestNode()
	case "/api/v1/namespaces/run/events":
	case "/apis/build.openshift.io/v1/namespaces/run/builds":
		object = &metav1.List{TypeMeta: metav1.TypeMeta{APIVersion: "build.openshift.io/v1", Kind: "BuildList"}}
	case "/api":
		object = &metav1.APIVersions{Versions: []string{"v1"}}
	case "/apis":
		object = &metav1.APIGroupList{Groups: []metav1.APIGroup{{Name: "image.openshift.io", Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "image.openshift.io/v1", Version: "v1"}}, PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: "image.openshift.io/v1", Version: "v1"}}}}
	case "/apis/image.openshift.io/v1":
		object = &metav1.APIResourceList{GroupVersion: "image.openshift.io/v1", APIResources: []metav1.APIResource{{Name: "imagestreams", Kind: "ImageStream", Namespaced: true}}}
	case "/apis/image.openshift.io/v1/namespaces/run/imagestreams":
		object = &metav1.List{TypeMeta: metav1.TypeMeta{APIVersion: "image.openshift.io/v1", Kind: "ImageStreamList"}}
	case "/api/v1/events":
		object = &coreapi.EventList{Items: []coreapi.Event{startupTestEvent()}}
	default:
		status = http.StatusServiceUnavailable
		object = &metav1.Status{Status: "Failure", Reason: metav1.StatusReasonServiceUnavailable, Code: int32(status)}
	}
	if transport.denied || (transport.eventDenied && request.URL.Path == "/api/v1/namespaces/run/events") {
		status = http.StatusForbidden
		object = &metav1.Status{Status: "Failure", Reason: metav1.StatusReasonForbidden, Message: "never-emit", Code: int32(status)}
	}
	data, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
}

// TestSaveNamespaceArtifacts verifies legacy snapshots precede bounded diagnostics and retain their request configuration and original execution outcome.
func TestSaveNamespaceArtifacts(t *testing.T) {
	for _, test := range []struct {
		name                               string
		denied                             bool
		want                               string
		eventDenied, eventTimeout, blocked bool
		nodeTimeout                        bool
		originalTimeout                    time.Duration
	}{
		{name: "collects after execution", want: "collected"},
		{name: "records denied namespace read", denied: true, want: "forbidden"},
		{name: "namespace events forbidden", eventDenied: true, want: "collected"},
		{name: "namespace events timeout", eventTimeout: true, want: "collected"},
		{name: "artifact write failure preserves result", blocked: true},
		{name: "slow node cannot starve legacy snapshots", nodeTimeout: true, want: "collected"},
		{name: "retains configured legacy timeout", originalTimeout: 7 * time.Second, want: "collected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if test.blocked {
				dir = filepath.Join(dir, "file")
				if err := os.WriteFile(dir, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("ARTIFACTS", dir)
			censor := secrets.NewDynamicCensor()
			transport := &startupArtifactTransport{denied: test.denied, eventDenied: test.eventDenied, eventTimeout: test.eventTimeout, nodeTimeout: test.nodeTimeout, artifactDir: dir, legacyTimeout: test.originalTimeout}
			options := options{clusterConfig: &rest.Config{Host: "https://fixture.invalid", Transport: transport, Timeout: test.originalTimeout}, namespace: "run", censor: &censor, jobSpec: &api.JobSpec{JobSpec: downwardapi.JobSpec{ProwJobID: "run-id"}}}
			original := errors.New("synthetic graph/release-import failure")
			failure := startupGraphFailure{err: original}
			returned := interrupt.New(nil, options.saveNamespaceArtifacts).Run(failure.run)
			if len(returned) != 1 || returned[0] != original {
				t.Fatalf("diagnostics changed original result: %v", returned)
			}
			if test.blocked {
				return
			}
			if transport.legacyDeadline || options.clusterConfig.Timeout != test.originalTimeout {
				t.Fatal("changed legacy request deadline or client configuration")
			}
			if !test.denied && (!transport.snapshotsBeforeNode || !transport.nodeBounded) {
				t.Fatal("node collection ran before all legacy snapshots or without its deadline")
			}
			data, err := os.ReadFile(filepath.Join(dir, api.NamespaceDir, "node-startup.json"))
			if err != nil {
				t.Fatal(err)
			}
			var result startupDiagnostics
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != test.want || (!test.denied && !transport.bounded) || strings.Contains(string(data), "never-emit") {
				t.Errorf("unexpected artifact: %s, bounded=%v", data, transport.bounded)
			}
			if test.nodeTimeout && result.Nodes[0].Status != "timeout" {
				t.Fatal("node timeout was not classified")
			}
			if test.eventDenied && result.PodEventsStatus != "forbidden" {
				t.Errorf("unexpected pod event status: %s", result.PodEventsStatus)
			}
			if test.eventTimeout && result.PodEventsStatus != "timeout" {
				t.Errorf("unexpected pod event status: %s", result.PodEventsStatus)
			}
		})
	}
}

// startupNilCore supplies nil successful node or event responses for defensive checks.
type startupNilCore struct {
	coreclientset.CoreV1Interface
	nilNode bool
}

// Nodes retains normal node reads unless a nil response is requested.
func (client startupNilCore) Nodes() coreclientset.NodeInterface {
	if client.nilNode {
		return startupNilNodes{}
	}
	return client.CoreV1Interface.Nodes()
}

// Events supplies the nil list response used by the defensive test case.
func (client startupNilCore) Events(string) coreclientset.EventInterface { return startupNilEvents{} }

// startupNilNodes embeds unused operations and overrides only Get.
type startupNilNodes struct{ coreclientset.NodeInterface }

// Get returns an unavailable object without an API error.
func (startupNilNodes) Get(context.Context, string, metav1.GetOptions) (*coreapi.Node, error) {
	return nil, nil
}

// startupNilEvents embeds unused operations and overrides only List.
type startupNilEvents struct{ coreclientset.EventInterface }

// List returns an unavailable snapshot without an API error.
func (startupNilEvents) List(context.Context, metav1.ListOptions) (*coreapi.EventList, error) {
	return nil, nil
}
