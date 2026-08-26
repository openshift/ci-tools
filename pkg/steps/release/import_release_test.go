package release

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	utilpointer "k8s.io/utils/pointer"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	testhelperkube "github.com/openshift/ci-tools/pkg/testhelper/kubernetes"
	ciutil "github.com/openshift/ci-tools/pkg/util"
)

const testCLIImage = "quay.io/test/cli:latest"

func deterministicReleaseImportRetryDelays() []time.Duration {
	return []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		32 * time.Second,
		64 * time.Second,
		128 * time.Second,
	}
}

func TestRetryReleaseExtractionRecoversAfterVirtualTwoMinuteOutage(t *testing.T) {
	retryDelays := deterministicReleaseImportRetryDelays()
	var elapsed time.Duration
	var attempts int
	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	originalOutput := logger.Out
	logger.SetOutput(&logs)
	defer logger.SetOutput(originalOutput)

	err := retryReleaseExtraction(context.Background(), "release-images-latest", retryDelays, func(_ context.Context, delay time.Duration) error {
		elapsed += delay
		return nil
	}, func(context.Context) error {
		attempts++
		if elapsed < 2*time.Minute {
			return &transientReleaseExtractionError{err: errors.New("registry unavailable")}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected extraction to recover: %v", err)
	}
	if attempts != 8 {
		t.Fatalf("expected 8 extraction attempts, got %d", attempts)
	}
	if elapsed != 127*time.Second {
		t.Fatalf("expected virtual recovery after 127s, got %s", elapsed)
	}
	if !strings.Contains(logs.String(), "Release extraction failed, retrying") || strings.Contains(logs.String(), "release-images-latest") {
		t.Fatalf("expected retry log evidence, got %q", logs.String())
	}
	if !strings.Contains(logs.String(), "Release extraction recovered after retry") || !strings.Contains(logs.String(), "attempts=8") {
		t.Fatalf("expected recovery log with attempt count, got %q", logs.String())
	}
}

func TestRetryReleaseExtractionIsBoundedAndContextAware(t *testing.T) {
	t.Run("bounded", func(t *testing.T) {
		var elapsed time.Duration
		var attempts int
		var logs bytes.Buffer
		logger := logrus.StandardLogger()
		originalOutput := logger.Out
		logger.SetOutput(&logs)
		defer logger.SetOutput(originalOutput)
		expectedErr := errors.New("extract failed")
		err := retryReleaseExtraction(context.Background(), "release-images-latest", deterministicReleaseImportRetryDelays(), func(_ context.Context, delay time.Duration) error {
			elapsed += delay
			return nil
		}, func(context.Context) error {
			attempts++
			return &transientReleaseExtractionError{err: expectedErr}
		})
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected final extraction error, got %v", err)
		}
		if attempts != 9 {
			t.Fatalf("expected 9 bounded attempts, got %d", attempts)
		}
		if elapsed != 255*time.Second {
			t.Fatalf("expected a 255s retry budget, got %s", elapsed)
		}
		if !strings.Contains(logs.String(), "Release extraction retry attempts exhausted") ||
			!strings.Contains(logs.String(), "attempts=9") ||
			!strings.Contains(logs.String(), "retry_budget=4m15s") ||
			!strings.Contains(err.Error(), "retry budget 4m15s") {
			t.Fatalf("expected terminal exhaustion log with attempt count, got %q", logs.String())
		}
	})

	t.Run("permanent failure", func(t *testing.T) {
		var attempts int
		expectedErr := errors.New("malformed release payload")
		err := retryReleaseExtraction(context.Background(), "release-images-latest", deterministicReleaseImportRetryDelays(), func(context.Context, time.Duration) error {
			t.Fatal("permanent failure must not sleep")
			return nil
		}, func(context.Context) error {
			attempts++
			return expectedErr
		})
		if !errors.Is(err, expectedErr) || attempts != 1 {
			t.Fatalf("expected one permanent attempt, got err=%v attempts=%d", err, attempts)
		}
	})

	t.Run("pre-canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var attempts int
		err := retryReleaseExtraction(ctx, "release-images-latest", deterministicReleaseImportRetryDelays(), sleepForReleaseImportRetry, func(context.Context) error {
			attempts++
			return nil
		})
		if !errors.Is(err, context.Canceled) || attempts != 0 || !strings.Contains(err.Error(), "release extraction pod") {
			t.Fatalf("expected contextual pre-cancellation, got err=%v attempts=%d", err, attempts)
		}
	})

	t.Run("cancellation during backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var attempts int
		err := retryReleaseExtraction(ctx, "release-images-latest", deterministicReleaseImportRetryDelays(), func(ctx context.Context, _ time.Duration) error {
			cancel()
			return ctx.Err()
		}, func(context.Context) error {
			attempts++
			return &transientReleaseExtractionError{err: errors.New("extract failed")}
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
		if attempts != 1 {
			t.Fatalf("expected one attempt before cancellation, got %d", attempts)
		}
	})

	t.Run("cancellation during final attempt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var attempts int
		err := retryReleaseExtraction(ctx, "release-images-latest", []time.Duration{0}, func(context.Context, time.Duration) error { return nil }, func(context.Context) error {
			attempts++
			if attempts == 2 {
				cancel()
			}
			return &transientReleaseExtractionError{err: errors.New("extract failed")}
		})
		if !errors.Is(err, context.Canceled) || attempts != 2 {
			t.Fatalf("expected final-attempt cancellation, got err=%v attempts=%d", err, attempts)
		}
	})

	t.Run("attempt retains parent deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		err := retryReleaseExtraction(ctx, "release-images-latest", nil, sleepForReleaseImportRetry, func(attemptCtx context.Context) error {
			deadline, ok := attemptCtx.Deadline()
			if !ok || time.Until(deadline) < 30*time.Minute {
				return fmt.Errorf("attempt context was unexpectedly capped: deadline=%v present=%t", deadline, ok)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("expected parent deadline to govern the attempt: %v", err)
		}
	})

	t.Run("expired deadline", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		var attempts int
		err := retryReleaseExtraction(ctx, "release-images-latest", deterministicReleaseImportRetryDelays(), sleepForReleaseImportRetry, func(context.Context) error {
			attempts++
			return nil
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline error, got %v", err)
		}
		if attempts != 0 {
			t.Fatalf("expected no attempt after deadline, got %d", attempts)
		}
	})
}

