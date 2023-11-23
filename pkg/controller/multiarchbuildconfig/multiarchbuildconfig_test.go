package multiarchbuildconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	buildv1 "github.com/openshift/api/build/v1"

	v1 "github.com/openshift/ci-tools/pkg/api/multiarchbuildconfig/v1"
	"github.com/openshift/ci-tools/pkg/manifestpusher"
)

var (
	scheme *runtime.Scheme
)

func init() {
	scheme = runtime.NewScheme()
	sb := runtime.NewSchemeBuilder(v1.AddToScheme, buildv1.Install)
	if err := sb.AddToScheme(scheme); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to add scheme: %v", err)
		os.Exit(1)
	}
}

type mockManifestPusher struct {
	errToReturn error
}

func (m *mockManifestPusher) PushImageWithManifest(builds []buildv1.Build, targetImageRef string) error {
	return m.errToReturn
}

type buildBuilder struct {
	name     string
	arch     string
	phase    buildv1.BuildPhase
	mabcName string
}

func NewBuildBuilder() *buildBuilder {
	return &buildBuilder{mabcName: "foo"}
}

func (bb *buildBuilder) Name(name string) *buildBuilder {
	bb.name = name
	return bb
}

func (bb *buildBuilder) Arch(arch string) *buildBuilder {
	bb.arch = arch
	return bb
}

func (bb *buildBuilder) Phase(phase buildv1.BuildPhase) *buildBuilder {
	bb.phase = phase
	return bb
}

func (bb *buildBuilder) MABCName(mabcName string) *buildBuilder {
	bb.mabcName = mabcName
	return bb
}

func (bb *buildBuilder) Build() buildv1.Build {
	return buildv1.Build{
		ObjectMeta: metav1.ObjectMeta{
			Name: bb.name,
			Labels: map[string]string{
				v1.MultiArchBuildConfigNameLabel: bb.mabcName,
				v1.MultiArchBuildConfigArchLabel: bb.arch,
			},
		},
		Status: buildv1.BuildStatus{Phase: bb.phase},
	}
}

