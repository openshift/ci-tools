package release

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
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
	if !strings.Contains(logs.String(), "Release extraction pod release-images-latest failed, retrying") {
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

func TestRunReleaseExtractionAttemptTimeoutIsRetryablePerAttempt(t *testing.T) {
	var attempts int
	err := retryReleaseExtraction(context.Background(), "release-images-latest", []time.Duration{0}, func(context.Context, time.Duration) error { return nil }, func(ctx context.Context) error {
		attempts++
		return runReleaseExtractionAttempt(ctx, 0, func(attemptCtx context.Context) error {
			<-attemptCtx.Done()
			return attemptCtx.Err()
		})
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected attempt deadline error, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected a fresh timeout for both attempts, got %d attempts", attempts)
	}
}

func TestTransientReleaseExtractionErrorPattern(t *testing.T) {
	pattern := regexp.MustCompile("(?i)(" + transientReleaseExtractionErrorPattern + ")")
	testCases := []struct {
		name      string
		output    string
		transient bool
	}{
		{name: "too many requests", output: "error: too many requests", transient: true},
		{name: "server status", output: "registry returned status code 503", transient: true},
		{name: "connection reset", output: "read: connection reset by peer", transient: true},
		{name: "timeout", output: "TLS handshake timeout", transient: true},
		{name: "forbidden", output: "forbidden: access denied"},
		{name: "invalid reference", output: "invalid reference format"},
		{name: "malformed payload", output: "image-references is malformed"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := pattern.MatchString(testCase.output); got != testCase.transient {
				t.Fatalf("classification = %t, want %t for %q", got, testCase.transient, testCase.output)
			}
		})
	}
}

type transientThenSuccessfulPodClient struct {
	*testhelperkube.FakePodClient
}

func (c *transientThenSuccessfulPodClient) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.GetOption) error {
	return c.FakePodExecutor.LoggingClient.Get(ctx, key, obj, opts...)
}

func TestRetryReleaseExtractionRecreatesTransientFailedPod(t *testing.T) {
	const (
		namespace = "test-namespace"
		podName   = "release-images-latest"
	)
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core API to scheme: %v", err)
	}
	loggingClient := loggingclient.New(fakectrlruntimeclient.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&corev1.Pod{}, "metadata.name", func(obj ctrlruntimeclient.Object) []string { return []string{obj.GetName()} }).
		Build(), nil)
	client := &transientThenSuccessfulPodClient{FakePodClient: &testhelperkube.FakePodClient{
		FakePodExecutor: &testhelperkube.FakePodExecutor{LoggingClient: loggingClient, AutoSchedule: true},
		PendingTimeout:  time.Minute,
	}}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: podName},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers:    []corev1.Container{{Name: releaseExtractionContainerName, Image: "stable:cli"}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts int
	err := retryReleaseExtraction(ctx, podName, []time.Duration{0}, func(context.Context, time.Duration) error { return nil }, func(ctx context.Context) error {
		attempts++
		created, err := ciutil.CreateOrRestartPod(ctx, client, pod.DeepCopy())
		if err != nil {
			return err
		}
		if attempts > 1 {
			return nil
		}
		created.Status.Phase = corev1.PodFailed
		created.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name: releaseExtractionContainerName,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				ExitCode: transientReleaseExtractionExitCode,
			}},
		}}
		if err := client.FakePodExecutor.LoggingClient.Status().Update(ctx, created); err != nil {
			return err
		}
		return transientReleaseExtractionPodError(ctx, client, namespace, podName, errors.New("transient registry outage"))
	})
	if err != nil {
		t.Fatalf("expected recreated extraction pod to succeed: %v", err)
	}
	if len(client.CreatedPods) != 2 {
		t.Fatalf("expected two real pod creations, got %d", len(client.CreatedPods))
	}
	if len(client.DeletedPods) != 1 || client.DeletedPods[0].Status.Phase != corev1.PodFailed {
		t.Fatalf("expected the transient failed pod to be deleted once, got %#v", client.DeletedPods)
	}
}

func TestTransientReleaseExtractionPodErrorClassification(t *testing.T) {
	testCases := []struct {
		name      string
		phase     corev1.PodPhase
		exitCode  int32
		transient bool
	}{
		{name: "explicit transient exit", phase: corev1.PodFailed, exitCode: transientReleaseExtractionExitCode, transient: true},
		{name: "permanent container failure", phase: corev1.PodFailed, exitCode: 1},
		{name: "pending timeout", phase: corev1.PodPending, exitCode: transientReleaseExtractionExitCode},
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
			client := fakectrlruntimeclient.NewClientBuilder().WithObjects(pod).Build()
			original := errors.New("pod failed")
			err := transientReleaseExtractionPodError(context.Background(), client, "ns", "extract", original)
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