func TestTransientReleaseExtractionErrorPattern(t *testing.T) {
	testCases := []struct {
		name      string
		output    string
		transient bool
	}{
		{name: "too many requests", output: "error: too many requests", transient: true},
		{name: "docker hub rate limit", output: "toomanyrequests: You have reached your pull rate limit", transient: true},
		{name: "server status", output: "registry returned status code 503", transient: true},
		{name: "canonical internal server error", output: "received unexpected HTTP status: 500 Internal Server Error", transient: true},
		{name: "canonical bad gateway", output: "received unexpected HTTP status: 502 Bad Gateway", transient: true},
		{name: "connection reset", output: "read: connection reset by peer", transient: true},
		{name: "timeout", output: "TLS handshake timeout", transient: true},
		{name: "unauthorized", output: "unauthorized: authentication required"},
		{name: "forbidden", output: "forbidden: access denied"},
		{name: "manifest unknown", output: "manifest unknown: manifest unknown"},
		{name: "invalid reference", output: "invalid reference format"},
		{name: "malformed payload", output: "image-references is malformed"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command("grep", "-Eqi", "("+transientReleaseExtractionErrorPattern+")")
			command.Stdin = strings.NewReader(testCase.output)
			got := command.Run() == nil
			if got != testCase.transient {
				t.Fatalf("classification = %t, want %t for %q", got, testCase.transient, testCase.output)
			}
		})
	}
}

type releasePodLifecycleClient struct {
	*testhelperkube.FakePodClient
	lock             sync.Mutex
	statuses         []corev1.PodStatus
	createCount      int
	deletedUIDs      []types.UID
	staleDeleteCount int
	deleteErr        error
	deleteCalls      chan struct{}
}

func (c *releasePodLifecycleClient) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return c.FakePodExecutor.LoggingClient.Create(ctx, obj, opts...)
	}
	c.lock.Lock()
	c.createCount++
	attempt := c.createCount
	pod.UID = types.UID(fmt.Sprintf("release-pod-%d", attempt))
	pod.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Minute))
	pod.Status = c.statuses[attempt-1]
	c.CreatedPods = append(c.CreatedPods, pod.DeepCopy())
	c.lock.Unlock()
	return c.FakePodExecutor.LoggingClient.Create(ctx, pod, opts...)
}

