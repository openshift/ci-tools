package multiarchbuildconfig

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	buildv1 "github.com/openshift/api/build/v1"

	v1 "github.com/openshift/ci-tools/pkg/api/multiarchbuildconfig/v1"
	"github.com/openshift/ci-tools/pkg/manifestpusher"
)

func TestCheckAllBuildsFinished(t *testing.T) {
	tests := []struct {
		name     string
		builds   map[string]*buildv1.Build
		expected bool
	}{
		{
			name: "AllBuildsComplete",
			builds: map[string]*buildv1.Build{
				"build1": {Status: buildv1.BuildStatus{Phase: buildv1.BuildPhaseComplete}},
				"build2": {Status: buildv1.BuildStatus{Phase: buildv1.BuildPhaseComplete}},
			},
			expected: true,
		},
		{
			name: "AllBuildsFailed",
			builds: map[string]*buildv1.Build{
				"build1": {Status: buildv1.BuildStatus{Phase: buildv1.BuildPhaseFailed}},
				"build2": {Status: buildv1.BuildStatus{Phase: buildv1.BuildPhaseFailed}},
			},
			expected: true,
		},
		{
			name: "MixOfAllowedStatuses",
			builds: map[string]*buildv1.Build{
				"build1": {Status: buildv1.BuildStatus{Phase: buildv1.BuildPhaseComplete}},
				"build2": {Status: buildv1.BuildStatus{Phase: buildv1.BuildPhaseFailed}},
			},
			expected: true,
		},
		{
			name: "WithOtherStatus",
			builds: map[string]*buildv1.Build{
				"build1": {Status: buildv1.BuildStatus{Phase: buildv1.BuildPhaseComplete}},
				"build2": {Status: buildv1.BuildStatus{Phase: "OtherStatus"}},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(checkAllBuildsFinished(tt.builds), tt.expected); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestCheckAllBuildsSuccessful(t *testing.T) {
	tests := []struct {
		name     string
		builds   map[string]*buildv1.Build
		expected bool
	}{
		{
			name: "AllBuildsComplete",
			builds: map[string]*buildv1.Build{
				"build1": {Status: buildv1.BuildStatus{Phase: buildv1.BuildPhaseComplete}},
				"build2": {Status: buildv1.BuildStatus{Phase: buildv1.BuildPhaseComplete}},
			},
			expected: true,
		},
		{
			name: "AtLeastOneBuildNotComplete",
			builds: map[string]*buildv1.Build{
				"build1": {Status: buildv1.BuildStatus{Phase: buildv1.BuildPhaseComplete}},
				"build2": {Status: buildv1.BuildStatus{Phase: "SomeOtherPhase"}},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(checkAllBuildsSuccessful(tt.builds), tt.expected); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestBuildOwnerReference(t *testing.T) {
	mabc := &v1.MultiArchBuildConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mabc",
			Namespace: "test-ns",
		},
		Spec: v1.MultiArchBuildConfigSpec{
			BuildSpec: buildv1.BuildConfigSpec{
				CommonSpec: buildv1.CommonSpec{
					Output: buildv1.BuildOutput{
						To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"},
					},
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	sb := runtime.NewSchemeBuilder(v1.AddToScheme, buildv1.AddToScheme)
	if err := sb.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}
	client := fake.NewClientBuilder().WithObjects(mabc).WithScheme(scheme).Build()
	r := &reconciler{
		logger:        logrus.NewEntry(logrus.StandardLogger()),
		client:        client,
		architectures: []string{"amd64", "arm64"},
		scheme:        scheme,
	}

	nn := types.NamespacedName{Name: mabc.Name, Namespace: mabc.Namespace}
	if err := r.reconcile(context.TODO(), reconcile.Request{NamespacedName: nn}, r.logger); err != nil {
		t.Fatalf("Failed to reconcile: %v", err)
	}

	builds := buildv1.BuildList{}
	if err := client.List(context.TODO(), &builds); err != nil {
		t.Fatalf("Failed to get builds: %v", err)
	}

	wantBuilds := buildv1.BuildList{
		Items: []buildv1.Build{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mabc-amd64",
					Namespace: "test-ns",
					Labels: map[string]string{
						"multiarchbuildconfigs.ci.openshift.io/arch": "amd64",
						"multiarchbuildconfigs.ci.openshift.io/name": "test-mabc",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         "ci.openshift.io/v1",
							Kind:               "MultiArchBuildConfig",
							Name:               "test-mabc",
							Controller:         pointer.Bool(true),
							BlockOwnerDeletion: pointer.Bool(true),
						},
					},
					ResourceVersion: "1",
					Generation:      0,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mabc-arm64",
					Namespace: "test-ns",
					Labels: map[string]string{
						"multiarchbuildconfigs.ci.openshift.io/arch": "arm64",
						"multiarchbuildconfigs.ci.openshift.io/name": "test-mabc",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         "ci.openshift.io/v1",
							Kind:               "MultiArchBuildConfig",
							Name:               "test-mabc",
							Controller:         pointer.Bool(true),
							BlockOwnerDeletion: pointer.Bool(true),
						},
					},
					ResourceVersion: "1",
					Generation:      0,
				},
			},
		},
	}

	if diff := cmp.Diff(wantBuilds, builds,
		cmpopts.IgnoreFields(buildv1.BuildList{}, "TypeMeta", "ListMeta"),
		cmpopts.IgnoreFields(buildv1.Build{}, "Spec", "Kind"),
	); diff != "" {
		t.Error(diff)
	}
}

func TestReconcile(t *testing.T) {
	tests := []struct {
		name           string
		inputMabc      *v1.MultiArchBuildConfig
		expectedMabc   *v1.MultiArchBuildConfig
		manifestPusher manifestpusher.ManifestPusher
	}{
		{
			name: "Early exit on SuccessState",
			inputMabc: &v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mabc",
					Namespace: "test-ns",
				},
				Spec: v1.MultiArchBuildConfigSpec{
					BuildSpec: buildv1.BuildConfigSpec{
						CommonSpec: buildv1.CommonSpec{Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"}}},
					},
				},
				Status: v1.MultiArchBuildConfigStatus{
					State: v1.SuccessState,
				},
			},
			expectedMabc: &v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "test-mabc", Namespace: "test-ns"},
				Spec: v1.MultiArchBuildConfigSpec{
					BuildSpec: buildv1.BuildConfigSpec{
						CommonSpec: buildv1.CommonSpec{Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"}}},
					},
				},
				Status: v1.MultiArchBuildConfigStatus{
					State: v1.SuccessState,
				},
			},
		},
		{
			name: "FailureState on build failure",
			inputMabc: &v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "test-mabc", Namespace: "test-ns"},
				Spec: v1.MultiArchBuildConfigSpec{
					BuildSpec: buildv1.BuildConfigSpec{
						CommonSpec: buildv1.CommonSpec{Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"}}},
					},
				},
				Status: v1.MultiArchBuildConfigStatus{
					Builds: map[string]*buildv1.Build{
						"test-build": {Status: buildv1.BuildStatus{Phase: buildv1.BuildPhaseFailed}},
					},
				},
			},
			expectedMabc: &v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "test-mabc", Namespace: "test-ns"},
				Spec: v1.MultiArchBuildConfigSpec{
					BuildSpec: buildv1.BuildConfigSpec{
						CommonSpec: buildv1.CommonSpec{Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"}}},
					},
				},
				Status: v1.MultiArchBuildConfigStatus{
					State: v1.FailureState,
					Builds: map[string]*buildv1.Build{
						"test-build": {Status: buildv1.BuildStatus{Phase: buildv1.BuildPhaseFailed}},
					},
				},
			},
		},
		{
			name:           "Condition added on manifest push error",
			manifestPusher: &mockManifestPusher{errToReturn: fmt.Errorf("test error")},
			inputMabc: &v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mabc",
					Namespace: "test-ns",
				},
				Spec: v1.MultiArchBuildConfigSpec{
					BuildSpec: buildv1.BuildConfigSpec{
						CommonSpec: buildv1.CommonSpec{Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"}}},
					},
				},
				Status: v1.MultiArchBuildConfigStatus{
					Builds: map[string]*buildv1.Build{
						"test-build": {
							Status: buildv1.BuildStatus{
								Phase: buildv1.BuildPhaseComplete,
							},
						},
					},
				},
			},
			expectedMabc: &v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mabc",
					Namespace: "test-ns",
				},
				Spec: v1.MultiArchBuildConfigSpec{
					BuildSpec: buildv1.BuildConfigSpec{
						CommonSpec: buildv1.CommonSpec{Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"}}},
					},
				},
				Status: v1.MultiArchBuildConfigStatus{
					State: v1.FailureState,
					Builds: map[string]*buildv1.Build{
						"test-build": {
							Status: buildv1.BuildStatus{
								Phase: buildv1.BuildPhaseComplete,
							},
						},
					},
					Conditions: []metav1.Condition{
						{
							Type:    PushImageManifestDone,
							Status:  metav1.ConditionFalse,
							Reason:  "PushManifestError",
							Message: "test error",
						},
					},
				},
			},
		},
		{
			name:           "Condition added on manifest push success",
			manifestPusher: &mockManifestPusher{},
			inputMabc: &v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mabc",
					Namespace: "test-ns",
				},
				Spec: v1.MultiArchBuildConfigSpec{
					BuildSpec: buildv1.BuildConfigSpec{
						CommonSpec: buildv1.CommonSpec{Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"}}},
					},
				},
				Status: v1.MultiArchBuildConfigStatus{
					Builds: map[string]*buildv1.Build{
						"test-build": {
							Status: buildv1.BuildStatus{
								Phase: buildv1.BuildPhaseComplete,
							},
						},
					},
				},
			},
			expectedMabc: &v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mabc",
					Namespace: "test-ns",
				},
				Spec: v1.MultiArchBuildConfigSpec{
					BuildSpec: buildv1.BuildConfigSpec{
						CommonSpec: buildv1.CommonSpec{Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"}}},
					},
				},
				Status: v1.MultiArchBuildConfigStatus{
					Builds: map[string]*buildv1.Build{
						"test-build": {
							Status: buildv1.BuildStatus{
								Phase: buildv1.BuildPhaseComplete,
							},
						},
					},
					Conditions: []metav1.Condition{
						{
							Type:   PushImageManifestDone,
							Status: metav1.ConditionTrue,
							Reason: "PushManifestSuccess",
						},
					},
				},
			},
		},
		{
			name: "Conditions added when image mirror succeeded",
			inputMabc: &v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mabc",
					Namespace: "test-ns",
				},
				Spec: v1.MultiArchBuildConfigSpec{
					BuildSpec: buildv1.BuildConfigSpec{
						CommonSpec: buildv1.CommonSpec{Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"}}},
					},
					ExternalRegistries: []string{"foo-registry.com/foo/bar:latest"},
				},
				Status: v1.MultiArchBuildConfigStatus{
					Builds: map[string]*buildv1.Build{
						"test-build": {
							Status: buildv1.BuildStatus{
								Phase: buildv1.BuildPhaseComplete,
							},
						},
					},
					Conditions: []metav1.Condition{
						{
							Type:   PushImageManifestDone,
							Status: metav1.ConditionTrue,
							Reason: "PushManifestSuccess",
						},
					},
				},
			},
			expectedMabc: &v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mabc",
					Namespace: "test-ns",
				},
				Spec: v1.MultiArchBuildConfigSpec{
					BuildSpec: buildv1.BuildConfigSpec{
						CommonSpec: buildv1.CommonSpec{Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"}}},
					},
					ExternalRegistries: []string{"foo-registry.com/foo/bar:latest"},
				},
				Status: v1.MultiArchBuildConfigStatus{
					Builds: map[string]*buildv1.Build{
						"test-build": {
							Status: buildv1.BuildStatus{
								Phase: buildv1.BuildPhaseComplete,
							},
						},
					},
					Conditions: []metav1.Condition{
						{
							Type:   PushImageManifestDone,
							Status: metav1.ConditionTrue,
							Reason: "PushManifestSuccess",
						},
						{
							Type:   MirrorImageManifestDone,
							Status: metav1.ConditionTrue,
							Reason: ImageMirrorSuccessReason,
						},
					},
					State: v1.SuccessState,
				},
			},
			manifestPusher: &mockManifestPusher{},
		},
		{
			name: "Deletion in place do nothing",
			inputMabc: &v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-mabc",
					Namespace:         "test-ns",
					DeletionTimestamp: &metav1.Time{Time: time.Date(2023, 11, 8, 9, 45, 0, 0, time.Local)},
					Finalizers:        []string{"foo"},
				},
				Spec: v1.MultiArchBuildConfigSpec{
					BuildSpec: buildv1.BuildConfigSpec{
						CommonSpec: buildv1.CommonSpec{Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"}}},
					},
					ExternalRegistries: []string{"foo-registry.com/foo/bar:latest"},
				},
				Status: v1.MultiArchBuildConfigStatus{
					Builds: map[string]*buildv1.Build{
						"test-build": {
							Status: buildv1.BuildStatus{
								Phase: buildv1.BuildPhaseComplete,
							},
						},
					},
					Conditions: []metav1.Condition{
						{
							Type:   PushImageManifestDone,
							Status: metav1.ConditionTrue,
							Reason: "PushManifestSuccess",
						},
					},
					State: "doesntmatter",
				},
			},
			expectedMabc: &v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-mabc",
					Namespace:         "test-ns",
					DeletionTimestamp: &metav1.Time{Time: time.Date(2023, 11, 8, 9, 45, 0, 0, time.Local)},
					Finalizers:        []string{"foo"},
				},
				Spec: v1.MultiArchBuildConfigSpec{
					BuildSpec: buildv1.BuildConfigSpec{
						CommonSpec: buildv1.CommonSpec{Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"}}},
					},
					ExternalRegistries: []string{"foo-registry.com/foo/bar:latest"},
				},
				Status: v1.MultiArchBuildConfigStatus{
					Builds: map[string]*buildv1.Build{
						"test-build": {
							Status: buildv1.BuildStatus{
								Phase: buildv1.BuildPhaseComplete,
							},
						},
					},
					Conditions: []metav1.Condition{
						{
							Type:   PushImageManifestDone,
							Status: metav1.ConditionTrue,
							Reason: "PushManifestSuccess",
						},
					},
					State: "doesntmatter",
				},
			},
			manifestPusher: &mockManifestPusher{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithObjects(tt.inputMabc).Build()

			r := &reconciler{
				logger:         logrus.NewEntry(logrus.StandardLogger()),
				client:         client,
				architectures:  []string{"amd64", "arm64"},
				manifestPusher: tt.manifestPusher,
				imageMirrorer:  newFakeOCImage(func(images []string) error { return nil }),
			}

			if err := r.reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: tt.inputMabc.Name, Namespace: tt.inputMabc.Namespace}}, r.logger); err != nil {
				t.Fatalf("Failed to reconcile: %v", err)
			}

			actualMabc := &v1.MultiArchBuildConfig{}
			if err := client.Get(context.Background(), ctrlruntimeclient.ObjectKey{Name: tt.inputMabc.Name, Namespace: tt.inputMabc.Namespace}, actualMabc); err != nil {
				t.Fatalf("Failed to retrieve MultiArchBuildConfig: %v", err)
			}

			if diff := cmp.Diff(tt.expectedMabc, actualMabc,
				cmpopts.IgnoreFields(metav1.TypeMeta{}, "APIVersion", "Kind"),
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion"),
				cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime"),
			); diff != "" {
				t.Error(diff)
			}
		})
	}
}

type mockManifestPusher struct {
	errToReturn error
}

func (m *mockManifestPusher) PushImageWithManifest(builds map[string]*buildv1.Build, targetImageRef string) error {
	return m.errToReturn
}
