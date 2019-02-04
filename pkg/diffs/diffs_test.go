package diffs

import (
	"testing"

	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	prowconfig "k8s.io/test-infra/prow/config"

	"github.com/getlantern/deepcopy"
)

func TestGetChangedPresubmits(t *testing.T) {
	basePresubmit := []prowconfig.Presubmit{
		{
			JobBase: prowconfig.JobBase{
				Agent: "kubernetes",
				Name:  "test-base-presubmit",
				Spec: &v1.PodSpec{
					Containers: []v1.Container{{
						Command: []string{"ci-operator"},
						Args:    []string{"--artifact-dir=$(ARTIFACTS)", "--target=images"},
					}},
				},
			},
			Context:  "test-base-presubmit",
			Brancher: prowconfig.Brancher{Branches: []string{"^master$"}},
		},
	}

	testCases := []struct {
		name            string
		configGenerator func() (before, after *prowconfig.Config)
		expected        map[string][]prowconfig.Presubmit
	}{
		{
			name: "no differences mean nothing is identified as a diff",
			configGenerator: func() (*prowconfig.Config, *prowconfig.Config) {
				return makeConfig(basePresubmit), makeConfig(basePresubmit)
			},
			expected: map[string][]prowconfig.Presubmit{},
		},
		{
			name: "new job added",
			configGenerator: func() (*prowconfig.Config, *prowconfig.Config) {
				var p []prowconfig.Presubmit
				var pNew prowconfig.Presubmit
				deepcopy.Copy(&p, basePresubmit)

				pNew = p[0]
				pNew.Name = "test-base-presubmit-new"
				p = append(p, pNew)

				return makeConfig(basePresubmit), makeConfig(p)

			},
			expected: map[string][]prowconfig.Presubmit{
				"org/repo": func() []prowconfig.Presubmit {
					var p []prowconfig.Presubmit
					var pNew prowconfig.Presubmit
					deepcopy.Copy(&p, basePresubmit)
					pNew = p[0]
					pNew.Name = "test-base-presubmit-new"

					return []prowconfig.Presubmit{pNew}
				}(),
			},
		},
		{
			name: "different agent is identified as a diff (from jenkins to kubernetes)",
			configGenerator: func() (*prowconfig.Config, *prowconfig.Config) {
				var p []prowconfig.Presubmit
				deepcopy.Copy(&p, basePresubmit)
				p[0].Agent = "jenkins"
				return makeConfig(p), makeConfig(basePresubmit)

			},
			expected: map[string][]prowconfig.Presubmit{
				"org/repo": basePresubmit,
			},
		},
		{
			name: "different spec is identified as a diff - single change",
			configGenerator: func() (*prowconfig.Config, *prowconfig.Config) {
				var p []prowconfig.Presubmit
				deepcopy.Copy(&p, basePresubmit)
				p[0].Spec.Containers[0].Command = []string{"test-command"}
				return makeConfig(basePresubmit), makeConfig(p)

			},
			expected: map[string][]prowconfig.Presubmit{
				"org/repo": func() []prowconfig.Presubmit {
					var p []prowconfig.Presubmit
					deepcopy.Copy(&p, basePresubmit)
					p[0].Spec.Containers[0].Command = []string{"test-command"}
					return p
				}(),
			},
		},
		{
			name: "different spec is identified as a diff - massive changes",
			configGenerator: func() (*prowconfig.Config, *prowconfig.Config) {
				var p []prowconfig.Presubmit
				deepcopy.Copy(&p, basePresubmit)
				p[0].Spec.Containers[0].Command = []string{"test-command"}
				p[0].Spec.Containers[0].Args = []string{"testarg", "testarg", "testarg"}
				p[0].Spec.Volumes = []v1.Volume{{
					Name: "test-volume",
					VolumeSource: v1.VolumeSource{
						EmptyDir: &v1.EmptyDirVolumeSource{},
					}},
				}
				return makeConfig(basePresubmit), makeConfig(p)
			},
			expected: map[string][]prowconfig.Presubmit{
				"org/repo": func() []prowconfig.Presubmit {
					var p []prowconfig.Presubmit
					deepcopy.Copy(&p, basePresubmit)
					p[0].Spec.Containers[0].Command = []string{"test-command"}
					p[0].Spec.Containers[0].Args = []string{"testarg", "testarg", "testarg"}
					p[0].Spec.Volumes = []v1.Volume{{
						Name: "test-volume",
						VolumeSource: v1.VolumeSource{
							EmptyDir: &v1.EmptyDirVolumeSource{},
						}},
					}
					return p
				}()},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			before, after := testCase.configGenerator()
			p := GetChangedPresubmits(before, after, logrus.NewEntry(logrus.New()))
			if !equality.Semantic.DeepEqual(p, testCase.expected) {
				t.Fatalf("Name:%s\nExpected %#v\nFound:%#v\n", testCase.name, testCase.expected["org/repo"], p["org/repo"])
			}
		})
	}
}

func makeConfig(p []prowconfig.Presubmit) *prowconfig.Config {
	return &prowconfig.Config{
		JobConfig: prowconfig.JobConfig{
			Presubmits: map[string][]prowconfig.Presubmit{"org/repo": p},
		},
	}
}
