package testimagesdistributor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	testimagestreamtagimportv1 "github.com/openshift/ci-tools/pkg/api/testimagestreamtagimport/v1"
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
		buildClusters sets.Set[string]
		filter        objectFilter

		expected []reconcile.Request
		verify   func(r []reconcile.Request) error
	}{
		{
			name:          "Generates requests for all buildclusters",
			buildClusters: sets.New[string]("build01", "build02"),
			expected: []reconcile.Request{
				reconcileRequest("build01_"+namespace, name),
				reconcileRequest("build02_"+namespace, name),
			},
		},
		{
			name:          "Filter is respected",
			buildClusters: sets.New[string]("build01"),
			filter:        func(_ types.NamespacedName) bool { return false },
		},
		{
			name:          "RoundTrips with DecodeRequest",
			buildClusters: sets.New[string]("build01"),
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
				Status: imagev1.ImageStreamStatus{
					Tags: []imagev1.NamedTagEventList{{Tag: tagName}},
				},
			}
			event := event.TypedCreateEvent[*imagev1.ImageStream]{Object: obj}
			handler.Create(context.Background(), event, queue)

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
	workqueue.TypedRateLimitingInterface[reconcile.Request]
	lock     sync.Mutex
	received []reconcile.Request
}

func (hq *hijackingQueue) Add(item reconcile.Request) {
	hq.lock.Lock()
	defer hq.lock.Unlock()
	hq.received = append(hq.received, item)
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

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ci",
			Name:      "registry-pull-credentials",
		},
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("abc")},
		Type: corev1.SecretTypeDockerConfigJson,
	}

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
	referenceImageStream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: referenceImageStreamTag.Namespace,
			Name:      strings.Split(referenceImageStreamTag.Name, ":")[0],
			Annotations: map[string]string{
				"release.openshift.io/config": "bar",
			},
		},
	}

	imageStreamTagWithBuild01PullSpec := func() *imagev1.ImageStreamTag {
		copy := referenceImageStreamTag.DeepCopy()
		copy.Image.DockerImageReference = "registry.build01.ci.openshift.org/ci-op-hbtwhrrm/pipeline@sha256:328d0a90295ef5f5932807bcab8f230007afeb1572d1d7878ab8bdae671dfa8b"
		return copy
	}

	outdatedImageStreamTag := func() *imagev1.ImageStreamTag {
		copy := referenceImageStreamTag.DeepCopy()
		copy.Image.Name = "old"
		return copy
	}

	expectedPullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: referenceImageStreamTag.Namespace,
			// Do not use the const here, we want this to fail if someone changes its value
			Name: "registry-pull-credentials",
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("abc"),
		},
	}
	outdatedPullSecret := func() *corev1.Secret {
		copy := expectedPullSecret.DeepCopy()
		copy.Data[corev1.DockerConfigJsonKey] = []byte("gibberish")
		return copy
	}

	expectedRoleBindig := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: referenceImageStreamTag.Namespace,
			Name:      "ci-operator-image-manager",
		},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      "ci-operator",
			Namespace: "ci",
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     "ci-operator-image-manager",
		},
	}
	outdatedRoleBindig := func() *rbacv1.RoleBinding {
		copy := expectedRoleBindig.DeepCopy()
		copy.RoleRef.Kind = "not-a-clusterr-role"
		return copy
	}

	expectedRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: referenceImageStreamTag.Namespace,
			Name:      "ci-operator-image-manager",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"image.openshift.io"},
				Resources: []string{"imagestreamtags", "imagestreams", "imagestreams/layers"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch"},
			},
		},
	}
	outdatedRole := func() *rbacv1.Role {
		copy := expectedRole.DeepCopy()
		copy.Rules = append(copy.Rules, rbacv1.PolicyRule{})
		return copy
	}

	expectedNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: referenceImageStreamTag.Namespace},
	}

	expectedImageStream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: referenceImageStreamTag.Namespace,
			Name:      strings.Split(referenceImageStreamTag.Name, ":")[0],
			Annotations: map[string]string{
				"release.openshift.io/config": "bar",
			},
		},
		Spec: imagev1.ImageStreamSpec{
			LookupPolicy: imagev1.ImageLookupPolicy{
				Local: true,
			},
		},
	}
	outdatedImageStream := func() *imagev1.ImageStream {
		copy := expectedImageStream.DeepCopy()
		copy.Spec.LookupPolicy.Local = false
		copy.ObjectMeta.Annotations["release.openshift.io/config"] = "baz"
		return copy
	}

	ctx := context.Background()
	verifyEverythingCreated := func(c ctrlruntimeclient.Client) error {
		if err := c.Get(ctx, types.NamespacedName{Name: expectedNamespace.Name}, &corev1.Namespace{}); err != nil {
			return fmt.Errorf("expected namespace %s, but failed to get it: %w", expectedNamespace.Name, err)
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
						Name: "registry.ci.openshift.org/ns/4.2@sha256:a273f5ac7f1ad8f7ffab45205ac36c8dff92d9107ef3ae429eeb135fa8057b8b",
					},
					To:              &corev1.LocalObjectReference{Name: "Question"},
					ReferencePolicy: imagev1.TagReferencePolicy{Type: "Local"},
					ImportPolicy:    imagev1.TagImportPolicy{ImportMode: imagev1.ImportModePreserveOriginal},
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
			return fmt.Errorf("failed to get import %s: %w", imageImportName.String(), err)
		}
		if diff := cmp.Diff(imageStreamImport, actualImport, cmpopts.IgnoreFields(imagev1.ImageStreamImport{}, "ResourceVersion", "Kind", "APIVersion")); diff != "" {
			return fmt.Errorf("actual import differs from expected: %s", diff)
		}

		actualRoleBinding := &rbacv1.RoleBinding{}
		roleBindingName := types.NamespacedName{
			Namespace: imageStreamImport.Namespace,
			Name:      "ci-operator-image-manager",
		}
		if err := c.Get(ctx, roleBindingName, actualRoleBinding); err != nil {
			return fmt.Errorf("failed to get rolebinding %s: %w", roleBindingName.String(), err)
		}
		if diff := cmp.Diff(expectedRoleBindig, actualRoleBinding, cmpopts.IgnoreFields(rbacv1.RoleBinding{}, "ResourceVersion", "Kind", "APIVersion")); diff != "" {
			return fmt.Errorf("actual rolebinding differs from expected: %s", diff)
		}

		actualRole := &rbacv1.Role{}
		roleName := types.NamespacedName{
			Namespace: imageStreamImport.Namespace,
			Name:      "ci-operator-image-manager",
		}
		if err := c.Get(ctx, roleName, actualRole); err != nil {
			return fmt.Errorf("failed to get role %s: %w", roleName.String(), err)
		}
		if diff := cmp.Diff(expectedRole, actualRole, cmpopts.IgnoreFields(rbacv1.Role{}, "ResourceVersion", "Kind", "APIVersion")); diff != "" {
			return fmt.Errorf("actual role differs from expected: %s", diff)
		}

		actualImageStream := &imagev1.ImageStream{}
		imageStreamName := types.NamespacedName{
			Namespace: imageStreamImport.Namespace,
			Name:      strings.Split(imageStreamImport.Name, ":")[0],
		}
		if err := c.Get(ctx, imageStreamName, actualImageStream); err != nil {
			return fmt.Errorf("failed to get imagestream %s: %w", imageStreamName.String(), err)
		}
		if diff := cmp.Diff(expectedImageStream, actualImageStream, cmpopts.IgnoreFields(imagev1.ImageStream{}, "ResourceVersion", "Kind", "APIVersion")); diff != "" {
			return fmt.Errorf("actual imagestream differs from expected: %s", diff)
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
			registryClient:      fakeclient.NewClientBuilder().WithRuntimeObjects(referenceImageStream.DeepCopy()).Build(),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": fakeclient.NewClientBuilder().Build()},
			verify: func(_ ctrlruntimeclient.Client, _ map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %w", err)
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
					return fmt.Errorf("error %w is not terminal", err)
				}
				return nil
			},
		},
		{
			name: "ImageStreamTag with build01 reference, no import is created",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient:      fakeclient.NewClientBuilder().WithRuntimeObjects(referenceImageStream.DeepCopy(), imageStreamTagWithBuild01PullSpec()).Build(),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": fakeclient.NewClientBuilder().Build()},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %w", err)
				}
				name := types.NamespacedName{
					Namespace: referenceImageStreamTag.Namespace,
					Name:      referenceImageStreamTag.Name,
				}
				if err := bc["01"].Get(ctx, name, &imagev1.ImageStreamImport{}); !apierrors.IsNotFound(err) {
					return fmt.Errorf("expected to get not found err, but got %w", err)
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
			registryClient:      fakeclient.NewClientBuilder().WithRuntimeObjects(referenceImageStream.DeepCopy(), referenceImageStreamTag.DeepCopy()).Build(),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": fakeclient.NewClientBuilder().WithRuntimeObjects(referenceImageStreamTag.DeepCopy()).Build()},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %w", err)
				}
				name := types.NamespacedName{
					Namespace: referenceImageStreamTag.Namespace,
					Name:      referenceImageStreamTag.Name,
				}
				if err := bc["01"].Get(ctx, name, &imagev1.ImageStreamImport{}); !apierrors.IsNotFound(err) {
					return fmt.Errorf("expected to get not found err, but got %w", err)
				}
				return nil
			},
		},
		{
			name: "Outdated imageStreamtag, Namespace, pull secret, imagestream and import and rbac are created",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient:      fakeclient.NewClientBuilder().WithRuntimeObjects(referenceImageStream.DeepCopy(), referenceImageStreamTag.DeepCopy()).Build(),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": bcc(fakeclient.NewClientBuilder().WithRuntimeObjects(secret.DeepCopy(), outdatedImageStreamTag()).Build())},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %w", err)
				}
				return verifyEverythingCreated(bc["01"])
			},
		},
		{
			name: "Outdated imageStreamtag, pull secret, imagestream, import and rbac are created",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient: fakeclient.NewClientBuilder().WithRuntimeObjects(referenceImageStream.DeepCopy(), referenceImageStreamTag.DeepCopy()).Build(),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": bcc(fakeclient.NewClientBuilder().WithRuntimeObjects(
				secret.DeepCopy(),
				outdatedImageStreamTag(),
				expectedNamespace.DeepCopy(),
			).Build())},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %w", err)
				}
				return verifyEverythingCreated(bc["01"])
			},
		},
		{
			name: "Outdated imageStreamtag and pull secret, pull secret is updated, imagestream import and rbac created",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient: fakeclient.NewClientBuilder().WithRuntimeObjects(referenceImageStream.DeepCopy(), referenceImageStreamTag.DeepCopy()).Build(),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": bcc(fakeclient.NewClientBuilder().WithRuntimeObjects(
				secret.DeepCopy(),
				outdatedImageStreamTag(),
				expectedNamespace.DeepCopy(),
				outdatedPullSecret(),
			).Build())},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %w", err)
				}
				return verifyEverythingCreated(bc["01"])
			},
		},
		{
			name: "Outdated imageStreamtag and rbac, rbac updated, imagestream and import created",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient: fakeclient.NewClientBuilder().WithRuntimeObjects(referenceImageStream.DeepCopy(), referenceImageStreamTag.DeepCopy()).Build(),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": bcc(fakeclient.NewClientBuilder().WithRuntimeObjects(
				secret.DeepCopy(),
				outdatedImageStreamTag(),
				expectedNamespace.DeepCopy(),
				outdatedRoleBindig(),
				outdatedRole(),
				expectedPullSecret.DeepCopy(),
			).Build())},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %w", err)
				}
				return verifyEverythingCreated(bc["01"])
			},
		},
		{
			name: "Outdated Imagestream is updated, import is created",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient: fakeclient.NewClientBuilder().WithRuntimeObjects(referenceImageStream.DeepCopy(), referenceImageStreamTag.DeepCopy()).Build(),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": bcc(fakeclient.NewClientBuilder().WithRuntimeObjects(
				secret.DeepCopy(),
				expectedNamespace.DeepCopy(),
				expectedPullSecret.DeepCopy(),
				outdatedImageStream(),
				outdatedImageStreamTag(),
			).Build())},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %w", err)
				}
				return verifyEverythingCreated(bc["01"])
			},
		},
		{
			name: "Outdated imageStreamtag, import is created",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient: fakeclient.NewClientBuilder().WithRuntimeObjects(referenceImageStream.DeepCopy(), referenceImageStreamTag.DeepCopy()).Build(),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": bcc(fakeclient.NewClientBuilder().WithRuntimeObjects(
				secret.DeepCopy(),
				outdatedImageStreamTag(),
				expectedNamespace.DeepCopy(),
				expectedPullSecret.DeepCopy(),
				expectedImageStream.DeepCopy(),
			).Build())},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				if err != nil {
					return fmt.Errorf("unexpected error: %w", err)
				}
				return verifyEverythingCreated(bc["01"])
			},
		},
		{
			name: "Outdated imageStreamtag, import is created, failure is returned",
			request: types.NamespacedName{
				Namespace: "01_" + referenceImageStreamTag.Namespace,
				Name:      referenceImageStreamTag.Name,
			},
			registryClient: fakeclient.NewClientBuilder().WithRuntimeObjects(referenceImageStream.DeepCopy(), referenceImageStreamTag.DeepCopy()).Build(),
			buildClusterClients: map[string]ctrlruntimeclient.Client{"01": bcc(fakeclient.NewClientBuilder().WithRuntimeObjects(
				secret.DeepCopy(),
				outdatedImageStreamTag(),
				expectedNamespace.DeepCopy(),
				expectedPullSecret.DeepCopy(),
				expectedImageStream.DeepCopy(),
			).Build(), func(c *imageImportStatusSettingClient) { c.failure = true },
			)},
			verify: func(rc ctrlruntimeclient.Client, bc map[string]ctrlruntimeclient.Client, err error) error {
				exp := "imageStreamImport did not succeed: reason: , message: failing as requested"
				if err == nil || err.Error() != exp {
					return fmt.Errorf("expected error message %s, got %w", exp, err)
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
			log := logrus.NewEntry(logrus.StandardLogger())
			logrus.SetLevel(logrus.TraceLevel)
			r := &reconciler{
				log:                 log,
				registryClusterName: "app.ci",
				registryClient:      tc.registryClient,
				buildClusterClients: tc.buildClusterClients,
				forbiddenRegistries: sets.New[string]("default-route-openshift-image-registry.apps.build01.ci.devcluster.openshift.com",
					"registry.build01.ci.openshift.org",
					"registry.build02.ci.openshift.org",
				),
			}

			request := reconcile.Request{NamespacedName: tc.request}
			err := r.reconcile(context.Background(), request, r.log)
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

func (client *imageImportStatusSettingClient) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
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

// indexConfigsByTestInputImageStreamTag must be an agents.IndexFn
var _ agents.IndexFn = indexConfigsByTestInputImageStreamTag(nil)

func TestTestImageStramTagImportHandlerRoundTrips(t *testing.T) {
	t.Parallel()
	const cluster, namespace, name = "cluster", "namespace", "name"
	obj := &testimagestreamtagimportv1.TestImageStreamTagImport{
		Spec: testimagestreamtagimportv1.TestImageStreamTagImportSpec{
			ClusterName: cluster,
			Namespace:   namespace,
			Name:        name,
		},
	}
	queue := &hijackingQueue{}

	event := event.TypedCreateEvent[*testimagestreamtagimportv1.TestImageStreamTagImport]{Object: obj}
	testImageStreamTagImportHandler(logrus.NewEntry(logrus.StandardLogger()), sets.New[string]()).Create(context.Background(), event, queue)

	if n := len(queue.received); n != 1 {
		t.Fatalf("expected exactly one reconcile request, got %d(%v)", n, queue.received)
	}

	actualCluster, namespacedName, err := decodeRequest(queue.received[0])
	if err != nil {
		t.Fatalf("error decoding request: %v", err)
	}
	if actualCluster != cluster {
		t.Errorf("expected cluster to be %s, was %s", cluster, actualCluster)
	}
	if namespacedName.Namespace != namespace {
		t.Errorf("expected namespace to be %s, was %s", namespace, namespacedName.Namespace)
	}
	if namespacedName.Name != name {
		t.Errorf("expected name to be %s, was %s", name, namespacedName.Name)
	}
}

func TestTestInputImageStreamTagFilterFactory(t *testing.T) {
	t.Parallel()
	const namespace, streamName, tagName = "namespace", "streamName", "streamTag"
	testCases := []struct {
		name                            string
		config                          api.ReleaseBuildConfiguration
		client                          ctrlruntimeclient.Client
		buildClusterClients             map[string]ctrlruntimeclient.Client
		additionalImageStreamTags       sets.Set[string]
		additionalImageStreams          sets.Set[string]
		additionalImageStreamNamespaces sets.Set[string]
		expectedResult                  bool
	}{
		{
			name:                      "imagestreamtag is explicitly allowed",
			additionalImageStreamTags: sets.New[string](namespace + "/" + streamName + ":" + tagName),
			expectedResult:            true,
		},
		{
			name:                   "imagestream is explicitly allowed",
			additionalImageStreams: sets.New[string](namespace + "/" + streamName),
			expectedResult:         true,
		},
		{
			name:                            "imagestream_namespace is explicitly allowed",
			additionalImageStreamNamespaces: sets.New[string](namespace),
			expectedResult:                  true,
		},
		{
			name: "imagestreamtag is referenced by config",
			config: api.ReleaseBuildConfiguration{
				RawSteps: []api.StepConfiguration{
					{
						InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
							InputImage: api.InputImage{
								BaseImage: api.ImageStreamTagReference{Namespace: namespace, Name: streamName, Tag: tagName}},
						},
					},
				},
			},
			expectedResult: true,
		},
		{
			name: "imagestream is referenced by config",
			config: api.ReleaseBuildConfiguration{InputConfiguration: api.InputConfiguration{
				ReleaseTagConfiguration: &api.ReleaseTagConfiguration{Namespace: namespace, Name: streamName},
			}},
			expectedResult: true,
		},
		{
			name: "imagestream is referenced by integration stream",
			config: api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					Releases: map[string]api.UnresolvedRelease{
						api.InitialReleaseName: {
							Integration: &api.Integration{
								Namespace: namespace,
								Name:      streamName,
							},
						},
						api.LatestReleaseName: {
							Integration: &api.Integration{
								Namespace:          namespace,
								Name:               streamName,
								IncludeBuiltImages: true,
							},
						},
					},
				},
			},
			expectedResult: true,
		},
		{
			name: "imagestream is referenced by non-integration stream",
			config: api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					Releases: map[string]api.UnresolvedRelease{
						api.LatestReleaseName: {
							Release: &api.Release{
								Version:      "v",
								Channel:      api.ReleaseChannelStable,
								Architecture: api.ReleaseArchitectureAMD64,
							},
						},
					},
				},
			},
		},
		{
			name: "imagestreamtag is referenced by imagestreamtag import",
			client: fakeclient.NewClientBuilder().WithRuntimeObjects((&testimagestreamtagimportv1.TestImageStreamTagImport{Spec: testimagestreamtagimportv1.TestImageStreamTagImportSpec{
				Namespace: namespace,
				Name:      streamName + ":" + tagName,
			}}).WithImageStreamLabels()).Build(),
			expectedResult: true,
		},
		{
			name: "imagestreamtag is referenced by imagestreatag import in a buildcluster",
			buildClusterClients: map[string]ctrlruntimeclient.Client{"build01": fakeclient.NewClientBuilder().WithRuntimeObjects((&testimagestreamtagimportv1.TestImageStreamTagImport{
				Spec: testimagestreamtagimportv1.TestImageStreamTagImportSpec{
					Namespace: namespace,
					Name:      streamName + ":" + tagName,
				}}).WithImageStreamLabels()).Build(),
			},
			expectedResult: true,
		},
		{
			name: "no reference, imagestreatag gets denied",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.client == nil {
				tc.client = fakeclient.NewClientBuilder().Build()
			}
			if tc.buildClusterClients == nil {
				tc.buildClusterClients = map[string]ctrlruntimeclient.Client{}
			}
			configAgent := agents.NewFakeConfigAgent(map[string]map[string][]api.ReleaseBuildConfiguration{"": {"": []api.ReleaseBuildConfiguration{tc.config}}})
			filter, err := testInputImageStreamTagFilterFactory(
				logrus.NewEntry(logrus.New()),
				configAgent,
				tc.client,
				noOpRegistryResolver{},
				tc.additionalImageStreamTags,
				tc.additionalImageStreams,
				tc.additionalImageStreamNamespaces,
				tc.buildClusterClients,
			)
			if err != nil {
				t.Fatalf("failed to construct filter: %v", err)
			}
			if result := filter(types.NamespacedName{Namespace: namespace, Name: streamName + ":" + tagName}); result != tc.expectedResult {
				t.Errorf("expected result %t, got result %t", tc.expectedResult, result)
			}
		})
	}
}

