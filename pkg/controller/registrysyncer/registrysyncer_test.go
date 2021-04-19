package registrysyncer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

func TestFindNewest(t *testing.T) {
	now := metav1.Now()
	testCases := []struct {
		name     string
		isTags   map[string]*imagev1.ImageStreamTag
		expected string
	}{
		{
			name: "nil isTags",
		},
		{
			name:   "empty isTags",
			isTags: map[string]*imagev1.ImageStreamTag{},
		},
		{
			name: "1 cluster",
			isTags: map[string]*imagev1.ImageStreamTag{
				"cluster1": {
					Image: imagev1.Image{
						ObjectMeta: metav1.ObjectMeta{
							CreationTimestamp: now,
						},
					},
				},
			},
			expected: "cluster1",
		},
		{
			name: "basic case: 2 clusters",
			isTags: map[string]*imagev1.ImageStreamTag{
				"cluster1": {
					Image: imagev1.Image{
						ObjectMeta: metav1.ObjectMeta{
							CreationTimestamp: now,
						},
					},
				},
				"cluster2": {
					Image: imagev1.Image{
						ObjectMeta: metav1.ObjectMeta{
							CreationTimestamp: metav1.NewTime(now.Add(-1 * time.Minute)),
						},
					},
				},
			},
			expected: "cluster1",
		},
		{
			name: "3 of them",
			isTags: map[string]*imagev1.ImageStreamTag{
				"cluster1": {
					Image: imagev1.Image{
						ObjectMeta: metav1.ObjectMeta{
							CreationTimestamp: now,
						},
					},
				},
				"cluster2": {
					Image: imagev1.Image{
						ObjectMeta: metav1.ObjectMeta{
							CreationTimestamp: metav1.NewTime(now.Add(1 * time.Minute)),
						},
					},
				},
				"cluster3": {
					Image: imagev1.Image{
						ObjectMeta: metav1.ObjectMeta{
							CreationTimestamp: metav1.NewTime(now.Add(-1 * time.Minute)),
						},
					},
				},
			},
			expected: "cluster2",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := findNewest(tc.isTags)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
		})
	}
}

const (
	apiCI = "api.ci"
	appCI = "app.ci"
)