func (c *releasePodLifecycleClient) Watch(ctx context.Context, list ctrlruntimeclient.ObjectList, _ ...ctrlruntimeclient.ListOption) (watch.Interface, error) {
	if err := c.FakePodExecutor.LoggingClient.List(ctx, list); err != nil {
		return nil, err
	}
	items := list.(*corev1.PodList).Items
	ch := make(chan watch.Event, len(items))
	for i := range items {
		ch <- watch.Event{Type: watch.Modified, Object: items[i].DeepCopy()}
	}
	return watch.NewProxyWatcher(ch), nil
}

func (c *releasePodLifecycleClient) Delete(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteOption) error {
	c.deleteCalls <- struct{}{}
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return c.FakePodExecutor.LoggingClient.Delete(ctx, obj, opts...)
	}
	c.lock.Lock()
	deleteErr := c.deleteErr
	c.lock.Unlock()
	if deleteErr != nil {
		return deleteErr
	}
	deleteOptions := &ctrlruntimeclient.DeleteOptions{}
	deleteOptions.ApplyOptions(opts)
	var expectedUID types.UID
	if deleteOptions.Preconditions != nil && deleteOptions.Preconditions.UID != nil {
		expectedUID = *deleteOptions.Preconditions.UID
	}
	current := &corev1.Pod{}
	if err := c.FakePodExecutor.LoggingClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(pod), current); err != nil {
		return err
	}
	if expectedUID == "" || current.UID != expectedUID {
		c.lock.Lock()
		c.staleDeleteCount++
		c.lock.Unlock()
		return kerrors.NewConflict(corev1.Resource("pods"), pod.Name, errors.New("UID precondition mismatch"))
	}
	c.lock.Lock()
	c.deletedUIDs = append(c.deletedUIDs, expectedUID)
	c.DeletedPods = append(c.DeletedPods, current.DeepCopy())
	c.lock.Unlock()
	return c.FakePodExecutor.LoggingClient.Delete(ctx, current, opts...)
}

func newReleasePodLifecycleClient(t *testing.T, statuses ...corev1.PodStatus) *releasePodLifecycleClient {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core API to scheme: %v", err)
	}
	loggingClient := loggingclient.New(fakectrlruntimeclient.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&corev1.Pod{}, "metadata.name", func(obj ctrlruntimeclient.Object) []string { return []string{obj.GetName()} }).
		WithIndex(&corev1.Event{}, "involvedObject.uid", func(obj ctrlruntimeclient.Object) []string {
			return []string{string(obj.(*corev1.Event).InvolvedObject.UID)}
		}).
		Build(), nil)
	return &releasePodLifecycleClient{
		FakePodClient: &testhelperkube.FakePodClient{
			FakePodExecutor: &testhelperkube.FakePodExecutor{LoggingClient: loggingClient},
			PendingTimeout:  0,
		},
		statuses:    statuses,
		deleteCalls: make(chan struct{}, 20),
	}
}

func waitForReleasePodDeleteCalls(t *testing.T, client *releasePodLifecycleClient, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		select {
		case <-client.deleteCalls:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for delete call %d of %d", i+1, count)
		}
	}
}

func releaseExtractionPodStep(client *releasePodLifecycleClient, namespace, podName string) api.Step {
	jobSpec := &api.JobSpec{JobSpec: downwardapi.JobSpec{
		Job:  "release-import-test",
		Type: prowapi.PresubmitJob,
		Refs: &prowapi.Refs{Org: "openshift", Repo: "ci-tools", BaseRef: "main", Pulls: []prowapi.Pull{{Number: 5376, SHA: "test-sha"}}},
		DecorationConfig: &prowapi.DecorationConfig{
			Timeout:     &prowapi.Duration{Duration: time.Hour},
			GracePeriod: &prowapi.Duration{Duration: time.Second},
			UtilityImages: &prowapi.UtilityImages{
				Sidecar:    "sidecar",
				Entrypoint: "entrypoint",
			},
			SkipCloning: utilpointer.Bool(true),
		},
	}}
	jobSpec.SetNamespace(namespace)
	return steps.PodStep(releaseExtractionContainerName, steps.PodStepConfiguration{
		WaitFlags: ciutil.SkipLogs,
		As:        podName,
		From:      api.ImageStreamTagReference{Name: api.PipelineImageStream, Tag: "cli"},
		Commands:  "oc adm release extract",
	}, nil, client, jobSpec, nil)
}

