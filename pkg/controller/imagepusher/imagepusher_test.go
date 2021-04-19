package imagepusher

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestTestInputImageStreamTagFilterFactory(t *testing.T) {
	testCases := []struct {
		name         string
		l            *logrus.Entry
		imageStreams sets.String
		nn           types.NamespacedName
		expected     bool
	}{
		{
			name: "default",
			nn:   types.NamespacedName{Namespace: "some-namespace", Name: "some-name:some-tag"},
		},
		{
			name:         "imageStreams: true",
			nn:           types.NamespacedName{Namespace: "some-ns", Name: "stream:tag"},
			imageStreams: sets.NewString("some-ns/stream"),
			expected:     true,
		},
		{
			name:         "imageStreams: false",
			nn:           types.NamespacedName{Namespace: "some-ns", Name: "other-stream:tag"},
			imageStreams: sets.NewString("some-ns/stream"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.l = logrus.WithField("tc.name", tc.name)
			objectFilter := imageStreamTagFilterFactory(tc.l, tc.imageStreams)
			if diff := cmp.Diff(tc.expected, objectFilter(tc.nn)); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
		})
	}
}

func init() {
	if err := imagev1.Install(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestReconcile(t *testing.T) {
	t.Parallel()

	now := metav1.Now()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "ci",
			Name:        "registry-pull-credentials",
			Annotations: map[string]string{"a": "c"},
		},
		Data: map[string][]byte{"pass": []byte("some")},
		Type: corev1.SecretTypeDockerConfigJson,
	}

	release46ISTag := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ocp",
			Name:      "release:4.6",
		},
		Image: imagev1.Image{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "sha256:14a6cf5a0b9ef37ca45c3ce75ff0c16d44ddc5e446aa00aec5e7819157e838be",
				CreationTimestamp: now,
			},
			DockerImageReference: "image-registry.openshift-image-registry.svc:5000/ocp/release@sha256:14a6cf5a0b9ef37ca45c3ce75ff0c16d44ddc5e446aa00aec5e7819157e838be",
		},
	}

	release46ISTagAPICI := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ocp",
			Name:      "release:4.6",
		},
		Image: imagev1.Image{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "sha256:14a6cf5a0b9ef37ca45c3ce75ff0c16d44ddc5e446aa00aec5e7819157e838be",
				CreationTimestamp: now,
			},
			DockerImageReference: "registry.ci.openshift.org/ocp/release@sha256:14a6cf5a0b9ef37ca45c3ce75ff0c16d44ddc5e446aa00aec5e7819157e838be",
		},
	}

	releaseIS := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ocp",
			Name:      "release",
			Annotations: map[string]string{
				"a": "b",
			},
		},
	}

	ctx := context.Background()

	for _, tc := range []struct {
		name                 string
		request              types.NamespacedName
		apiCIClient          ctrlruntimeclient.Client
		appCIClient          ctrlruntimeclient.Client
		expected             error
		expectedAPICIObjects []runtime.Object
		expectedAPPCIObjects []runtime.Object
		verify               func(apiCIClient ctrlruntimeclient.Client, appCIClient ctrlruntimeclient.Client) error
	}{
		{
			name: "a new tag is created in app.ci",
			request: types.NamespacedName{
				Name:      "release:4.6",
				Namespace: "ocp",
			},
			apiCIClient: bcc(fakeclient.NewFakeClient(secret.DeepCopy())),
			appCIClient: fakeclient.NewFakeClient(release46ISTag.DeepCopy(), releaseIS.DeepCopy()),

			verify: func(apiCIClient ctrlruntimeclient.Client, appCIClient ctrlruntimeclient.Client) error {
				actualImageStreamImport := &imagev1.ImageStreamImport{}
				if err := apiCIClient.Get(ctx, types.NamespacedName{Name: "release", Namespace: "ocp"}, actualImageStreamImport); err != nil {
					return fmt.Errorf("faile to get ImageStreamImport from api.ci: %w", err)
				}
				expectedImageStreamImport := &imagev1.ImageStreamImport{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ocp",
						Name:      "release",
					},
					Spec: imagev1.ImageStreamImportSpec{
						Import: true,
						Images: []imagev1.ImageImportSpec{
							{
								From:            corev1.ObjectReference{Kind: "DockerImage", Name: "registry.ci.openshift.org/ocp/release@sha256:14a6cf5a0b9ef37ca45c3ce75ff0c16d44ddc5e446aa00aec5e7819157e838be"},
								To:              &corev1.LocalObjectReference{Name: "4.6"},
								ReferencePolicy: imagev1.TagReferencePolicy{Type: "Local"},
							},
						},
					},
					Status: imagev1.ImageStreamImportStatus{
						Images: []imagev1.ImageImportStatus{
							{
								Image: &imagev1.Image{},
							},
						},
					},
				}
				if diff := cmp.Diff(expectedImageStreamImport, actualImageStreamImport, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}

				actualImageStream := &imagev1.ImageStream{}
				if err := apiCIClient.Get(ctx, types.NamespacedName{Name: "release", Namespace: "ocp"}, actualImageStream); err != nil {
					return err
				}
				expectedImageStream := &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ocp",
						Name:      "release",
						Labels:    map[string]string{"dptp.openshift.io/requester": "image_pusher"},
					},
				}
				if diff := cmp.Diff(expectedImageStream, actualImageStream, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}

				actualNamespace := &corev1.Namespace{}
				if err := apiCIClient.Get(ctx, types.NamespacedName{Name: "ocp"}, actualNamespace); err != nil {
					return err
				}
				expectedNamespace := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ocp",
						Labels: map[string]string{
							"dptp.openshift.io/requester": "image_pusher",
						},
					},
				}
				if diff := cmp.Diff(expectedNamespace, actualNamespace, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name: "is current",
			request: types.NamespacedName{
				Name:      "release:4.6",
				Namespace: "ocp",
			},
			apiCIClient: fakeclient.NewFakeClient(release46ISTagAPICI.DeepCopy()),
			appCIClient: fakeclient.NewFakeClient(release46ISTag.DeepCopy(), releaseIS.DeepCopy()),

			verify: func(apiCIClient ctrlruntimeclient.Client, appCIClient ctrlruntimeclient.Client) error {
				actualImageStreamImport := &imagev1.ImageStreamImport{}
				if err := apiCIClient.Get(ctx, types.NamespacedName{Name: "release", Namespace: "ocp"}, actualImageStreamImport); !kerrors.IsNotFound(err) {
					return fmt.Errorf("the expected NotFound error did not occur")
				}

				actualNamespace := &corev1.Namespace{}
				if err := apiCIClient.Get(ctx, types.NamespacedName{Name: "ocp"}, actualNamespace); !kerrors.IsNotFound(err) {
					return fmt.Errorf("the expected NotFound error did not occur")
				}
				return nil
			},
		},
	} {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &reconciler{
				log:         logrus.NewEntry(logrus.New()),
				apiCIClient: tc.apiCIClient,
				appCIClient: tc.appCIClient,
			}

			request := reconcile.Request{NamespacedName: tc.request}
			actual := r.reconcile(context.Background(), request, r.log)

			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", diff)
			}
			if actual == nil && tc.verify != nil {
				if err := tc.verify(tc.apiCIClient, tc.appCIClient); err != nil {
					t.Errorf("unexpcected error: %v", err)
				}
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
