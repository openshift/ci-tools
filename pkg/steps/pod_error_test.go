package steps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/kubernetes"
)

type podStepTypedNilCause struct{}

func (*podStepTypedNilCause) Error() string { panic("typed nil cause must not be formatted") }

func TestPodStepErrorContract(t *testing.T) {
	cause := errors.New("pod failed")
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "failed-pod"}}
	err := fmt.Errorf("outer context: %w", &PodStepError{Pod: pod, err: cause})

	var podStepErr *PodStepError
	if !errors.As(err, &podStepErr) {
		t.Fatalf("expected errors.As to find PodStepError in %v", err)
	}
	if podStepErr.Pod != pod {
		t.Fatalf("expected observed pod to be retained, got %#v", podStepErr.Pod)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("expected errors.Is to find wrapped cause in %v", err)
	}
}

func TestPodStepErrorNilSafety(t *testing.T) {
	var nilWrapper *PodStepError
	var typedNilCause *podStepTypedNilCause
	for _, testCase := range []struct {
		name string
		err  *PodStepError
	}{
		{name: "nil receiver", err: nilWrapper},
		{name: "nil cause", err: &PodStepError{}},
		{name: "typed nil cause", err: &PodStepError{err: typedNilCause}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.err.Error(); got != "pod step failed" {
				t.Fatalf("Error() = %q, want %q", got, "pod step failed")
			}
			if got := testCase.err.Unwrap(); got != nil {
				t.Fatalf("Unwrap() = %v, want nil", got)
			}
		})
	}
}

func TestPodCleanupErrorLogIsSanitized(t *testing.T) {
	const (
		internalHost = "api.internal.example.test"
		podName      = "release-images-latest"
		stepName     = "release"
	)
	transportErr := fmt.Errorf(`Delete "https://%s:6443/api/v1/namespaces/ci/pods/%s": %w`, internalHost, podName, syscall.ECONNREFUSED)

	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	originalOutput, originalFormatter := logger.Out, logger.Formatter
	logger.SetOutput(&logs)
	logger.SetFormatter(&logrus.TextFormatter{DisableColors: true, DisableTimestamp: true})
	defer func() {
		logger.SetOutput(originalOutput)
		logger.SetFormatter(originalFormatter)
	}()

	logPodCleanupError(stepName, podName, transportErr)
	output := logs.String()
	for _, sensitive := range []string{internalHost, "https://", "/api/v1/"} {
		if strings.Contains(output, sensitive) {
			t.Fatalf("cleanup log contains sensitive transport detail %q: %s", sensitive, output)
		}
	}
	for _, safe := range []string{"error_class=connection_refused", "pod=" + podName, "step=" + stepName} {
		if !strings.Contains(output, safe) {
			t.Fatalf("cleanup log is missing safe field %q: %s", safe, output)
		}
	}
}

func TestPodCleanupErrorClass(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		want string
	}{
		{name: "connection refused", err: syscall.ECONNREFUSED, want: "connection_refused"},
		{name: "HTTP2 connection lost", err: errors.New("http2: client connection lost"), want: "http2_connection_lost"},
		{name: "API timeout", err: context.DeadlineExceeded, want: "api_timeout"},
		{name: "unknown", err: errors.New("cleanup failed"), want: "unknown"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := podCleanupErrorClass(testCase.err); got != testCase.want {
				t.Fatalf("podCleanupErrorClass() = %q, want %q", got, testCase.want)
			}
		})
	}
}

type podDeleteAttempt struct {
	preconditionUID types.UID
	err             error
}

type lostCreateResponsePodClient struct {
	kubernetes.PodClient
	commitCreate bool
	createErr    error
	createdUID   types.UID
	deleteCalls  chan podDeleteAttempt
}

func (c *lostCreateResponsePodClient) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok || !c.commitCreate {
		return c.createErr
	}
	stored := pod.DeepCopy()
	stored.UID = c.createdUID
	if err := c.PodClient.Create(ctx, stored, opts...); err != nil {
		return err
	}
	return c.createErr
}

func (c *lostCreateResponsePodClient) Delete(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteOption) error {
	deleteOptions := &ctrlruntimeclient.DeleteOptions{}
	deleteOptions.ApplyOptions(opts)
	var preconditionUID types.UID
	if deleteOptions.Preconditions != nil && deleteOptions.Preconditions.UID != nil {
		preconditionUID = *deleteOptions.Preconditions.UID
	}

	current := &corev1.Pod{}
	err := c.PodClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(obj), current)
	if err == nil && current.UID != preconditionUID {
		err = kerrors.NewConflict(corev1.Resource("pods"), obj.GetName(), errors.New("UID precondition mismatch"))
	} else if err == nil {
		err = c.PodClient.Delete(ctx, current, opts...)
	}
	c.deleteCalls <- podDeleteAttempt{preconditionUID: preconditionUID, err: err}
	return err
}