func transientRunningReleasePodStatus() corev1.PodStatus {
	return corev1.PodStatus{
		Phase: corev1.PodRunning,
		ContainerStatuses: []corev1.ContainerStatus{
			{Name: releaseExtractionContainerName, State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: transientReleaseExtractionExitCode}}},
			{Name: "sidecar", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
		},
	}
}

func TestReleaseExtractionUsesRealPodStepLifecycle(t *testing.T) {
	const (
		namespace = "test-namespace"
		podName   = "release-images-latest"
	)
	ctx, cancel := context.WithCancel(context.Background())
	client := newReleasePodLifecycleClient(t, transientRunningReleasePodStatus(), corev1.PodStatus{Phase: corev1.PodSucceeded})
	err := runReleaseExtractionWithRetries(ctx, podName, releaseExtractionPodStep(client, namespace, podName), client, []time.Duration{0}, func(context.Context, time.Duration) error { return nil })
	if err != nil {
		t.Fatalf("expected recreated extraction pod to succeed: %v", err)
	}
	if client.createCount != 2 {
		t.Fatalf("expected two PodStep pod creations, got %d", client.createCount)
	}
	if len(client.deletedUIDs) != 1 || client.deletedUIDs[0] != "release-pod-1" {
		t.Fatalf("expected UID-safe deletion of the transient pod, got %v", client.deletedUIDs)
	}
	replacement := &corev1.Pod{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: podName}, replacement); err != nil || replacement.UID != "release-pod-2" {
		t.Fatalf("expected recreated pod with new UID, got pod=%#v err=%v", replacement, err)
	}
	if err := ciutil.DeletePodWithUID(ctx, client, client.CreatedPods[0]); err != nil {
		t.Fatalf("stale cleanup should be harmless: %v", err)
	}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: podName}, replacement); err != nil || replacement.UID != "release-pod-2" || client.staleDeleteCount != 1 {
		t.Fatalf("stale cleanup affected replacement: pod=%#v err=%v stale_deletes=%d", replacement, err, client.staleDeleteCount)
	}
	cancel()
	waitForReleasePodDeleteCalls(t, client, 4)
}

func TestReleaseExtractionRealPodStepTransientExhaustion(t *testing.T) {
	client := newReleasePodLifecycleClient(t, transientRunningReleasePodStatus(), transientRunningReleasePodStatus())
	ctx, cancel := context.WithCancel(context.Background())
	err := runReleaseExtractionWithRetries(ctx, "extract", releaseExtractionPodStep(client, "ns", "extract"), client, []time.Duration{0}, func(context.Context, time.Duration) error { return nil })
	var transientErr *transientReleaseExtractionError
	if !errors.As(err, &transientErr) {
		t.Fatalf("expected typed transient exhaustion cause, got %v", err)
	}
	if client.createCount != 2 || len(client.deletedUIDs) != 2 {
		t.Fatalf("expected two real attempts and UID-safe deletions, got creates=%d deletes=%v", client.createCount, client.deletedUIDs)
	}
	cancel()
	waitForReleasePodDeleteCalls(t, client, 4)
}

func TestReleaseExtractionCleanupFailureStopsRetry(t *testing.T) {
	client := newReleasePodLifecycleClient(t, transientRunningReleasePodStatus())
	cleanupErr := errors.New("pod deletion failed")
	client.deleteErr = cleanupErr
	ctx, cancel := context.WithCancel(context.Background())
	err := runReleaseExtractionWithRetries(ctx, "extract", releaseExtractionPodStep(client, "ns", "extract"), client, []time.Duration{0}, func(context.Context, time.Duration) error {
		t.Fatal("cleanup failure must stop before retry backoff")
		return nil
	})
	if !errors.Is(err, cleanupErr) || !strings.Contains(err.Error(), "cannot retry") {
		t.Fatalf("expected permanent cleanup failure, got %v", err)
	}
	var transientErr *transientReleaseExtractionError
	if errors.As(err, &transientErr) {
		t.Fatalf("cleanup failure retained transient marker: %v", err)
	}
	if client.createCount != 1 {
		t.Fatalf("cleanup failure retried the PodStep, got %d creations", client.createCount)
	}
	cancel()
	waitForReleasePodDeleteCalls(t, client, 2)
}