var _ registryResolver = noOpRegistryResolver{}

type noOpRegistryResolver struct{}

func (noOpRegistryResolver) ResolveConfig(cfg api.ReleaseBuildConfiguration) (api.ReleaseBuildConfiguration, error) {
	return cfg, nil
}

func TestSourceForConfigChangeChannel(t *testing.T) {
	t.Parallel()

	type requestWithCluster struct {
		cluster string
		request types.NamespacedName
	}

	buildClusters := sets.New[string]("build01", "build02")
	testCases := []struct {
		name   string
		change agents.IndexDelta
		client ctrlruntimeclient.Client

		expected []requestWithCluster
	}{
		{
			name:   "Config was added, we trigger an event per cluster",
			change: agents.IndexDelta{IndexKey: "namespace/name:tag", Added: []*api.ReleaseBuildConfiguration{{}}},

			expected: []requestWithCluster{
				{cluster: "build01", request: types.NamespacedName{Namespace: "namespace", Name: "name:tag"}},
				{cluster: "build02", request: types.NamespacedName{Namespace: "namespace", Name: "name:tag"}},
			},
		},
		{
			name:   "Config was removed, we don't trigger an event",
			change: agents.IndexDelta{IndexKey: "namespace/name:tag", Removed: []*api.ReleaseBuildConfiguration{{}}},
		},
		{
			name:   "Config for imagestream was added, we trigger an event per cluster and tag",
			change: agents.IndexDelta{IndexKey: "imagestream_namespace/name", Added: []*api.ReleaseBuildConfiguration{{}}},
			client: fakeclient.NewClientBuilder().WithRuntimeObjects(&imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Namespace: "namespace", Name: "name"}, Status: imagev1.ImageStreamStatus{Tags: []imagev1.NamedTagEventList{{Tag: "foo"}, {Tag: "bar"}}}}).Build(),

			expected: []requestWithCluster{
				{cluster: "build01", request: types.NamespacedName{Namespace: "namespace", Name: "name:foo"}},
				{cluster: "build01", request: types.NamespacedName{Namespace: "namespace", Name: "name:bar"}},
				{cluster: "build02", request: types.NamespacedName{Namespace: "namespace", Name: "name:foo"}},
				{cluster: "build02", request: types.NamespacedName{Namespace: "namespace", Name: "name:bar"}},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.client == nil {
				tc.client = fakeclient.NewClientBuilder().Build()
			}

			changeChannel := make(chan agents.IndexDelta)

			source := sourceForConfigChangeChannel(buildClusters, tc.client, changeChannel)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			queue := &hijackingQueue{}

			if err := source.Start(ctx, queue); err != nil {
				t.Fatalf("failed to start source: %v", err)
			}
			changeChannel <- tc.change
			close(changeChannel)

			// The source copies the data from the input channel to the workqueue asynchronously
			// and there is no way for us to properly synchronize that so instead we just wait.
			// We can not poll because for negative testcases (expect no request) that would instanly
			// succeed and later events won't make the test fail correctly.
			time.Sleep(10 * time.Second)
			queue.lock.Lock()
			if len(queue.received) != len(tc.expected) {
				t.Errorf("expected to get %d events, got %d", len(tc.expected), len(queue.received))
			}

			var actual []requestWithCluster
			for _, request := range queue.received {
				cluster, namespacedName, err := decodeRequest(request)
				if err != nil {
					t.Errorf("failed to decode request %s: %v", request, err)
					continue
				}
				actual = append(actual, requestWithCluster{cluster: cluster, request: namespacedName})
			}
			if diff := cmp.Diff(tc.expected, actual, cmp.AllowUnexported(requestWithCluster{})); diff != "" {
				t.Errorf("expected requests differ from actual: %s", diff)
			}

		})
	}
}