func init() {
	if err := imagev1.Install(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestReconcile(t *testing.T) {
	t.Parallel()

	now := metav1.Now()
	threeMinLater := metav1.NewTime(now.Add(3 * time.Minute))

	applyconfigISTag := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ci",
			Name:      "applyconfig:latest",
		},
		Image: imagev1.Image{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
				CreationTimestamp: now,
			},
			DockerImageReference: "docker-registry.default.svc:5000/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
		},
	}

	applyconfigISTagAppCI := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ci",
			Name:      "applyconfig:latest",
		},
		Image: imagev1.Image{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
				CreationTimestamp: now,
			},
			DockerImageReference: "image-registry.openshift-image-registry.svc:5000/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
		},
	}

	applyconfigISTagNewer := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "ci",
			Name:        "applyconfig:latest",
			Annotations: map[string]string{"a": "b"},
		},
		Image: imagev1.Image{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "sha256:new",
				CreationTimestamp: threeMinLater,
			},
			DockerImageReference: "image-registry.openshift-image-registry.svc:5000/ci/applyconfig@sha256:new",
		},
	}

	applyconfigISTagNewerSameName := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ci",
			Name:      "applyconfig:latest",
		},
		Image: imagev1.Image{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
				CreationTimestamp: threeMinLater,
			},
			DockerImageReference: "image-registry.openshift-image-registry.svc:5000/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
		},
	}

	applyconfigISTagSoftDeleted := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "ci",
			Name:        "applyconfig:latest",
			Annotations: map[string]string{"release.openshift.io/soft-delete": time.Now().Add(-time.Hour).Format(time.RFC3339)},
		},
		Image: imagev1.Image{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
				CreationTimestamp: now,
			},
			DockerImageReference: "docker-registry.default.svc:5000/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
		},
	}

	applyconfigIS := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ci",
			Name:      "applyconfig",
			Annotations: map[string]string{
				"release.openshift.io-something": "copied",
				"something":                      "not-copied",
			},
		},
	}

	applyconfigISDeleted := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ci",
			Name:      "applyconfig",
			Annotations: map[string]string{
				"release.openshift.io-something": "copied",
				"something":                      "not-copied",
			},
			Finalizers:        []string{"dptp.openshift.io/registry-syncer"},
			DeletionTimestamp: &now,
		},
	}

	applyconfigISDeleted2 := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ci",
			Name:      "applyconfig",
			Annotations: map[string]string{
				"release.openshift.io-something": "copied",
			},
			Finalizers:        []string{"dptp.openshift.io/registry-syncer"},
			DeletionTimestamp: &now,
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
			name: "abnormal case: the underlying imagestream is gone",
			request: types.NamespacedName{
				Name:      "applyconfig:latest",
				Namespace: "ci",
			},
			apiCIClient: fakeclient.NewFakeClient(),
			appCIClient: fakeclient.NewFakeClient(applyconfigISTag.DeepCopy()),

			expected: fmt.Errorf("failed to get imageStream %s from cluster app.ci: %w", "ci/applyconfig", fmt.Errorf("imagestreams.image.openshift.io \"applyconfig\" not found")),
		},
		{
			name: "a new tag is created in api.ci",
			request: types.NamespacedName{
				Name:      "applyconfig:latest",
				Namespace: "ci",
			},
			apiCIClient: fakeclient.NewFakeClient(),
			appCIClient: fakeclient.NewFakeClient(applyconfigISTagAppCI.DeepCopy(), applyconfigIS.DeepCopy()),

			verify: func(apiCIClient ctrlruntimeclient.Client, appCIClient ctrlruntimeclient.Client) error {
				actualImageStreamTag := &imagev1.ImageStreamTag{}
				if err := apiCIClient.Get(ctx, types.NamespacedName{Name: "applyconfig:latest", Namespace: "ci"}, actualImageStreamTag); err != nil {
					return fmt.Errorf("faile to get tag imagestreamtag from api.ci: %w", err)
				}
				expectedImageStreamTag := &imagev1.ImageStreamTag{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ci",
						Name:      "applyconfig:latest",
					},
					Tag: &imagev1.TagReference{
						From: &corev1.ObjectReference{
							Kind: "DockerImage",
							Name: "registry.ci.openshift.org/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
						},
					},
				}
				if diff := cmp.Diff(expectedImageStreamTag, actualImageStreamTag, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}

				actualImageStream := &imagev1.ImageStream{}
				if err := apiCIClient.Get(ctx, types.NamespacedName{Name: "applyconfig", Namespace: "ci"}, actualImageStream); err != nil {
					return err
				}
				expectedImageStream := &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ci",
						Name:      "applyconfig",
						Annotations: map[string]string{
							"release.openshift.io-something": "copied",
						},
					},
				}
				if diff := cmp.Diff(expectedImageStream, actualImageStream, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}

				actualImageStream = &imagev1.ImageStream{}
				if err := appCIClient.Get(ctx, types.NamespacedName{Name: "applyconfig", Namespace: "ci"}, actualImageStream); err != nil {
					return err
				}
				expectedImageStream = &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ci",
						Name:      "applyconfig",
						Annotations: map[string]string{
							"release.openshift.io-something": "copied",
							"something":                      "not-copied",
						},
						Finalizers: []string{"dptp.openshift.io/registry-syncer"},
					},
				}
				if diff := cmp.Diff(expectedImageStream, actualImageStream, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}

				actualNamespace := &corev1.Namespace{}
				if err := apiCIClient.Get(ctx, types.NamespacedName{Name: "ci"}, actualNamespace); err != nil {
					return err
				}
				expectedNamespace := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "ci",
						ResourceVersion: "1",
						Labels: map[string]string{
							"dptp.openshift.io/requester": "registry_syncer",
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
			name: "app.ci is newer",
			request: types.NamespacedName{
				Name:      "applyconfig:latest",
				Namespace: "ci",
			},
			apiCIClient: fakeclient.NewFakeClient(applyconfigISTag.DeepCopy()),
			appCIClient: fakeclient.NewFakeClient(applyconfigISTagNewer.DeepCopy(), applyconfigIS.DeepCopy()),

			verify: func(apiCIClient ctrlruntimeclient.Client, appCIClient ctrlruntimeclient.Client) error {
				actualImageStreamTag := &imagev1.ImageStreamTag{}
				if err := apiCIClient.Get(ctx, types.NamespacedName{Name: "applyconfig:latest", Namespace: "ci"}, actualImageStreamTag); err != nil {
					return err
				}
				expectedImageStreamTag := &imagev1.ImageStreamTag{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:   "ci",
						Name:        "applyconfig:latest",
						Annotations: map[string]string{"a": "b"},
					},
					Tag: &imagev1.TagReference{
						From: &corev1.ObjectReference{
							Kind: "DockerImage",
							Name: "registry.ci.openshift.org/ci/applyconfig@sha256:new",
						},
					},
					Image: imagev1.Image{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
							CreationTimestamp: now,
						},
						DockerImageReference: "docker-registry.default.svc:5000/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
					},
				}
				//ignoring DeletionTimestamp: because it is changed when returning from fakeclient
				expectedImageStreamTag.Image.CreationTimestamp = actualImageStreamTag.Image.CreationTimestamp
				if diff := cmp.Diff(expectedImageStreamTag, actualImageStreamTag, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name: "app.ci is newer but refers to the same image",
			request: types.NamespacedName{
				Name:      "applyconfig:latest",
				Namespace: "ci",
			},
			apiCIClient: fakeclient.NewFakeClient(applyconfigISTag.DeepCopy()),
			appCIClient: fakeclient.NewFakeClient(applyconfigISTagNewerSameName.DeepCopy(), applyconfigIS.DeepCopy()),

			verify: func(apiCIClient ctrlruntimeclient.Client, appCIClient ctrlruntimeclient.Client) error {
				for clusterName, client := range map[string]ctrlruntimeclient.Client{apiCI: apiCIClient, appCI: appCIClient} {
					actualImageStreamTag := &imagev1.ImageStreamTag{}
					if err := client.Get(ctx, types.NamespacedName{Name: "applyconfig:latest", Namespace: "ci"}, actualImageStreamTag); err != nil {
						return err
					}
					expectedImageStreamTag := &imagev1.ImageStreamTag{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ci",
							Name:      "applyconfig:latest",
						},
						Image: imagev1.Image{
							ObjectMeta: metav1.ObjectMeta{
								Name:              "sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
								CreationTimestamp: now,
							},
							DockerImageReference: "docker-registry.default.svc:5000/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
						},
					}
					if clusterName == appCI {
						expectedImageStreamTag = &imagev1.ImageStreamTag{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "ci",
								Name:      "applyconfig:latest",
							},
							Image: imagev1.Image{
								ObjectMeta: metav1.ObjectMeta{
									Name:              "sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
									CreationTimestamp: threeMinLater,
								},
								DockerImageReference: "image-registry.openshift-image-registry.svc:5000/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
							},
						}
					}
					//ignoring DeletionTimestamp: because it is changed when returning from fakeclient
					expectedImageStreamTag.Image.CreationTimestamp = actualImageStreamTag.Image.CreationTimestamp
					if diff := cmp.Diff(expectedImageStreamTag, actualImageStreamTag, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
						return fmt.Errorf("actual does not match expected, diff: %s", diff)
					}
				}
				return nil
			},
		},
		{
			name: "new IS on api.ci is deleted",
			request: types.NamespacedName{
				Name:      "applyconfig:latest",
				Namespace: "ci",
			},
			apiCIClient: fakeclient.NewFakeClient(applyconfigISTagAppCI.DeepCopy(), applyconfigIS.DeepCopy()),
			appCIClient: fakeclient.NewFakeClient(applyconfigISTagNewer.DeepCopy(), applyconfigISDeleted.DeepCopy()),

			verify: func(apiCIClient ctrlruntimeclient.Client, appCIClient ctrlruntimeclient.Client) error {
				actualImageStream := &imagev1.ImageStream{}
				if err := appCIClient.Get(ctx, types.NamespacedName{Name: "applyconfig", Namespace: "ci"}, actualImageStream); err != nil {
					return err
				}
				expectedImageStream := &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ci",
						Name:      "applyconfig",
						Annotations: map[string]string{
							"release.openshift.io-something": "copied",
							"something":                      "not-copied",
						},
						ResourceVersion:   "1",
						DeletionTimestamp: &now,
					},
				}
				if actualImageStream.DeletionTimestamp == nil {
					t.Errorf("actualImageStream.DeletionTimestamp is nil")
				}
				//ignoring DeletionTimestamp: because it is changed when returning from fakeclient
				expectedImageStream.DeletionTimestamp = actualImageStream.DeletionTimestamp
				if diff := cmp.Diff(expectedImageStream, actualImageStream, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				actualImageStream = &imagev1.ImageStream{}
				if err := apiCIClient.Get(ctx, types.NamespacedName{Name: "applyconfig", Namespace: "ci"}, actualImageStream); !apierrors.IsNotFound(err) {
					return fmt.Errorf("expected NotFound error did not occur and the actual error is: %v", err)
				}
				return nil
			},
		},
		{
			name: "old IS on api.ci is deleted",
			request: types.NamespacedName{
				Name:      "applyconfig:latest",
				Namespace: "ci",
			},
			apiCIClient: fakeclient.NewFakeClient(applyconfigISTag.DeepCopy(), applyconfigISDeleted.DeepCopy()),
			appCIClient: fakeclient.NewFakeClient(applyconfigISTagNewer.DeepCopy(), applyconfigIS.DeepCopy()),

			verify: func(apiCIClient ctrlruntimeclient.Client, appCIClient ctrlruntimeclient.Client) error {
				actualImageStream := &imagev1.ImageStream{}
				if err := apiCIClient.Get(ctx, types.NamespacedName{Name: "applyconfig", Namespace: "ci"}, actualImageStream); err != nil {
					return err
				}
				expectedImageStream := &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ci",
						Name:      "applyconfig",
						Annotations: map[string]string{
							"release.openshift.io-something": "copied",
							"something":                      "not-copied",
						},
						DeletionTimestamp: &now,
						ResourceVersion:   "1",
					},
				}
				if actualImageStream.DeletionTimestamp == nil {
					t.Errorf("actualImageStream.DeletionTimestamp is nil")
				}
				//ignoring DeletionTimestamp: because it is changed when returning from fakeclient
				expectedImageStream.DeletionTimestamp = actualImageStream.DeletionTimestamp
				if diff := cmp.Diff(expectedImageStream, actualImageStream, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				actualImageStream = &imagev1.ImageStream{}
				if err := appCIClient.Get(ctx, types.NamespacedName{Name: "applyconfig", Namespace: "ci"}, actualImageStream); !apierrors.IsNotFound(err) {
					t.Errorf("expected NotFound error did not occur and the actual error is: %v", err)
				}
				return nil
			},
		},
		{
			name: "ISs on both clusters are deleted",
			request: types.NamespacedName{
				Name:      "applyconfig:latest",
				Namespace: "ci",
			},
			apiCIClient: fakeclient.NewFakeClient(applyconfigISTag.DeepCopy(), applyconfigISDeleted.DeepCopy()),
			appCIClient: fakeclient.NewFakeClient(applyconfigISTagNewer.DeepCopy(), applyconfigISDeleted2.DeepCopy()),

			verify: func(apiCIClient ctrlruntimeclient.Client, appCIClient ctrlruntimeclient.Client) error {
				actualImageStream := &imagev1.ImageStream{}
				if err := apiCIClient.Get(ctx, types.NamespacedName{Name: "applyconfig", Namespace: "ci"}, actualImageStream); err != nil {
					return err
				}
				expectedImageStream := &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ci",
						Name:      "applyconfig",
						Annotations: map[string]string{
							"release.openshift.io-something": "copied",
							"something":                      "not-copied",
						},
						DeletionTimestamp: &now,
					},
				}
				if actualImageStream.DeletionTimestamp == nil {
					t.Errorf("actualImageStream.DeletionTimestamp is nil")
				}
				//ignoring DeletionTimestamp: because it is changed when returning from fakeclient
				expectedImageStream.DeletionTimestamp = actualImageStream.DeletionTimestamp
				if diff := cmp.Diff(expectedImageStream, actualImageStream, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}

				actualImageStream = &imagev1.ImageStream{}
				if err := appCIClient.Get(ctx, types.NamespacedName{Name: "applyconfig", Namespace: "ci"}, actualImageStream); err != nil {
					return err
				}
				expectedImageStream = &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ci",
						Name:      "applyconfig",
						Annotations: map[string]string{
							"release.openshift.io-something": "copied",
						},
						DeletionTimestamp: &now,
					},
				}
				if actualImageStream.DeletionTimestamp == nil {
					t.Errorf("actualImageStream.DeletionTimestamp is nil")
				}
				//ignoring DeletionTimestamp: because it is changed when returning from fakeclient
				expectedImageStream.DeletionTimestamp = actualImageStream.DeletionTimestamp
				if diff := cmp.Diff(expectedImageStream, actualImageStream, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name: "old ISTag on api.ci is deleted",
			request: types.NamespacedName{
				Name:      "applyconfig:latest",
				Namespace: "ci",
			},
			apiCIClient: fakeclient.NewFakeClient(applyconfigISTagSoftDeleted.DeepCopy(), applyconfigIS.DeepCopy()),
			appCIClient: fakeclient.NewFakeClient(applyconfigISTagNewer.DeepCopy(), applyconfigIS.DeepCopy()),

			verify: func(apiCIClient ctrlruntimeclient.Client, appCIClient ctrlruntimeclient.Client) error {
				actualImageStream := &imagev1.ImageStream{}
				if err := apiCIClient.Get(ctx, types.NamespacedName{Name: "applyconfig", Namespace: "ci"}, actualImageStream); err != nil {
					return err
				}
				expectedImageStream := &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ci",
						Name:      "applyconfig",
						Annotations: map[string]string{
							"release.openshift.io-something": "copied",
							"something":                      "not-copied",
						},
					},
				}
				if diff := cmp.Diff(expectedImageStream, actualImageStream, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				for _, client := range []ctrlruntimeclient.Client{apiCIClient, appCIClient} {
					actualImageStreamTag := &imagev1.ImageStreamTag{}
					if err := client.Get(ctx, types.NamespacedName{Name: "applyconfig:latest", Namespace: "ci"}, actualImageStreamTag); !apierrors.IsNotFound(err) {
						t.Errorf("expected NotFound error did not occur and the actual error is: %v", err)
					}
				}
				return nil
			},
		},
	} {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &reconciler{
				log: logrus.NewEntry(logrus.New()),
				registryClients: map[string]ctrlruntimeclient.Client{
					apiCI: tc.apiCIClient,
					appCI: tc.appCIClient,
				},
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

func TestTestInputImageStreamTagFilterFactory(t *testing.T) {
	testCases := []struct {
		name                string
		l                   *logrus.Entry
		imageStreamPrefixes sets.String
		deniedImageStreams  sets.String
		nn                  types.NamespacedName
		expected            bool
	}{
		{
			name:     "default",
			nn:       types.NamespacedName{Namespace: "some-namespace", Name: "some-name:some-tag"},
			expected: true,
		},
		{
			name:                "imageStreamPrefixes: true",
			nn:                  types.NamespacedName{Namespace: "openshift", Name: "knative-v0.11.0:knative-eventing-sources-heartbeats-receiver"},
			imageStreamPrefixes: sets.NewString("openshift/knative-"),
			expected:            true,
		},
		{
			name:                "imageStreamPrefixes: false",
			nn:                  types.NamespacedName{Namespace: "openshift", Name: "ruby:2.3"},
			imageStreamPrefixes: sets.NewString("openshift/knative-"),
		},
		{
			name: "not valid isTag name",
			nn:   types.NamespacedName{Namespace: "some-namespace", Name: "not-valid-name"},
		},
		{
			name:               "denied",
			nn:                 types.NamespacedName{Namespace: "ocp", Name: "release:2.3"},
			deniedImageStreams: sets.NewString("ocp/release"),
		},
		{
			name:               "not denied: ocp",
			nn:                 types.NamespacedName{Namespace: "ocp", Name: "ruby:2.3"},
			deniedImageStreams: sets.NewString("ocp/release"),
			expected:           true,
		},
		{
			name:               "not denied: some-namespace",
			nn:                 types.NamespacedName{Namespace: "some-namespace", Name: "ruby:2.3"},
			deniedImageStreams: sets.NewString("ocp/release"),
			expected:           true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.l = logrus.WithField("tc.name", tc.name)
			objectFilter := testInputImageStreamTagFilterFactory(tc.l, tc.imageStreamPrefixes, tc.deniedImageStreams)
			if diff := cmp.Diff(tc.expected, objectFilter(tc.nn)); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
		})
	}
}

func TestImagestream(t *testing.T) {
	testCases := []struct {
		name        string
		imageStream *imagev1.ImageStream
		expected    *imagev1.ImageStream
	}{
		{
			name: "basic case",
			imageStream: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ci",
					Name:      "applyconfig",
					Annotations: map[string]string{
						"release.openshift.io-something": "copied",
						"something":                      "not-copied",
					},
				},
				Spec: imagev1.ImageStreamSpec{
					LookupPolicy: imagev1.ImageLookupPolicy{
						Local: true,
					},
					Tags: []imagev1.TagReference{
						{
							Name: "7.5.0",
							ReferencePolicy: imagev1.TagReferencePolicy{
								Type: imagev1.SourceTagReferencePolicy,
							},
							From: &corev1.ObjectReference{
								Kind: "DockerImage",
								Name: "registry.redhat.io/rhpam-7/rhpam-businesscentral-monitoring-rhel8:7.5.0",
							},
						},
					},
				},
			},
			expected: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ci",
					Name:      "applyconfig",
					Annotations: map[string]string{
						"release.openshift.io-something": "copied",
					},
				},
				Spec: imagev1.ImageStreamSpec{
					LookupPolicy: imagev1.ImageLookupPolicy{
						Local: true,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, fn := imagestream(tc.imageStream)
			if err := fn(); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
		})
	}
}

