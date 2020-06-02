package testimagesdistributor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	imagev1 "github.com/openshift/api/image/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/load/agents"
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestRegistryClusterHandlerFactory(t *testing.T) {
	t.Parallel()
	streamName := "stream"
	tagName := "tag"

	namespace := "namespace"
	name := streamName + ":" + tagName

	testCases := []struct {
		name          string
		buildClusters sets.String
		filter        objectFilter

		expected []reconcile.Request
		verify   func(r []reconcile.Request) error
	}{
		{
			name:          "Generates requests for all buildclusters",
			buildClusters: sets.NewString("build01", "build02"),
			expected: []reconcile.Request{
				reconcileRequest("build01_"+namespace, name),
				reconcileRequest("build02_"+namespace, name),
			},
		},
		{
			name:          "Filter is respected",
			buildClusters: sets.NewString("build01"),
			filter:        func(_ types.NamespacedName) bool { return false },
		},
		{
			name:          "RoundTrips with DecodeRequest",
			buildClusters: sets.NewString("build01"),
			expected:      []reconcile.Request{reconcileRequest("build01_"+namespace, name)},
			verify: func(r []reconcile.Request) error {
				if n := len(r); n != 1 {
					return fmt.Errorf("expected one request, got %d", n)
				}
				cluster, result, err := decodeRequest(r[0])
				if err != nil {
					return fmt.Errorf("decoding failed: %w", err)
				}
				if cluster != "build01" {
					return fmt.Errorf("expected cluster to be build01, was %q", cluster)
				}
				if result.Namespace != namespace {
					return fmt.Errorf("expected namespace to be %q, was %q", namespace, result.Namespace)
				}
				if result.Name != name {
					return fmt.Errorf("expected name to be %q, was %q", name, result.Name)
				}
				return nil
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.filter == nil {
				tc.filter = func(types.NamespacedName) bool { return true }
			}

			handler := registryClusterHandlerFactory(tc.buildClusters, tc.filter)
			queue := &hijackingQueue{}

			obj := &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Name:      streamName,
					Namespace: namespace,
				},
				Spec: imagev1.ImageStreamSpec{
					Tags: []imagev1.TagReference{{Name: tagName}},
				},
			}
			event := event.CreateEvent{Meta: obj, Object: obj}
			handler.Create(event, queue)

			if diff := cmp.Diff(tc.expected, queue.received); diff != "" {
				t.Errorf("received does not match expected, diff: %s", diff)
			}
			if tc.verify != nil {
				if err := tc.verify(queue.received); err != nil {
					t.Errorf("verification failed: %v", err)
				}
			}
		})
	}
}

type hijackingQueue struct {
	// We must embedd it here to satisfy the RateLimitingInterface for the handler,
	// but we leave it as nil, as we only expect the `AddRateLimited` to get called,
	// everything else is a bug in our (test-) code, so having that panic is fine.
	workqueue.RateLimitingInterface
	lock     sync.Mutex
	received []reconcile.Request
}

func (hq *hijackingQueue) Add(item interface{}) {
	asserted, ok := item.(reconcile.Request)
	if !ok {
		panic(fmt.Sprintf("expected to get reconcileRequest, got %T", item))
	}
	hq.lock.Lock()
	defer hq.lock.Unlock()
	hq.received = append(hq.received, asserted)
}

