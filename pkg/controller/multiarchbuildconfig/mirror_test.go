package multiarchbuildconfig

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1 "github.com/openshift/ci-tools/pkg/api/multiarchbuildconfig/v1"
)

type fakeOCImage struct {
	mirrorFn func(images []string) error
}

func (foci *fakeOCImage) mirror(images []string) error {
	return foci.mirrorFn(images)
}

func newFakeOCImage(mirrorFn func(images []string) error) *fakeOCImage {
	return &fakeOCImage{
		mirrorFn: mirrorFn,
	}
}

func TestOCImageMirrorArgs(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		src    string
		output []string
		want   []string
	}{
		{
			name:   "Mirror to one destination",
			src:    "src-registry.com/src-image:latest",
			output: []string{"dst-registry.com/dst-image:latest"},
			want:   []string{"src-registry.com/src-image:latest", "dst-registry.com/dst-image:latest"},
		},
		{
			name:   "Mirror to multiple destinations",
			src:    "src-registry.com/src-image:latest",
			output: []string{"dst-registry.com/dst-image-1:latest", "dst-registry.com/dst-image-2:latest"},
			want: []string{
				"src-registry.com/src-image:latest",
				"dst-registry.com/dst-image-1:latest",
				"dst-registry.com/dst-image-2:latest",
			},
		},
		{
			name: "Deduplicate destinations",
			src:  "src-registry.com/src-image:latest",
			output: []string{
				"dst-registry.com/dst-image-1:latest",
				"dst-registry.com/dst-image-2:latest",
				"dst-registry.com/dst-image-2:latest",
			},
			want: []string{
				"src-registry.com/src-image:latest",
				"dst-registry.com/dst-image-1:latest",
				"dst-registry.com/dst-image-2:latest",
			},
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			args := ocImageMirrorArgs(testCase.src, testCase.output)
			if diff := cmp.Diff(args, testCase.want); diff != "" {
				t.Errorf("Unexpected diff:\n%s", diff)
			}
		})
	}
}

func TestHandleMirrorImage(t *testing.T) {
	pushImageManifestCondition := &metav1.Condition{
		Type:   PushImageManifestDone,
		Status: metav1.ConditionTrue,
	}
	imageMirrorCmdFactory := func(err error) func([]string) error {
		return func([]string) error { return err }
	}

	for _, testCase := range []struct {
		name           string
		mabc           v1.MultiArchBuildConfig
		imageMirrorErr error
		want           v1.MultiArchBuildConfigStatus
	}{
		{
			name: "No output set, do nothing",
			mabc: v1.MultiArchBuildConfig{
				Spec: v1.MultiArchBuildConfigSpec{
					ExternalRegistries: []string{},
				},
				Status: v1.MultiArchBuildConfigStatus{State: ""},
			},
			want: v1.MultiArchBuildConfigStatus{State: ""},
		},
		{
			name: "Status shows image has been mirrored already, do nothing",
			mabc: v1.MultiArchBuildConfig{
				Spec: v1.MultiArchBuildConfigSpec{
					ExternalRegistries: []string{},
				},
				Status: v1.MultiArchBuildConfigStatus{
					Conditions: []metav1.Condition{
						*pushImageManifestCondition,
						{
							Type:               MirrorImageManifestDone,
							Status:             metav1.ConditionTrue,
							LastTransitionTime: metav1.Time{Time: time.Time{}},
							Reason:             ImageMirrorSuccessReason,
						},
					},
				},
			},
			want: v1.MultiArchBuildConfigStatus{
				Conditions: []metav1.Condition{
					*pushImageManifestCondition,
					{
						Type:               MirrorImageManifestDone,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Time{Time: time.Time{}},
						Reason:             ImageMirrorSuccessReason,
					},
				},
			},
		},
		{
			name: "Mirror completed successfully, set status to success",
			mabc: v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mabc-1",
					Namespace: "ns",
				},
				Spec: v1.MultiArchBuildConfigSpec{
					ExternalRegistries: []string{"dst-reg.com/dst-image:latest"},
				},
				Status: v1.MultiArchBuildConfigStatus{
					State: "",
					Conditions: []metav1.Condition{
						*pushImageManifestCondition,
					},
				},
			},
			want: v1.MultiArchBuildConfigStatus{
				Conditions: []metav1.Condition{
					*pushImageManifestCondition,
					{
						Type:               MirrorImageManifestDone,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Time{Time: time.Time{}},
						Reason:             ImageMirrorSuccessReason,
					},
				},
				State: v1.SuccessState,
			},
		},
		{
			name: "Mirror failed, set status to failed",
			mabc: v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mabc-1",
					Namespace: "ns",
				},
				Spec: v1.MultiArchBuildConfigSpec{
					ExternalRegistries: []string{"dst-reg.com/dst-image:latest"},
				},
				Status: v1.MultiArchBuildConfigStatus{
					State: "",
					Conditions: []metav1.Condition{
						*pushImageManifestCondition,
					},
				},
			},
			imageMirrorErr: errors.New("an error"),
			want: v1.MultiArchBuildConfigStatus{
				Conditions: []metav1.Condition{
					*pushImageManifestCondition,
					{
						Type:               MirrorImageManifestDone,
						Status:             metav1.ConditionFalse,
						LastTransitionTime: metav1.Time{Time: time.Time{}},
						Reason:             ImageMirrorErrorReason,
						Message:            "oc image mirror: an error",
					},
				},
				State: v1.FailureState,
			},
		},
	} {
		testCase := testCase

		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			client := fake.NewClientBuilder().WithObjects(&testCase.mabc).Build()
			r := reconciler{
				logger:        logrus.NewEntry(logrus.StandardLogger()),
				client:        client,
				imageMirrorer: newFakeOCImage(imageMirrorCmdFactory(testCase.imageMirrorErr)),
			}

			if err := r.handleMirrorImage(context.TODO(), "fake-image", &testCase.mabc); err != nil {
				t.Fatalf("Failed to mirror %v", err)
			}

			actualMabc := &v1.MultiArchBuildConfig{}
			if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Name: testCase.mabc.Name, Namespace: testCase.mabc.Namespace}, actualMabc); err != nil {
				t.Fatalf("Failed to retrieve MultiArchBuildConfig: %v", err)
			}

			if diff := cmp.Diff(testCase.want, actualMabc.Status,
				cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime"),
			); diff != "" {
				t.Errorf("unexpected mabc:\n%s", diff)
			}
		})
	}
}