func TestDockerImageImportedFromTargetingCluster(t *testing.T) {
	testCases := []struct {
		name           string
		cluster        string
		imageStreamTag *imagev1.ImageStreamTag
		expected       bool
	}{
		{
			name:    "api.ci cannot import api.ci",
			cluster: "api.ci",
			imageStreamTag: &imagev1.ImageStreamTag{
				Tag: &imagev1.TagReference{
					From: &corev1.ObjectReference{
						Kind: "DockerImage",
						Name: "registry.svc.ci.openshift.org/ocp/4.7-2020-11-17-181430@sha256:e9edaa5ea72b6e47a796856513368139cd3d0ec03cd26d145c5849e63aa5f0d2",
					},
				},
			},
			expected: true,
		},
		{
			name:    "app.ci cannot import app.ci",
			cluster: "app.ci",
			imageStreamTag: &imagev1.ImageStreamTag{
				Tag: &imagev1.TagReference{
					From: &corev1.ObjectReference{
						Kind: "DockerImage",
						Name: "registry.ci.openshift.org/ocp/4.7-2020-11-17-181430@sha256:e9edaa5ea72b6e47a796856513368139cd3d0ec03cd26d145c5849e63aa5f0d2",
					},
				},
			},
			expected: true,
		},
		{
			name:    "api.ci can import app.ci",
			cluster: "api.ci",
			imageStreamTag: &imagev1.ImageStreamTag{
				Tag: &imagev1.TagReference{
					From: &corev1.ObjectReference{
						Kind: "DockerImage",
						Name: "registry.ci.openshift.org/ocp/4.7-2020-11-17-181430@sha256:e9edaa5ea72b6e47a796856513368139cd3d0ec03cd26d145c5849e63aa5f0d2",
					},
				},
			},
		},
		{
			name:    "app.ci can import api.ci",
			cluster: "app.ci",
			imageStreamTag: &imagev1.ImageStreamTag{
				Tag: &imagev1.TagReference{
					From: &corev1.ObjectReference{
						Kind: "DockerImage",
						Name: "registry.svc.ci.openshift.org/ocp/4.7-2020-11-17-181430@sha256:e9edaa5ea72b6e47a796856513368139cd3d0ec03cd26d145c5849e63aa5f0d2",
					},
				},
			},
		},
		{
			name:    "build01 can import api.ci",
			cluster: "build01",
			imageStreamTag: &imagev1.ImageStreamTag{
				Tag: &imagev1.TagReference{
					From: &corev1.ObjectReference{
						Kind: "DockerImage",
						Name: "registry.svc.ci.openshift.org/ocp/4.7-2020-11-17-181430@sha256:e9edaa5ea72b6e47a796856513368139cd3d0ec03cd26d145c5849e63aa5f0d2",
					},
				},
			},
		},
		{
			name:    "nil isTag",
			cluster: "build01",
		},
		{
			name:           "nil Tag",
			cluster:        "build01",
			imageStreamTag: &imagev1.ImageStreamTag{},
		},
		{
			name:    "nil From",
			cluster: "build01",
			imageStreamTag: &imagev1.ImageStreamTag{
				Tag: &imagev1.TagReference{},
			},
		},
		{
			name:    "Not DockerImage kind",
			cluster: "build01",
			imageStreamTag: &imagev1.ImageStreamTag{
				Tag: &imagev1.TagReference{
					From: &corev1.ObjectReference{
						Kind: "Not DockerImage kind",
						Name: "registry.svc.ci.openshift.org/ocp/4.7-2020-11-17-181430@sha256:e9edaa5ea72b6e47a796856513368139cd3d0ec03cd26d145c5849e63aa5f0d2",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := dockerImageImportedFromTargetingCluster(tc.cluster, tc.imageStreamTag)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
		})
	}
}

func TestEnsureRemoveFinalizer(t *testing.T) {
	imageStream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ci",
			Name:      "applyconfig",
			Annotations: map[string]string{
				"release.openshift.io-something": "copied",
				"something":                      "not-copied",
			},
			Finalizers:      []string{"dptp.openshift.io/registry-syncer", "some"},
			ResourceVersion: "1",
		},
	}

	imageStreamWithoutFinalizer := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ci",
			Name:      "applyconfig",
			Annotations: map[string]string{
				"release.openshift.io-something": "copied",
				"something":                      "not-copied",
			},
			ResourceVersion: "1",
		},
	}

	testCases := []struct {
		name        string
		imageStream *imagev1.ImageStream
		Client      ctrlruntimeclient.Client
		expected    error
		verify      func(client ctrlruntimeclient.Client) error
	}{
		{
			name:        "basic case",
			imageStream: imageStream,
			Client:      fakeclient.NewFakeClient(imageStream.DeepCopy()),
			verify: func(client ctrlruntimeclient.Client) error {
				actualImageStream := &imagev1.ImageStream{}
				if err := client.Get(context.TODO(), types.NamespacedName{Name: "applyconfig", Namespace: "ci"}, actualImageStream); err != nil {
					return err
				}
				expectedImageStream := &imagev1.ImageStream{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ImageStream",
						APIVersion: "image.openshift.io/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ci",
						Name:      "applyconfig",
						//fakeclient actually changes ResourceVersion
						ResourceVersion: "2",
						Annotations: map[string]string{
							"release.openshift.io-something": "copied",
							"something":                      "not-copied",
						},
						Finalizers: []string{"some"},
					},
				}
				if diff := cmp.Diff(expectedImageStream, actualImageStream); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name:        "no patch without finalizer",
			imageStream: imageStream,
			Client:      fakeclient.NewFakeClient(imageStreamWithoutFinalizer.DeepCopy()),
			verify: func(client ctrlruntimeclient.Client) error {
				actualImageStream := &imagev1.ImageStream{}
				if err := client.Get(context.TODO(), types.NamespacedName{Name: "applyconfig", Namespace: "ci"}, actualImageStream); err != nil {
					return err
				}
				expectedImageStream := &imagev1.ImageStream{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ImageStream",
						APIVersion: "image.openshift.io/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "ci",
						Name:            "applyconfig",
						ResourceVersion: "1",
						Annotations: map[string]string{
							"release.openshift.io-something": "copied",
							"something":                      "not-copied",
						},
					},
				}
				if diff := cmp.Diff(expectedImageStream, actualImageStream); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := ensureRemoveFinalizer(context.TODO(), tc.imageStream, tc.Client, logrus.NewEntry(logrus.New()))
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if actual == nil && tc.verify != nil {
				if err := tc.verify(tc.Client); err != nil {
					t.Errorf("unexpcected error: %v", err)
				}
			}
		})
	}
}