func reconcileRequest(namespace, name string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func TestReconcile(t *testing.T) {

	referenceImageStreamTag := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "4.2:Question",
		},
		Image: imagev1.Image{
			ObjectMeta: metav1.ObjectMeta{
				Name: "sha256:a273f5ac7f1ad8f7ffab45205ac36c8dff92d9107ef3ae429eeb135fa8057b8b",
			},
			DockerImageReference: "registry.svc.ci.openshift.org/ocp/4.4@sha256:a273f5ac7f1ad8f7ffab45205ac36c8dff92d9107ef3ae429eeb135fa8057b8b",
		},
	}

	outdatedImageStreamTag := func() *imagev1.ImageStreamTag {
		copy := referenceImageStreamTag.DeepCopy()
		copy.Image.Name = "old"
		return copy
	}

	pullSecretData := []byte("abc")
	expectedPullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: referenceImageStreamTag.Namespace,
			// Do not use the const here, we want this to fail if someone changes its value
			Name: "registry-cluster-pull-secret",
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: pullSecretData,
		},
	}
	outdatedPullSecret := func() *corev1.Secret {
		copy := expectedPullSecret.DeepCopy()
		copy.Data[corev1.DockerConfigJsonKey] = []byte("gibberish")
		return copy
	}

	expectedNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: referenceImageStreamTag.Namespace},
	}

	ctx := context.Background()
	verifyNamespacePullSecretAndImport := func(c ctrlruntimeclient.Client) error {
		if err := c.Get(ctx, types.NamespacedName{Name: expectedNamespace.Name}, &corev1.Namespace{}); err != nil {
			return fmt.Errorf("expected namespace %s, but failed to get it: %w", referenceImageStreamTag.Name, err)
		}

		pullSecret := &corev1.Secret{}
		pullScretName := types.NamespacedName{
			Namespace: expectedPullSecret.Namespace,
			Name:      expectedPullSecret.Name,
		}
		if err := c.Get(ctx, pullScretName, pullSecret); err != nil {
			return fmt.Errorf("failed to get secret %s: %w", pullScretName.String(), err)
		}
		if diff := cmp.Diff(expectedPullSecret, pullSecret, cmpopts.IgnoreFields(corev1.Secret{}, "ResourceVersion", "Kind", "APIVersion")); diff != "" {
			return fmt.Errorf("pull secret differs from expected: %s", diff)
		}

		imageStreamImport := &imagev1.ImageStreamImport{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: referenceImageStreamTag.Namespace,
				Name:      "4.2",
			},
			Spec: imagev1.ImageStreamImportSpec{
				Import: true,
				Images: []imagev1.ImageImportSpec{{
					From: corev1.ObjectReference{
						Kind: "DockerImage",
						Name: "registry.svc.ci.openshift.org/ocp/4.4@sha256:a273f5ac7f1ad8f7ffab45205ac36c8dff92d9107ef3ae429eeb135fa8057b8b",
					},
					To: &corev1.LocalObjectReference{Name: "Question"},
				}},
			},
			Status: imagev1.ImageStreamImportStatus{
				Images: []imagev1.ImageImportStatus{{Image: &imagev1.Image{}}},
			},
		}
		actualImport := &imagev1.ImageStreamImport{}
		imageImportName := types.NamespacedName{
			Namespace: imageStreamImport.Namespace,
			Name:      imageStreamImport.Name,
		}
		if err := c.Get(ctx, imageImportName, actualImport); err != nil {
			return fmt.Errorf("failed to get import %s: %v", imageImportName.String(), err)
		}
		if diff := cmp.Diff(imageStreamImport, actualImport, cmpopts.IgnoreFields(imagev1.ImageStreamImport{}, "ResourceVersion", "Kind", "APIVersion")); diff != "" {
			return fmt.Errorf("actual import differs from expected: %s", diff)
		}
		return nil
	}

	testCases := []struct {
		name                string
		request             types.NamespacedName
		registryClient      ctrlruntimeclient.Client
		buildClusterClients map[string]ctrlruntimeclient.Client
		verify              func(ctrlruntimeclient.Client, map[string]ctrlruntimeclient.Client, error) error
	}{
		{
			name:                "Request for non existent object doesn't error",
			request:             types.NamespacedName{Namespace: "01_doesnotexist/doesnotexist"},
			registryClient:      fakeclient.NewFakeClient(),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": fakeclient.NewFakeClient()},
			verify: func(_ ctrlruntimeclient.Client, _ map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %v", err)
				}
				return nil
			},
		},
		{
			name:    "Request for non-existent cluster yields terminal error",
			request: types.NamespacedName{Namespace: "01_doesnotexist", Name: "doesnotexist"},
			verify: func(_ ctrlruntimeclient.Client, _ map[string]ctrlruntimeclient.Client, err error) error {
				if err == nil {
					return errors.New("expected error, got none")
				}
				if err := controllerutil.SwallowIfTerminal(err); err != nil {
					return fmt.Errorf("error %v is not terminal", err)
				}
				return nil
			},
		},
		{
			name: "ImageStreamTag is current, no import created",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient:      fakeclient.NewFakeClient(referenceImageStreamTag.DeepCopy()),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": fakeclient.NewFakeClient(referenceImageStreamTag.DeepCopy())},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %v", err)
				}
				name := types.NamespacedName{
					Namespace: referenceImageStreamTag.Namespace,
					Name:      referenceImageStreamTag.Name,
				}
				if err := bc["01"].Get(ctx, name, &imagev1.ImageStreamImport{}); !apierrors.IsNotFound(err) {
					return fmt.Errorf("expected to get not found err, but got %v", err)
				}
				return nil
			},
		},
		{
			name: "Outdated imageStreamtag, Namespace, pull secret and import are created",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient:      fakeclient.NewFakeClient(referenceImageStreamTag.DeepCopy()),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": bcc(fakeclient.NewFakeClient(outdatedImageStreamTag()))},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %v", err)
				}
				return verifyNamespacePullSecretAndImport(bc["01"])
			},
		},
		{
			name: "Outdated imageStreamtag, pull secret and import are created",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient: fakeclient.NewFakeClient(referenceImageStreamTag.DeepCopy()),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": bcc(fakeclient.NewFakeClient(
				outdatedImageStreamTag(),
				expectedNamespace.DeepCopy(),
			))},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %v", err)
				}
				return verifyNamespacePullSecretAndImport(bc["01"])
			},
		},
		{
			name: "Outdated imageStreamtag and pull secret, pull secret is updated, import is created",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient: fakeclient.NewFakeClient(referenceImageStreamTag.DeepCopy()),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": bcc(fakeclient.NewFakeClient(
				outdatedImageStreamTag(),
				expectedNamespace.DeepCopy(),
				outdatedPullSecret(),
			))},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %v", err)
				}
				return verifyNamespacePullSecretAndImport(bc["01"])
			},
		},
		{
			name: "Outdated imageStreamtag, import is created",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient: fakeclient.NewFakeClient(referenceImageStreamTag.DeepCopy()),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": bcc(fakeclient.NewFakeClient(
				outdatedImageStreamTag(),
				expectedNamespace.DeepCopy(),
				expectedPullSecret.DeepCopy(),
			))},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %v", err)
				}
				return verifyNamespacePullSecretAndImport(bc["01"])
			},
		},
		{
			name: "Outdated imageStreamtag, import is created, failure is returned",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient: fakeclient.NewFakeClient(referenceImageStreamTag.DeepCopy()),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": bcc(fakeclient.NewFakeClient(
				outdatedImageStreamTag(),
				expectedNamespace.DeepCopy(),
				expectedPullSecret.DeepCopy(),
			), func(c *imageImportStatusSettingClient) { c.failure = true },
			)},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				exp := "imageStreamImport did not succeed: reason: , message: failing as requested"
				if err == nil || err.Error() != exp {
					return fmt.Errorf("expected error message %s, got %v", exp, err)
				}
				return nil
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc := tc
			// Needed so the racedetector tells us if we accidentally re-use global state, e.G. by not deepcopying
			t.Parallel()
			r := &reconciler{
				ctx:                 ctx,
				log:                 logrus.NewEntry(logrus.StandardLogger()),
				registryClient:      tc.registryClient,
				buildClusterClients: tc.buildClusterClients,
				pullSecretGetter:    func() []byte { return pullSecretData },
				successfulImportsCounter: prometheus.NewCounterVec(
					prometheus.CounterOpts{},
					[]string{"cluster", "ns"},
				),
				failedImportsCounter: prometheus.NewCounterVec(
					prometheus.CounterOpts{},
					[]string{"cluster", "ns"},
				),
			}

			request := reconcile.Request{NamespacedName: tc.request}
			err := r.reconcile(request, r.log)
			if err := tc.verify(r.registryClient, r.buildClusterClients, err); err != nil {
				t.Errorf("verification failed: %v", err)
			}
		})
	}
}

func bcc(upstream ctrlruntimeclient.Client, opts ...func(*imageImportStatusSettingClient)) ctrlruntimeclient.Client {
	c := &imageImportStatusSettingClient{
		Client: upstream,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type imageImportStatusSettingClient struct {
	ctrlruntimeclient.Client
	failure bool
}

func (client *imageImportStatusSettingClient) Create(ctx context.Context, obj runtime.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if asserted, match := obj.(*imagev1.ImageStreamImport); match {
		asserted.Status.Images = []imagev1.ImageImportStatus{{}}
		if client.failure {
			asserted.Status.Images[0].Status.Message = "failing as requested"
		} else {
			asserted.Status.Images[0].Image = &imagev1.Image{}
		}
	}
	return client.Client.Create(ctx, obj, opts...)
}

// indexConfigsByTestInputImageStramTag must be an agents.IndexFn
var _ agents.IndexFn = indexConfigsByTestInputImageStramTag(nil)