func TestReleaseExtractionPendingImagePullIsNotRetried(t *testing.T) {
	pending := corev1.PodStatus{Phase: corev1.PodPending, ContainerStatuses: []corev1.ContainerStatus{{
		Name: releaseExtractionContainerName,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
			Reason:  "ImagePullBackOff",
			Message: "authentication required",
		}},
	}}}
	client := newReleasePodLifecycleClient(t, pending)
	ctx, cancel := context.WithCancel(context.Background())
	err := runReleaseExtractionWithRetries(ctx, "extract", releaseExtractionPodStep(client, "ns", "extract"), client, []time.Duration{0}, func(context.Context, time.Duration) error {
		t.Fatal("pending image-pull failure must not retry")
		return nil
	})
	var transientErr *transientReleaseExtractionError
	if err == nil || errors.As(err, &transientErr) || client.createCount != 1 {
		t.Fatalf("expected one permanent pending failure, got err=%v creates=%d", err, client.createCount)
	}
	cancel()
	waitForReleasePodDeleteCalls(t, client, 1)
}

func TestTransientReleaseExtractionPodErrorClassification(t *testing.T) {
	testCases := []struct {
		name      string
		phase     corev1.PodPhase
		exitCode  int32
		transient bool
	}{
		{name: "explicit transient exit", phase: corev1.PodFailed, exitCode: transientReleaseExtractionExitCode, transient: true},
		{name: "decorated running pod with transient release exit", phase: corev1.PodRunning, exitCode: transientReleaseExtractionExitCode, transient: true},
		{name: "permanent container failure", phase: corev1.PodFailed, exitCode: 1},
		{name: "successful pod", phase: corev1.PodSucceeded},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "extract"},
				Status: corev1.PodStatus{
					Phase: testCase.phase,
					ContainerStatuses: []corev1.ContainerStatus{{
						Name:  releaseExtractionContainerName,
						State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: testCase.exitCode}},
					}},
				},
			}
			original := errors.New("pod failed")
			err := transientReleaseExtractionPodError(pod, original)
			var transientErr *transientReleaseExtractionError
			if got := errors.As(err, &transientErr); got != testCase.transient {
				t.Fatalf("transient classification = %t, want %t (err=%v)", got, testCase.transient, err)
			}
			if !errors.Is(err, original) {
				t.Fatalf("classification must preserve original error, got %v", err)
			}
		})
	}
}

func TestResolveCLIImageFromStreamWaitsForSpecVisibility(t *testing.T) {
	const (
		namespace  = "test-namespace"
		streamName = "stable-initial"
	)
	client := newReleaseImportGuardClient(t, namespace, streamName,
		&imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: streamName}},
		&imagev1.ImageStreamTag{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: streamName + ":cli"},
			Tag: &imagev1.TagReference{From: &corev1.ObjectReference{
				Kind: "DockerImage",
				Name: testCLIImage,
			}},
		},
	)
	jobSpec := &api.JobSpec{}
	jobSpec.SetNamespace(namespace)
	step := &importReleaseStep{client: client, jobSpec: jobSpec}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := step.resolveCLIImageFromStream(ctx, streamName)
	if err != nil {
		t.Fatalf("resolve CLI image from stream: %v", err)
	}
	assertTestCLIReference(t, got)
	if client.imageStreamWatchCount != 1 {
		t.Fatalf("expected the shared import wait to use one ImageStream watch, got %d", client.imageStreamWatchCount)
	}
}

func TestExtractAndTagCLIImageWaitsForSpecVisibility(t *testing.T) {
	const (
		namespace  = "test-namespace"
		streamName = "stable"
		targetCLI  = "release-images-latest-cli"
	)
	client := newReleaseImportGuardClient(t, namespace, streamName,
		&imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: streamName}},
		&imagev1.ImageStreamTag{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: streamName + ":cli"}},
	)
	jobSpec := &api.JobSpec{}
	jobSpec.SetNamespace(namespace)
	step := &importReleaseStep{name: api.LatestReleaseName, client: client, jobSpec: jobSpec}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := step.extractAndTagCLIImage(ctx, targetCLI, streamName)
	if err != nil {
		t.Fatalf("extract and tag CLI image: %v", err)
	}
	assertTestCLIReference(t, got)
	if client.imageStreamWatchCount != 1 {
		t.Fatalf("expected the shared import wait to use one ImageStream watch, got %d", client.imageStreamWatchCount)
	}
	if len(client.CreatedPods) != 1 || client.CreatedPods[0].Name != targetCLI {
		t.Fatalf("expected CLI extractor pod %q to be created, got %#v", targetCLI, client.CreatedPods)
	}
}