func TestCheckAllBuildsFinished(t *testing.T) {
	tests := []struct {
		name     string
		builds   *buildv1.BuildList
		expected bool
	}{
		{
			name: "AllBuildsComplete",
			builds: &buildv1.BuildList{
				Items: []buildv1.Build{
					NewBuildBuilder().Name("build0").Arch("amd64").Phase(buildv1.BuildPhaseComplete).Build(),
					NewBuildBuilder().Name("build1").Arch("arm64").Phase(buildv1.BuildPhaseComplete).Build(),
				},
			},
			expected: true,
		},
		{
			name: "AllBuildsFailed",
			builds: &buildv1.BuildList{
				Items: []buildv1.Build{
					NewBuildBuilder().Name("build0").Arch("amd64").Phase(buildv1.BuildPhaseFailed).Build(),
					NewBuildBuilder().Name("build1").Arch("arm64").Phase(buildv1.BuildPhaseFailed).Build(),
				},
			},
			expected: true,
		},
		{
			name: "MixOfAllowedStatuses",
			builds: &buildv1.BuildList{
				Items: []buildv1.Build{
					NewBuildBuilder().Name("build0").Arch("amd64").Phase(buildv1.BuildPhaseComplete).Build(),
					NewBuildBuilder().Name("build1").Arch("arm64").Phase(buildv1.BuildPhaseFailed).Build(),
				},
			},
			expected: true,
		},
		{
			name: "WithOtherStatus",
			builds: &buildv1.BuildList{
				Items: []buildv1.Build{
					NewBuildBuilder().Name("build0").Arch("amd64").Phase(buildv1.BuildPhaseComplete).Build(),
					NewBuildBuilder().Name("build1").Arch("arm64").Phase(buildv1.BuildPhaseRunning).Build(),
				},
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
		builds   *buildv1.BuildList
		expected bool
	}{
		{
			name: "AllBuildsComplete",
			builds: &buildv1.BuildList{
				Items: []buildv1.Build{
					NewBuildBuilder().Name("build0").Arch("amd64").Phase(buildv1.BuildPhaseComplete).Build(),
					NewBuildBuilder().Name("build1").Arch("arm64").Phase(buildv1.BuildPhaseComplete).Build(),
				},
			},
			expected: true,
		},
		{
			name: "AtLeastOneBuildNotComplete",
			builds: &buildv1.BuildList{
				Items: []buildv1.Build{
					NewBuildBuilder().Name("build0").Arch("amd64").Phase(buildv1.BuildPhaseComplete).Build(),
					NewBuildBuilder().Name("build1").Arch("arm64").Phase(buildv1.BuildPhasePending).Build(),
				},
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
	makeBuilds := func(mabcName string) *buildv1.BuildList {
		return &buildv1.BuildList{
			Items: []buildv1.Build{
				NewBuildBuilder().Name("build0").Arch("amd64").MABCName(mabcName).Phase(buildv1.BuildPhaseComplete).Build(),
				NewBuildBuilder().Name("build1").Arch("arm64").MABCName(mabcName).Phase(buildv1.BuildPhaseComplete).Build(),
			},
		}
	}
	createInterceptorFactory := func(failOnBuildCreate bool) func(ctx context.Context, client ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
		return func(ctx context.Context, client ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
			if _, ok := obj.(*buildv1.Build); ok && failOnBuildCreate {
				return errors.New("planned failure")
			}
			return nil
		}
	}

	tests := []struct {
		name              string
		failOnBuildCreate bool
		inputMabc         *v1.MultiArchBuildConfig
		builds            *buildv1.BuildList
		expectedMabc      *v1.MultiArchBuildConfig
		expectedErr       error
		manifestPusher    manifestpusher.ManifestPusher
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
			},
			builds: &buildv1.BuildList{
				Items: []buildv1.Build{
					NewBuildBuilder().Name("build0").Arch("amd64").MABCName("test-mabc").Phase(buildv1.BuildPhaseFailed).Build(),
					NewBuildBuilder().Name("build1").Arch("arm64").MABCName("test-mabc").Phase(buildv1.BuildPhaseComplete).Build(),
				},
			},
			expectedMabc: &v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "test-mabc", Namespace: "test-ns"},
				Spec: v1.MultiArchBuildConfigSpec{
					BuildSpec: buildv1.BuildConfigSpec{
						CommonSpec: buildv1.CommonSpec{Output: buildv1.BuildOutput{To: &corev1.ObjectReference{Namespace: "test-ns", Name: "test-image"}}},
					},
				},
				Status: v1.MultiArchBuildConfigStatus{State: v1.FailureState},
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
		{
			name:              "Fails if it isn't able to spawn builds",
			builds:            &buildv1.BuildList{},
			failOnBuildCreate: true,
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
				Status: v1.MultiArchBuildConfigStatus{State: v1.FailureState},
			},
			expectedErr: errors.New("couldn't create builds for architectures: amd64,arm64: couldn't create build test-ns/test-mabc-amd64: planned failure"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.builds == nil {
				tt.builds = makeBuilds(tt.inputMabc.Name)
			}
			client := fake.NewClientBuilder().
				WithObjects(tt.inputMabc).
				WithScheme(scheme).
				WithLists(tt.builds).
				WithInterceptorFuncs(interceptor.Funcs{Create: createInterceptorFactory(tt.failOnBuildCreate)}).
				Build()

			r := &reconciler{
				logger:         logrus.NewEntry(logrus.StandardLogger()),
				client:         client,
				architectures:  []string{"amd64", "arm64"},
				manifestPusher: tt.manifestPusher,
				imageMirrorer:  newFakeOCImage(func(images []string) error { return nil }),
				scheme:         scheme,
			}

			err := r.reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: tt.inputMabc.Name, Namespace: tt.inputMabc.Namespace}}, r.logger)
			if err != nil && tt.expectedErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tt.expectedErr != nil {
				t.Fatalf("want err %v but nil", tt.expectedErr)
			}
			if err != nil && tt.expectedErr != nil {
				if diff := cmp.Diff(tt.expectedErr.Error(), err.Error()); diff != "" {
					t.Fatalf("unexpected error: %s", diff)
				}
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
