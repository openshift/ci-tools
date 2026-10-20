package multiarchbuildconfig

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		name               string
		buildImageRef      string
		targetImageRef     string
		externalRegistries []string
		want               []string
	}{
		{
			name:               "Mirror to one destination",
			buildImageRef:      "ci/src-image:latest",
			targetImageRef:     "ci/src-image:latest",
			externalRegistries: []string{"dst-registry.com"},
			want:               []string{"image-registry.openshift-image-registry.svc:5000/ci/src-image:latest", "dst-registry.com/ci/src-image:latest"},
		},
		{
			name:               "Mirror to multiple external registries",
			buildImageRef:      "ci/src-image:latest",
			targetImageRef:     "ci/src-image:latest",
			externalRegistries: []string{"dst-registry-1.com", "dst-registry-2.com", "quay.io/openshift/ci"},
			want: []string{
				"image-registry.openshift-image-registry.svc:5000/ci/src-image:latest",
				"dst-registry-1.com/ci/src-image:latest",
				"dst-registry-2.com/ci/src-image:latest",
				"quay.io/openshift/ci:ci_src-image_latest",
			},
		},
		{
			name:           "Deduplicate destinations",
			buildImageRef:  "ci/src-image:latest",
			targetImageRef: "ci/src-image:latest",
			externalRegistries: []string{
				"dst-registry-1.com",
				"dst-registry-1.com",
				"dst-registry-3.com",
			},
			want: []string{
				"image-registry.openshift-image-registry.svc:5000/ci/src-image:latest",
				"dst-registry-1.com/ci/src-image:latest",
				"dst-registry-3.com/ci/src-image:latest",
			},
		},
		{
			name:               "Mirror image built in ci to requested namespace tag on Quay",
			buildImageRef:      "ci/ocp-cli-yq:latest",
			targetImageRef:     "ocp/cli-yq:latest",
			externalRegistries: []string{"quay.io/openshift/ci"},
			want: []string{
				"image-registry.openshift-image-registry.svc:5000/ci/ocp-cli-yq:latest",
				"quay.io/openshift/ci:ocp_cli-yq_latest",
			},
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			args := ocImageMirrorArgs(testCase.buildImageRef, testCase.targetImageRef, testCase.externalRegistries)
			if diff := cmp.Diff(testCase.want, args); diff != "" {
				t.Errorf("mirror arguments (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHandleMirrorImage(t *testing.T) {
	imageMirrorCmdFactory := func(err error) func([]string) error {
		return func([]string) error { return err }
	}

	for _, tc := range []struct {
		name       string
		mabc       v1.MultiArchBuildConfig
		wantErr    error
		wantStatus v1.MultiArchBuildConfigStatus
	}{
		{
			name: "No output set, do nothing",
			mabc: v1.MultiArchBuildConfig{
				Spec: v1.MultiArchBuildConfigSpec{
					ExternalRegistries: []string{},
				},
				Status: v1.MultiArchBuildConfigStatus{State: ""},
			},
			wantStatus: v1.MultiArchBuildConfigStatus{
				Conditions: []metav1.Condition{{
					Type:    MirrorImageManifestDone,
					Status:  metav1.ConditionTrue,
					Reason:  ImageMirrorSkipedReason,
					Message: ImageMirrorNoExtRegistriesMsg,
				}},
				State: "",
			},
		},
		{
			name: "No registries, do nothing",
			mabc: v1.MultiArchBuildConfig{
				Spec: v1.MultiArchBuildConfigSpec{
					ExternalRegistries: []string{},
				},
			},
			wantStatus: v1.MultiArchBuildConfigStatus{
				Conditions: []metav1.Condition{{
					Type:    MirrorImageManifestDone,
					Status:  metav1.ConditionTrue,
					Reason:  ImageMirrorSkipedReason,
					Message: ImageMirrorNoExtRegistriesMsg,
				}},
			},
		},
		{
			name: "Mirror completed successfully, add condition",
			mabc: v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mabc-1",
					Namespace: "ns",
				},
				Spec: v1.MultiArchBuildConfigSpec{
					ExternalRegistries: []string{"dst-reg.com/dst-image:latest"},
				},
				Status: v1.MultiArchBuildConfigStatus{},
			},
			wantStatus: v1.MultiArchBuildConfigStatus{
				Conditions: []metav1.Condition{{
					Type:               MirrorImageManifestDone,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Time{Time: time.Time{}},
					Reason:             ImageMirrorSuccessReason,
					Message:            ImageMirrorSuccessMessage,
				}},
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
				Status: v1.MultiArchBuildConfigStatus{},
			},
			wantErr: errors.New("an error"),
			wantStatus: v1.MultiArchBuildConfigStatus{
				Conditions: []metav1.Condition{{
					Type:               MirrorImageManifestDone,
					Status:             metav1.ConditionFalse,
					LastTransitionTime: metav1.Time{Time: time.Time{}},
					Reason:             ImageMirrorErrorReason,
					Message:            "oc image mirror: an error",
				}},
				State: v1.FailureState,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := fake.NewClientBuilder().WithObjects(&tc.mabc).Build()
			r := reconciler{
				logger:        logrus.NewEntry(logrus.StandardLogger()),
				client:        client,
				imageMirrorer: newFakeOCImage(imageMirrorCmdFactory(tc.wantErr)),
			}

			observedStatus := v1.MultiArchBuildConfigStatus{}
			gotErr := r.handleMirrorImage(logrus.NewEntry(logrus.StandardLogger()), "fake-image", "fake-image", &tc.mabc, &observedStatus)

			goErrMsg, wantErrMsg := "<nil>", "<nil>"
			if gotErr != nil {
				goErrMsg = gotErr.Error()
			}
			if tc.wantErr != nil {
				wantErrMsg = tc.wantErr.Error()
			}

			if diff := cmp.Diff(wantErrMsg, goErrMsg); diff != "" {
				t.Fatalf("unexpected err: %s", diff)
			}

			if diff := cmp.Diff(tc.wantStatus, observedStatus,
				cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime"),
			); diff != "" {
				t.Errorf("unexpected mabc:\n%s", diff)
			}
		})
	}
}