func TestEnsureImageStreamTag(t *testing.T) {
	now := metav1.NewTime(time.Now().Add(-24 * 3 * time.Hour))
	ctx := context.Background()

	testCases := []struct {
		name           string
		client         ctrlruntimeclient.Client
		imageStreamTag *imagev1.ImageStreamTag
		expected       error
		verify         func(client ctrlruntimeclient.Client) error
	}{
		{
			name: "basic case",
			imageStreamTag: &imagev1.ImageStreamTag{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "ci",
					Name:        "applyconfig:latest",
					Annotations: map[string]string{"a": "c"},
				},
				Image: imagev1.Image{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
						CreationTimestamp: now,
					},
					DockerImageReference: "docker-registry.default.svc:5000/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
				},
			},
			verify: func(client ctrlruntimeclient.Client) error {
				actualImageStreamTag := &imagev1.ImageStreamTag{}
				if err := client.Get(ctx, types.NamespacedName{Name: "applyconfig:latest", Namespace: "ci"}, actualImageStreamTag); err != nil {
					return err
				}
				expectedImageStreamTag := &imagev1.ImageStreamTag{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:   "ci",
						Name:        "applyconfig:latest",
						Annotations: map[string]string{"a": "c"},
					},
					Tag: &imagev1.TagReference{
						From: &corev1.ObjectReference{
							Kind: "DockerImage",
							Name: "deckerImageReference",
						},
					},
					Image: imagev1.Image{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
							CreationTimestamp: now,
						},
						DockerImageReference: "docker-registry.default.svc:5000/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
					},
				}
				//ignoring DeletionTimestamp: because it is changed when returning from fakeclient
				expectedImageStreamTag.Image.CreationTimestamp = actualImageStreamTag.Image.CreationTimestamp
				if diff := cmp.Diff(expectedImageStreamTag, actualImageStreamTag, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.client = fakeclient.NewFakeClient(tc.imageStreamTag.DeepCopy())
			r := &reconciler{
				log: logrus.NewEntry(logrus.New()),
				registryClients: map[string]ctrlruntimeclient.Client{
					"cluster": tc.client,
				},
			}
			actual := r.ensureImageStreamTag(context.Background(), tc.imageStreamTag, "deckerImageReference", tc.client, logrus.WithField("tc.name", tc.name))
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if actual == nil && tc.verify != nil {
				if err := tc.verify(tc.client); err != nil {
					t.Errorf("unexpcected error: %v", err)
				}
			}
		})
	}
}

