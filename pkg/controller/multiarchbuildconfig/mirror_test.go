package multiarchbuildconfig

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	v1 "github.com/openshift/ci-tools/pkg/api/multiarchbuildconfig/v1"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestOCImageMirrorArgs(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		src    string
		output v1.MultiArchBuildConfigOutput
		want   []string
	}{
		{
			name: "Mirror to one destination",
			src:  "src-registry.com/src-image:latest",
			output: v1.MultiArchBuildConfigOutput{
				To: []string{"dst-registry.com/dst-image:latest"},
			},
			want: []string{"src-registry.com/src-image:latest", "dst-registry.com/dst-image:latest"},
		},
		{
			name: "Mirror to multiple destinations",
			src:  "src-registry.com/src-image:latest",
			output: v1.MultiArchBuildConfigOutput{
				To: []string{"dst-registry.com/dst-image-1:latest", "dst-registry.com/dst-image-2:latest"},
			},
			want: []string{
				"src-registry.com/src-image:latest",
				"dst-registry.com/dst-image-1:latest",
				"dst-registry.com/dst-image-2:latest",
			},
		},
		{
			name: "Deduplicate destinations",
			src:  "src-registry.com/src-image:latest",
			output: v1.MultiArchBuildConfigOutput{
				To: []string{
					"dst-registry.com/dst-image-1:latest",
					"dst-registry.com/dst-image-2:latest",
					"dst-registry.com/dst-image-2:latest",
				},
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
			args := ocImageMirrorArgs(testCase.src, &testCase.output)
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
	imageMirrorCmdFactory := func(err error) func(*logrus.Entry, string, []string) error {
		return func(*logrus.Entry, string, []string) error { return err }
	}

	for _, testCase := range []struct {
		name           string
		mabc           v1.MultiArchBuildConfig
		imageMirrorErr error
		want           v1.MultiArchBuildConfigStatus
		wantResult     bool
	}{
		{
			name: "No output set, do nothing",
			mabc: v1.MultiArchBuildConfig{
				Spec: v1.MultiArchBuildConfigSpec{
					Output: v1.MultiArchBuildConfigOutput{
						To: []string{},
					},
				},
				Status: v1.MultiArchBuildConfigStatus{State: ""},
			},
			want:       v1.MultiArchBuildConfigStatus{State: ""},
			wantResult: true,
		},
		{
			name: "Status shows image has been mirrored already, do nothing",
			mabc: v1.MultiArchBuildConfig{
				Spec: v1.MultiArchBuildConfigSpec{
					Output: v1.MultiArchBuildConfigOutput{
						To: []string{},
					},
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
			wantResult: true,
		},
		{
			name: "Mirror completed successfully, set status to success",
			mabc: v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mabc-1",
					Namespace: "ns",
				},
				Spec: v1.MultiArchBuildConfigSpec{
					Output: v1.MultiArchBuildConfigOutput{
						To: []string{"dst-reg.com/dst-image:latest"},
					},
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
			},
			wantResult: true,
		},
		{
			name: "Mirror failed, set status to failed",
			mabc: v1.MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mabc-1",
					Namespace: "ns",
				},
				Spec: v1.MultiArchBuildConfigSpec{
					Output: v1.MultiArchBuildConfigOutput{
						To: []string{"dst-reg.com/dst-image:latest"},
					},
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
			},
			wantResult: false,
		},
	} {
		testCase := testCase

		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			r := reconciler{
				client:         fake.NewClientBuilder().Build(),
				timeNowFn:      func() time.Time { return time.Time{} },
				mirrorImagesFn: imageMirrorCmdFactory(testCase.imageMirrorErr),
			}

			mutateFn, succeeded := r.handleMirrorImage("fake-image", &testCase.mabc)
			if mutateFn != nil {
				mutateFn(&testCase.mabc)
			}
			if testCase.wantResult != succeeded {
				t.Errorf("expected mirror result to be %t but got %t instead", testCase.wantResult, succeeded)
			}
			if diff := cmp.Diff(testCase.want, testCase.mabc.Status); diff != "" {
				t.Errorf("unexpected mabc:\n%s", diff)
			}
		})
	}
}