func newLostCreateResponsePodStep(namespace string, commitCreate bool, createErr error) (*podStep, *lostCreateResponsePodClient) {
	step, _ := preparePodStep(namespace)
	client := &lostCreateResponsePodClient{
		PodClient:    step.client,
		commitCreate: commitCreate,
		createErr:    createErr,
		createdUID:   types.UID("created-uid"),
		deleteCalls:  make(chan podDeleteAttempt, 1),
	}
	step.client = client
	return step, client
}

func waitForPodDeleteAttempt(t *testing.T, client *lostCreateResponsePodClient) podDeleteAttempt {
	t.Helper()
	select {
	case attempt := <-client.deleteCalls:
		return attempt
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pod cancellation cleanup")
		return podDeleteAttempt{}
	}
}

func TestPodStepCleansUpReconciledLostCreateResponse(t *testing.T) {
	const namespace = "test-namespace"
	ctx, cancel := context.WithCancel(context.Background())
	step, client := newLostCreateResponsePodStep(namespace, true, syscall.ECONNRESET)

	err := step.run(ctx)
	if !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("expected lost create response, got %v", err)
	}
	created := &corev1.Pod{}
	key := ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: step.config.As}
	if err := client.Get(context.Background(), key, created); err != nil || created.UID != client.createdUID {
		t.Fatalf("expected committed pod before cancellation, pod=%#v err=%v", created, err)
	}

	cancel()
	attempt := waitForPodDeleteAttempt(t, client)
	if attempt.err != nil {
		t.Fatalf("recovered pod cleanup failed: %v", attempt.err)
	}
	if attempt.preconditionUID != client.createdUID {
		t.Fatalf("cleanup UID precondition = %q, want %q", attempt.preconditionUID, client.createdUID)
	}
	if err := client.Get(context.Background(), key, &corev1.Pod{}); !kerrors.IsNotFound(err) {
		t.Fatalf("committed pod was orphaned after cancellation: %v", err)
	}
}

func TestPodStepLostCreateCleanupPreservesReplacement(t *testing.T) {
	const namespace = "test-namespace"
	ctx, cancel := context.WithCancel(context.Background())
	step, client := newLostCreateResponsePodStep(namespace, true, syscall.ECONNRESET)
	if err := step.run(ctx); !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("expected lost create response, got %v", err)
	}

	key := ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: step.config.As}
	original := &corev1.Pod{}
	if err := client.PodClient.Get(context.Background(), key, original); err != nil {
		t.Fatalf("get original pod: %v", err)
	}
	if err := client.PodClient.Delete(context.Background(), original); err != nil {
		t.Fatalf("delete original pod while arranging replacement: %v", err)
	}
	replacement := original.DeepCopy()
	replacement.ResourceVersion = ""
	replacement.UID = types.UID("replacement-uid")
	if err := client.PodClient.Create(context.Background(), replacement); err != nil {
		t.Fatalf("create replacement pod: %v", err)
	}

	cancel()
	attempt := waitForPodDeleteAttempt(t, client)
	if !kerrors.IsConflict(attempt.err) {
		t.Fatalf("expected stale UID precondition conflict, got %v", attempt.err)
	}
	if attempt.preconditionUID != client.createdUID {
		t.Fatalf("cleanup UID precondition = %q, want original %q", attempt.preconditionUID, client.createdUID)
	}
	current := &corev1.Pod{}
	if err := client.Get(context.Background(), key, current); err != nil || current.UID != replacement.UID {
		t.Fatalf("replacement was affected by stale cleanup, pod=%#v err=%v", current, err)
	}
}

func TestPodStepPermanentCreateFailureDoesNotRegisterCleanup(t *testing.T) {
	const namespace = "test-namespace"
	ctx, cancel := context.WithCancel(context.Background())
	badRequest := kerrors.NewBadRequest("invalid pod")
	step, client := newLostCreateResponsePodStep(namespace, false, badRequest)
	if err := step.run(ctx); !kerrors.IsBadRequest(err) {
		t.Fatalf("expected permanent create failure, got %v", err)
	}
	cancel()

	select {
	case attempt := <-client.deleteCalls:
		t.Fatalf("permanent create failure unexpectedly registered cleanup: %#v", attempt)
	default:
	}
	key := ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: step.config.As}
	if err := client.Get(context.Background(), key, &corev1.Pod{}); !kerrors.IsNotFound(err) {
		t.Fatalf("permanent create failure left a pod behind: %v", err)
	}
}
