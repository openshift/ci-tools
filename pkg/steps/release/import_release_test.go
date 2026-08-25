package release

import (
	"bytes"
	"context"
	"errors"
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
)

const testCLIImage = "quay.io/test/cli:latest"

func TestRetryReleaseExtractionRecoversAfterVirtualTwoMinuteOutage(t *testing.T) {
	retryDelays := releaseImportRetryDelays()
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
			return errors.New("registry unavailable")
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
}

func TestRetryReleaseExtractionIsBoundedAndContextAware(t *testing.T) {
	t.Run("bounded", func(t *testing.T) {
		var elapsed time.Duration
		var attempts int
		expectedErr := errors.New("extract failed")
		err := retryReleaseExtraction(context.Background(), "release-images-latest", releaseImportRetryDelays(), func(_ context.Context, delay time.Duration) error {
			elapsed += delay
			return nil
		}, func(context.Context) error {
			attempts++
			return expectedErr
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
	})

	t.Run("cancellation during backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var attempts int
		err := retryReleaseExtraction(ctx, "release-images-latest", releaseImportRetryDelays(), func(ctx context.Context, _ time.Duration) error {
			cancel()
			return ctx.Err()
		}, func(context.Context) error {
			attempts++
			return errors.New("extract failed")
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
		err := retryReleaseExtraction(ctx, "release-images-latest", releaseImportRetryDelays(), sleepForReleaseImportRetry, func(context.Context) error {
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