func TestHasDueSoftDeleteAnnotation(t *testing.T) {
	now := metav1.NewTime(time.Now())
	oneDayLater := time.Now().Add(24 * time.Hour)
	oneDayBefore := time.Now().Add(-24 * time.Hour)

	testCases := []struct {
		name            string
		isTags          map[string]*imagev1.ImageStreamTag
		expectedCluster string
		expectedOK      bool
		expectedError   error
	}{
		{
			name: "no soft-delete annotation",
			isTags: map[string]*imagev1.ImageStreamTag{
				"a": {
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ci",
						Name:      "applyconfig:latest",
					},
					Image: imagev1.Image{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
							CreationTimestamp: now,
						},
						DockerImageReference: "docker-registry.default.svc:5000/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
					},
				},
			},
		},
		{
			name: "abnormal soft-delete annotation",
			isTags: map[string]*imagev1.ImageStreamTag{
				"a": {
					ObjectMeta: metav1.ObjectMeta{
						Namespace:   "ci",
						Name:        "applyconfig:latest",
						Annotations: map[string]string{"a": "c", "release.openshift.io/soft-delete": "not a timestamp"},
					},
					Image: imagev1.Image{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
							CreationTimestamp: now,
						},
						DockerImageReference: "docker-registry.default.svc:5000/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
					},
				},
			},
			expectedError: &time.ParseError{
				Layout:     "2006-01-02T15:04:05Z07:00",
				Value:      "not a timestamp",
				LayoutElem: "2006",
				ValueElem:  "not a timestamp",
			},
		},
		{
			name: "due soft-delete annotation",
			isTags: map[string]*imagev1.ImageStreamTag{
				"a": {
					ObjectMeta: metav1.ObjectMeta{
						Namespace:   "ci",
						Name:        "applyconfig:latest",
						Annotations: map[string]string{"a": "c", "release.openshift.io/soft-delete": oneDayBefore.Format(time.RFC3339)},
					},
					Image: imagev1.Image{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
							CreationTimestamp: now,
						},
						DockerImageReference: "docker-registry.default.svc:5000/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
					},
				},
			},
			expectedCluster: "a",
			expectedOK:      true,
		},
		{
			name: "not due soft-delete annotation",
			isTags: map[string]*imagev1.ImageStreamTag{
				"a": {
					ObjectMeta: metav1.ObjectMeta{
						Namespace:   "ci",
						Name:        "applyconfig:latest",
						Annotations: map[string]string{"a": "c", "release.openshift.io/soft-delete": oneDayLater.Format(time.RFC3339)},
					},
					Image: imagev1.Image{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
							CreationTimestamp: now,
						},
						DockerImageReference: "docker-registry.default.svc:5000/ci/applyconfig@sha256:4ff455dca5145a078c263ebf716eb1ccd1fe6fd41c9f9de6f27a9af9bbb0349d",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualCluster, actualOK, acutalError := hasDueSoftDeleteAnnotation(tc.isTags)
			if diff := cmp.Diff(tc.expectedCluster, actualCluster); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedOK, actualOK); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedError, acutalError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
		})
	}
}