func assertTestCLIReference(t *testing.T, ref *api.ImageStreamTagReference) {
	t.Helper()
	if ref == nil {
		t.Fatal("expected a CLI image reference, got nil")
	}
	if ref.Name != "quay.io/test/cli" || ref.Tag != "latest" {
		t.Fatalf("unexpected CLI image reference: %#v", ref)
	}
}

type releaseImportGuardClient struct {
	*testhelperkube.FakePodClient
	namespace             string
	streamName            string
	imageStreamWatchCount int
}

func (c *releaseImportGuardClient) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if pod, ok := obj.(*corev1.Pod); ok && len(pod.Spec.Containers) > 0 {
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name: pod.Spec.Containers[0].Name,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Message: testCLIImage,
			}},
		}}
	}
	return c.FakePodExecutor.Create(ctx, obj, opts...)
}

func (c *releaseImportGuardClient) Update(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.UpdateOption) error {
	if streamTag, ok := obj.(*imagev1.ImageStreamTag); ok && streamTag.ResourceVersion == "" {
		existing := &imagev1.ImageStreamTag{}
		if err := c.FakePodExecutor.LoggingClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(streamTag), existing); err != nil {
			return err
		}
		streamTag.ResourceVersion = existing.ResourceVersion
	}
	return c.FakePodExecutor.LoggingClient.Update(ctx, obj, opts...)
}

func (c *releaseImportGuardClient) Watch(ctx context.Context, list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) (watch.Interface, error) {
	if _, ok := list.(*corev1.PodList); ok {
		return c.FakePodExecutor.Watch(ctx, list, opts...)
	}
	if _, ok := list.(*imagev1.ImageStreamList); !ok {
		return c.FakePodExecutor.LoggingClient.Watch(ctx, list, opts...)
	}

	c.imageStreamWatchCount++
	stream := &imagev1.ImageStream{}
	key := ctrlruntimeclient.ObjectKey{Namespace: c.namespace, Name: c.streamName}
	if err := c.FakePodExecutor.LoggingClient.Get(ctx, key, stream); err != nil {
		return nil, err
	}
	stream.Spec.Tags = []imagev1.TagReference{{
		Name: "cli",
		From: &corev1.ObjectReference{Kind: "DockerImage", Name: testCLIImage},
	}}
	specOnly := stream.DeepCopy()
	stream.Status.Tags = []imagev1.NamedTagEventList{{
		Tag: "cli",
		Items: []imagev1.TagEvent{{
			DockerImageReference: "quay.io/test/cli@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
	}}
	if err := c.FakePodExecutor.LoggingClient.Update(ctx, stream); err != nil {
		return nil, err
	}
	// The first event exposes the spec tag; the second exposes import status.
	// Without the spec-aware shared evaluator, the initial empty list fails
	// before this watch can be established.
	events := make(chan watch.Event, 2)
	events <- watch.Event{Type: watch.Modified, Object: specOnly}
	events <- watch.Event{Type: watch.Modified, Object: stream.DeepCopy()}
	return watch.NewProxyWatcher(events), nil
}

func newReleaseImportGuardClient(t *testing.T, namespace, streamName string, objects ...runtime.Object) *releaseImportGuardClient {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core API to scheme: %v", err)
	}
	if err := imagev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add image API to scheme: %v", err)
	}
	loggingClient := loggingclient.New(fakectrlruntimeclient.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objects...).
		WithIndex(&corev1.Pod{}, "metadata.name", func(obj ctrlruntimeclient.Object) []string { return []string{obj.GetName()} }).
		Build(), nil)
	return &releaseImportGuardClient{
		FakePodClient: &testhelperkube.FakePodClient{
			FakePodExecutor: &testhelperkube.FakePodExecutor{
				LoggingClient: loggingClient,
				AutoSchedule:  true,
			},
			PendingTimeout: time.Minute,
		},
		namespace:  namespace,
		streamName: streamName,
	}
}
