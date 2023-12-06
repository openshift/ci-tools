package manifestpusher

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus/hooks/test"

	corev1 "k8s.io/api/core/v1"

	buildv1 "github.com/openshift/api/build/v1"
)

func TestArgs(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		builds         []buildv1.Build
		targetImageRef string
		dockercfgPath  string
		registryUrl    string
		wantArgs       []string
	}{
		{
			name: "Bundle amd64 and arm64 images into a manifest list",
			builds: []buildv1.Build{
				{
					Spec: buildv1.BuildSpec{
						CommonSpec: buildv1.CommonSpec{
							NodeSelector: buildv1.OptionalNodeSelector{
								nodeArchitectureLabel: "amd64",
							},
							Output: buildv1.BuildOutput{
								To: &corev1.ObjectReference{
									Name:      "managed-clonerefs:latest-amd64",
									Namespace: "ci",
								},
							},
						},
					},
				},
				{
					Spec: buildv1.BuildSpec{
						CommonSpec: buildv1.CommonSpec{
							NodeSelector: buildv1.OptionalNodeSelector{
								nodeArchitectureLabel: "arm64",
							},
							Output: buildv1.BuildOutput{
								To: &corev1.ObjectReference{
									Name:      "managed-clonerefs:latest-arm64",
									Namespace: "ci",
								},
							},
						},
					},
				},
			},
			targetImageRef: "ci/managed-clonerefs:latest",
			dockercfgPath:  ".dockercfgjson",
			registryUrl:    "foo-registry.com",
			wantArgs: []string{
				"--debug", "--insecure",
				"--docker-cfg", ".dockercfgjson",
				"push", "from-args",
				"--platforms", "linux/amd64,linux/arm64",
				"--template", "foo-registry.com/ci/managed-clonerefs:latest-ARCH",
				"--target", "foo-registry.com/ci/managed-clonerefs:latest",
			},
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			logger, _ := test.NewNullLogger()
			m := manifestPusher{
				logger:        logger.WithContext(context.TODO()),
				dockercfgPath: testCase.dockercfgPath,
				registryURL:   testCase.registryUrl,
			}
			args := m.args(testCase.builds, testCase.targetImageRef)
			if diff := cmp.Diff(testCase.wantArgs, args); diff != "" {
				t.Fatalf("args differs: %s", diff)
			}
		})
	}
}
