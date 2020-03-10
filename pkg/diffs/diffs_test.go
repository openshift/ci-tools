package diffs

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/getlantern/deepcopy"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/sets"
	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

var ignoreUnexported = cmpopts.IgnoreUnexported(prowconfig.Presubmit{}, prowconfig.Brancher{}, prowconfig.RegexpChangeMatcher{})

func TestGetChangedCiopConfigs(t *testing.T) {
	baseCiopConfig := config.DataWithInfo{
		Configuration: cioperatorapi.ReleaseBuildConfiguration{
			InputConfiguration: cioperatorapi.InputConfiguration{
				ReleaseTagConfiguration: &cioperatorapi.ReleaseTagConfiguration{
					Cluster:   "kluster",
					Namespace: "namespace",
					Name:      "name",
				},
			},
			Tests: []cioperatorapi.TestStepConfiguration{
				{
					As:       "unit",
					Commands: "make unit",
				},
				{
					As:       "e2e",
					Commands: "make e2e",
				},
				{
					As:       "verify",
					Commands: "make verify",
				},
			},
		},
		Info: config.Info{
			Org:      "org",
			Repo:     "repo",
			Branch:   "branch",
			Filename: "org-repo-branch.yaml",
		},
	}

	testCases := []struct {
		name                 string
		configGenerator      func(*testing.T) (before, after config.ByFilename)
		expected             func() config.ByFilename
		expectedAffectedJobs map[string]sets.String
	}{{
		name: "no changes",
		configGenerator: func(t *testing.T) (config.ByFilename, config.ByFilename) {
			before := config.ByFilename{"org-repo-branch.yaml": baseCiopConfig}
			after := config.ByFilename{"org-repo-branch.yaml": baseCiopConfig}
			return before, after
		},
		expected:             func() config.ByFilename { return config.ByFilename{} },
		expectedAffectedJobs: map[string]sets.String{},
	}, {
		name: "new config",
		configGenerator: func(t *testing.T) (config.ByFilename, config.ByFilename) {
			before := config.ByFilename{"org-repo-branch.yaml": baseCiopConfig}
			after := config.ByFilename{
				"org-repo-branch.yaml":         baseCiopConfig,
				"org-repo-another-branch.yaml": baseCiopConfig,
			}
			return before, after
		},
		expected: func() config.ByFilename {
			return config.ByFilename{"org-repo-another-branch.yaml": baseCiopConfig}
		},
		expectedAffectedJobs: map[string]sets.String{},
	}, {
		name: "changed config",
		configGenerator: func(t *testing.T) (config.ByFilename, config.ByFilename) {
			before := config.ByFilename{"org-repo-branch.yaml": baseCiopConfig}
			afterConfig := config.DataWithInfo{}
			if err := deepcopy.Copy(&afterConfig, &baseCiopConfig); err != nil {
				t.Fatal(err)
			}
			afterConfig.Configuration.InputConfiguration.ReleaseTagConfiguration.Name = "another-name"
			after := config.ByFilename{"org-repo-branch.yaml": afterConfig}
			return before, after
		},
		expected: func() config.ByFilename {
			expected := config.DataWithInfo{}
			if err := deepcopy.Copy(&expected, &baseCiopConfig); err != nil {
				t.Fatal(err)
			}
			expected.Configuration.InputConfiguration.ReleaseTagConfiguration.Name = "another-name"
			return config.ByFilename{"org-repo-branch.yaml": expected}
		},
		expectedAffectedJobs: map[string]sets.String{},
	},
		{
			name: "changed tests",
			configGenerator: func(t *testing.T) (config.ByFilename, config.ByFilename) {
				before := config.ByFilename{"org-repo-branch.yaml": baseCiopConfig}
				afterConfig := config.DataWithInfo{}
				if err := deepcopy.Copy(&afterConfig, &baseCiopConfig); err != nil {
					t.Fatal(err)
				}
				afterConfig.Configuration.Tests[0].Commands = "changed commands"
				after := config.ByFilename{"org-repo-branch.yaml": afterConfig}
				return before, after
			},
			expected: func() config.ByFilename {
				expected := config.DataWithInfo{}
				if err := deepcopy.Copy(&expected, &baseCiopConfig); err != nil {
					t.Fatal(err)
				}
				expected.Configuration.Tests[0].Commands = "changed commands"
				return config.ByFilename{"org-repo-branch.yaml": expected}
			},
			expectedAffectedJobs: map[string]sets.String{"org-repo-branch.yaml": {"unit": sets.Empty{}}},
		},
		{
			name: "changed multiple tests",
			configGenerator: func(t *testing.T) (config.ByFilename, config.ByFilename) {
				before := config.ByFilename{"org-repo-branch.yaml": baseCiopConfig}
				afterConfig := config.DataWithInfo{}
				if err := deepcopy.Copy(&afterConfig, &baseCiopConfig); err != nil {
					t.Fatal(err)
				}
				afterConfig.Configuration.Tests[0].Commands = "changed commands"
				afterConfig.Configuration.Tests[1].Commands = "changed commands"
				after := config.ByFilename{"org-repo-branch.yaml": afterConfig}
				return before, after
			},
			expected: func() config.ByFilename {
				expected := config.DataWithInfo{}
				if err := deepcopy.Copy(&expected, &baseCiopConfig); err != nil {
					t.Fatal(err)
				}
				expected.Configuration.Tests[0].Commands = "changed commands"
				expected.Configuration.Tests[1].Commands = "changed commands"
				return config.ByFilename{"org-repo-branch.yaml": expected}
			},
			expectedAffectedJobs: map[string]sets.String{
				"org-repo-branch.yaml": {
					"unit": sets.Empty{},
					"e2e":  sets.Empty{},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			before, after := tc.configGenerator(t)
			actual, affectedJobs := GetChangedCiopConfigs(before, after, logrus.NewEntry(logrus.New()))
			expected := tc.expected()

			if !reflect.DeepEqual(expected, actual) {
				t.Errorf("Detected changed ci-operator config changes differ from expected:\n%s", cmp.Diff(expected, actual))
			}

			if !reflect.DeepEqual(tc.expectedAffectedJobs, affectedJobs) {
				t.Errorf("Affected jobs differ from expected:\n%s", cmp.Diff(tc.expectedAffectedJobs, affectedJobs, ignoreUnexported))
			}
		})
	}
}

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
			Reporter: prowconfig.Reporter{
				Context: "test-base-presubmit",
			},
			Brancher: prowconfig.Brancher{Branches: []string{"^master$"}},
		},
	}

	testCases := []struct {
		name            string
		configGenerator func(*testing.T) (before, after *prowconfig.Config)
		expected        config.Presubmits
	}{
		{
			name: "no differences mean nothing is identified as a diff",
			configGenerator: func(t *testing.T) (*prowconfig.Config, *prowconfig.Config) {
				return makeConfig(basePresubmit), makeConfig(basePresubmit)
			},
			expected: config.Presubmits{},
		},
		{
			name: "new job added",
			configGenerator: func(t *testing.T) (*prowconfig.Config, *prowconfig.Config) {
				var p []prowconfig.Presubmit
				var pNew prowconfig.Presubmit
				if err := deepcopy.Copy(&p, basePresubmit); err != nil {
					t.Fatal(err)
				}

				pNew = p[0]
				pNew.Name = "test-base-presubmit-new"
				p = append(p, pNew)

				return makeConfig(basePresubmit), makeConfig(p)

			},
			expected: config.Presubmits{
				"org/repo": func() []prowconfig.Presubmit {
					var p []prowconfig.Presubmit
					var pNew prowconfig.Presubmit
					if err := deepcopy.Copy(&p, basePresubmit); err != nil {
						t.Fatal(err)
					}
					pNew = p[0]
					pNew.Name = "test-base-presubmit-new"

					return []prowconfig.Presubmit{pNew}
				}(),
			},
		},
		{
			name: "different agent is identified as a diff (from jenkins to kubernetes)",
			configGenerator: func(t *testing.T) (*prowconfig.Config, *prowconfig.Config) {
				var p []prowconfig.Presubmit
				if err := deepcopy.Copy(&p, basePresubmit); err != nil {
					t.Fatal(err)
				}
				p[0].Agent = "jenkins"
				return makeConfig(p), makeConfig(basePresubmit)

			},
			expected: config.Presubmits{
				"org/repo": basePresubmit,
			},
		},
		{
			name: "different optional field is identified as a diff (from true to false)",
			configGenerator: func(t *testing.T) (*prowconfig.Config, *prowconfig.Config) {
				var p, base []prowconfig.Presubmit
				if err := deepcopy.Copy(&p, basePresubmit); err != nil {
					t.Fatal(err)
				}
				if err := deepcopy.Copy(&base, basePresubmit); err != nil {
					t.Fatal(err)
				}

				base[0].Optional = true
				p[0].Optional = false
				return makeConfig(base), makeConfig(p)

			},
			expected: config.Presubmits{
				"org/repo": basePresubmit,
			},
		},
		{
			name: "different always_run field is identified as a diff (from false to true)",
			configGenerator: func(t *testing.T) (*prowconfig.Config, *prowconfig.Config) {
				var p, base []prowconfig.Presubmit
				if err := deepcopy.Copy(&p, basePresubmit); err != nil {
					t.Fatal(err)
				}
				if err := deepcopy.Copy(&base, basePresubmit); err != nil {
					t.Fatal(err)
				}

				base[0].AlwaysRun = false
				p[0].AlwaysRun = true
				return makeConfig(base), makeConfig(p)

			},
			expected: config.Presubmits{
				"org/repo": func() []prowconfig.Presubmit {
					var p []prowconfig.Presubmit
					if err := deepcopy.Copy(&p, basePresubmit); err != nil {
						t.Fatal(err)
					}
					p[0].AlwaysRun = true
					return p
				}(),
			},
		},
		{
			name: "different spec is identified as a diff - single change",
			configGenerator: func(t *testing.T) (*prowconfig.Config, *prowconfig.Config) {
				var p []prowconfig.Presubmit
				if err := deepcopy.Copy(&p, basePresubmit); err != nil {
					t.Fatal(err)
				}
				p[0].Spec.Containers[0].Command = []string{"test-command"}
				return makeConfig(basePresubmit), makeConfig(p)

			},
			expected: config.Presubmits{
				"org/repo": func() []prowconfig.Presubmit {
					var p []prowconfig.Presubmit
					if err := deepcopy.Copy(&p, basePresubmit); err != nil {
						t.Fatal(err)
					}
					p[0].Spec.Containers[0].Command = []string{"test-command"}
					return p
				}(),
			},
		},
		{
			name: "different spec is identified as a diff - massive changes",
			configGenerator: func(t *testing.T) (*prowconfig.Config, *prowconfig.Config) {
				var p []prowconfig.Presubmit
				if err := deepcopy.Copy(&p, basePresubmit); err != nil {
					t.Fatal(err)
				}
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
			expected: config.Presubmits{
				"org/repo": func() []prowconfig.Presubmit {
					var p []prowconfig.Presubmit
					if err := deepcopy.Copy(&p, basePresubmit); err != nil {
						t.Fatal(err)
					}
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
			before, after := testCase.configGenerator(t)
			p := GetChangedPresubmits(before, after, logrus.NewEntry(logrus.New()))
			if !equality.Semantic.DeepEqual(p, testCase.expected) {
				t.Fatalf(cmp.Diff(testCase.expected["org/repo"], p["org/repo"], ignoreUnexported))
			}
		})
	}
}

func makeConfig(p []prowconfig.Presubmit) *prowconfig.Config {
	return &prowconfig.Config{
		JobConfig: prowconfig.JobConfig{
			PresubmitsStatic: config.Presubmits{"org/repo": p},
		},
	}
}

func TestGetPresubmitsForCiopConfigs(t *testing.T) {
	baseCiopConfig := config.Info{
		Org:      "org",
		Repo:     "repo",
		Branch:   "branch",
		Filename: "org-repo-branch.yaml",
	}

	basePresubmitWithCiop := prowconfig.Presubmit{
		Brancher: prowconfig.Brancher{
			Branches: []string{baseCiopConfig.Branch},
		},
		JobBase: prowconfig.JobBase{
			Name:  baseCiopConfig.JobName(jobconfig.PresubmitPrefix, "test"),
			Agent: string(pjapi.KubernetesAgent),
			Spec: &v1.PodSpec{
				Containers: []v1.Container{{
					Env: []v1.EnvVar{{
						ValueFrom: &v1.EnvVarSource{
							ConfigMapKeyRef: &v1.ConfigMapKeySelector{
								LocalObjectReference: v1.LocalObjectReference{
									Name: baseCiopConfig.ConfigMapName(),
								},
							},
						},
					}},
				}},
			},
		},
	}

	affectedJobs := map[string]sets.String{
		"org-repo-branch.yaml": {
			"testjob": sets.Empty{},
		},
	}

	testCases := []struct {
		description string
		prow        *prowconfig.Config
		ciop        config.ByFilename
		expected    config.Presubmits
	}{{
		description: "return a presubmit using one of the input ciop configs",
		prow: &prowconfig.Config{
			JobConfig: prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"org/repo": {
						func() prowconfig.Presubmit {
							ret := prowconfig.Presubmit{}
							if err := deepcopy.Copy(&ret, &basePresubmitWithCiop); err != nil {
								t.Fatal(err)
							}
							ret.Name = baseCiopConfig.JobName(jobconfig.PresubmitPrefix, "testjob")
							ret.Spec.Containers[0].Env[0].ValueFrom.ConfigMapKeyRef.Key = baseCiopConfig.Filename
							return ret
						}(),
					}},
			},
		},
		ciop: config.ByFilename{baseCiopConfig.Filename: {Info: baseCiopConfig}},
		expected: config.Presubmits{"org/repo": {
			func() prowconfig.Presubmit {
				ret := prowconfig.Presubmit{}
				if err := deepcopy.Copy(&ret, &basePresubmitWithCiop); err != nil {
					t.Fatal(err)
				}
				ret.Name = baseCiopConfig.JobName(jobconfig.PresubmitPrefix, "testjob")
				ret.Spec.Containers[0].Env[0].ValueFrom.ConfigMapKeyRef.Key = baseCiopConfig.Filename
				return ret
			}(),
		}},
	}, {
		description: "return a presubmit using one of the input ciop configs, with additional env vars",
		prow: &prowconfig.Config{
			JobConfig: prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"org/repo": {
						func() prowconfig.Presubmit {
							ret := prowconfig.Presubmit{}
							if err := deepcopy.Copy(&ret, &basePresubmitWithCiop); err != nil {
								t.Fatal(err)
							}
							ret.Name = baseCiopConfig.JobName(jobconfig.PresubmitPrefix, "testjob")
							moreEnvVars := []v1.EnvVar{{
								Name:  "SOMETHING",
								Value: "value of SOMETHING",
							}}
							ret.Spec.Containers[0].Env = append(moreEnvVars, ret.Spec.Containers[0].Env...)
							ret.Spec.Containers[0].Env[len(moreEnvVars)].ValueFrom.ConfigMapKeyRef.Key = baseCiopConfig.Filename
							return ret
						}(),
					}},
			},
		},
		ciop: config.ByFilename{baseCiopConfig.Filename: {Info: baseCiopConfig}},
		expected: config.Presubmits{"org/repo": {
			func() prowconfig.Presubmit {
				ret := prowconfig.Presubmit{}
				if err := deepcopy.Copy(&ret, &basePresubmitWithCiop); err != nil {
					t.Fatal(err)
				}
				ret.Name = baseCiopConfig.JobName(jobconfig.PresubmitPrefix, "testjob")
				moreEnvVars := []v1.EnvVar{{
					Name:  "SOMETHING",
					Value: "value of SOMETHING",
				}}
				ret.Spec.Containers[0].Env = append(moreEnvVars, ret.Spec.Containers[0].Env...)
				ret.Spec.Containers[0].Env[len(moreEnvVars)].ValueFrom.ConfigMapKeyRef.Key = baseCiopConfig.Filename
				return ret
			}(),
		}},
	}, {
		description: "do not return a presubmit using a ciop config not present in input",
		prow: &prowconfig.Config{
			JobConfig: prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"org/repo": {
						func() prowconfig.Presubmit {
							ret := prowconfig.Presubmit{}
							if err := deepcopy.Copy(&ret, &basePresubmitWithCiop); err != nil {
								t.Fatal(err)
							}
							ret.Name = baseCiopConfig.JobName(jobconfig.PresubmitPrefix, "testjob")
							ret.Spec.Containers[0].Env[0].ValueFrom.ConfigMapKeyRef.Key = baseCiopConfig.Filename
							return ret
						}(),
					}},
			},
		},
		ciop:     config.ByFilename{},
		expected: config.Presubmits{},
	}, {
		description: "handle jenkins presubmits",
		prow: &prowconfig.Config{
			JobConfig: prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"org/repo": {
						func() prowconfig.Presubmit {
							ret := prowconfig.Presubmit{}
							if err := deepcopy.Copy(&ret, &basePresubmitWithCiop); err != nil {
								t.Fatal(err)
							}
							ret.Name = baseCiopConfig.JobName(jobconfig.PresubmitPrefix, "testjob")
							ret.Agent = string(pjapi.JenkinsAgent)
							ret.Spec.Containers[0].Env = []v1.EnvVar{}
							return ret
						}(),
					}},
			},
		},
		ciop:     config.ByFilename{},
		expected: config.Presubmits{},
	},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			presubmits := GetPresubmitsForCiopConfigs(tc.prow, tc.ciop, affectedJobs)

			if !reflect.DeepEqual(tc.expected, presubmits) {
				t.Errorf("Returned presubmits differ from expected:\n%s", cmp.Diff(tc.expected, presubmits, ignoreUnexported))
			}
		})
	}
}

func podSpecReferencing(info config.Info) *v1.PodSpec {
	return &v1.PodSpec{
		Containers: []v1.Container{{
			Env: []v1.EnvVar{{
				ValueFrom: &v1.EnvVarSource{
					ConfigMapKeyRef: &v1.ConfigMapKeySelector{
						LocalObjectReference: v1.LocalObjectReference{
							Name: info.ConfigMapName(),
						},
						Key: info.Basename(),
					},
				},
			}},
		}},
	}
}

func TestGetImagesPostsubmitsForCiopConfigs(t *testing.T) {
	var testCases = []struct {
		name        string
		prowConfig  *prowconfig.Config
		ciopConfigs config.ByFilename
		expected    []PostsubmitInContext
	}{
		{
			name: "no changed ci-op configs means no changed postsubmits",
			prowConfig: &prowconfig.Config{
				JobConfig: prowconfig.JobConfig{
					PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
						"org/repo": {{
							JobBase: prowconfig.JobBase{
								Name:  "branch-ci-org-repo-branch-images",
								Agent: "kubernetes",
								Spec:  podSpecReferencing(config.Info{Org: "org", Repo: "repo", Branch: "branch"}),
							},
						}},
					},
				},
			},
			ciopConfigs: map[string]config.DataWithInfo{},
		},
		{
			name: "changed ci-op configs but no images job means no changed postsubmits",
			prowConfig: &prowconfig.Config{
				JobConfig: prowconfig.JobConfig{
					PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
						"org/repo": {},
					},
				},
			},
			ciopConfigs: map[string]config.DataWithInfo{
				"org-repo-branch.yaml": {Info: config.Info{Org: "org", Repo: "repo", Branch: "branch"}},
			},
		},
		{
			name: "changed ci-op configs means changed postsubmits",
			prowConfig: &prowconfig.Config{
				JobConfig: prowconfig.JobConfig{
					PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
						"org/repo": {{
							JobBase: prowconfig.JobBase{
								Name:  "branch-ci-org-repo-branch-images",
								Agent: "kubernetes",
								Spec:  podSpecReferencing(config.Info{Org: "org", Repo: "repo", Branch: "branch"}),
							},
						}},
					},
				},
			},
			ciopConfigs: map[string]config.DataWithInfo{
				"org-repo-branch.yaml": {Info: config.Info{Org: "org", Repo: "repo", Branch: "branch"}},
			},
			expected: []PostsubmitInContext{{
				Info: config.Info{Org: "org", Repo: "repo", Branch: "branch"},
				Job: prowconfig.Postsubmit{
					JobBase: prowconfig.JobBase{
						Name:  "branch-ci-org-repo-branch-images",
						Agent: "kubernetes",
						Spec:  podSpecReferencing(config.Info{Org: "org", Repo: "repo", Branch: "branch"}),
					},
				},
			}},
		},
		{
			name: "changed ci-op configs but images job referencing a different file means no changed postsubmits",
			prowConfig: &prowconfig.Config{
				JobConfig: prowconfig.JobConfig{
					PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
						"org/repo": {{
							JobBase: prowconfig.JobBase{
								Name:  "branch-ci-org-repo-BRANCH-images",
								Agent: "kubernetes",
								Spec:  podSpecReferencing(config.Info{Org: "org", Repo: "repo", Branch: "BRANCH"}),
							},
						}},
					},
				},
			},
			ciopConfigs: map[string]config.DataWithInfo{
				"org-repo-branch.yaml": {Info: config.Info{Org: "org", Repo: "repo", Branch: "branch"}},
			},
		},
		{
			name: "changed ci-op configs but only non-images job means no changed postsubmits",
			prowConfig: &prowconfig.Config{
				JobConfig: prowconfig.JobConfig{
					PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
						"org/repo": {{
							JobBase: prowconfig.JobBase{
								Name:  "branch-ci-org-repo-branch-othertest",
								Agent: "kubernetes",
								Spec:  podSpecReferencing(config.Info{Org: "org", Repo: "repo", Branch: "branch"}),
							},
						}},
					},
				},
			},
			ciopConfigs: map[string]config.DataWithInfo{
				"org-repo-branch.yaml": {Info: config.Info{Org: "org", Repo: "repo", Branch: "branch"}},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := GetImagesPostsubmitsForCiopConfigs(testCase.prowConfig, testCase.ciopConfigs), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect images postsubmits: %v", testCase.name, cmp.Diff(actual, expected, ignoreUnexported))
			}
		})
	}
}

func TestGetPresubmitsForClusterProfiles(t *testing.T) {
	makePresubmit := func(name string, agent pjapi.ProwJobAgent, profiles []string) prowconfig.Presubmit {
		ret := prowconfig.Presubmit{
			JobBase: prowconfig.JobBase{
				Name:  name,
				Agent: string(agent),
				Spec:  &v1.PodSpec{}},
		}
		for _, p := range profiles {
			ret.Spec.Volumes = append(ret.Spec.Volumes, v1.Volume{
				Name: "cluster-profile",
				VolumeSource: v1.VolumeSource{
					Projected: &v1.ProjectedVolumeSource{
						Sources: []v1.VolumeProjection{{
							ConfigMap: &v1.ConfigMapProjection{
								LocalObjectReference: v1.LocalObjectReference{
									Name: config.ClusterProfilePrefix + p,
								},
							},
						}},
					},
				},
			})
		}
		return ret
	}
	for _, tc := range []struct {
		id       string
		cfg      *prowconfig.Config
		profiles []config.ConfigMapSource
		expected []string
	}{{
		id:  "empty",
		cfg: &prowconfig.Config{},
		profiles: []config.ConfigMapSource{{
			Filename: filepath.Join(config.ClusterProfilesPath, "test-profile"),
		}},
	}, {
		id: "not a kubernetes job",
		cfg: &prowconfig.Config{
			JobConfig: prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"org/repo": {
						makePresubmit("not-a-kubernetes-job", pjapi.JenkinsAgent, []string{}),
					},
				},
			},
		},
		profiles: []config.ConfigMapSource{{
			Filename: filepath.Join(config.ClusterProfilesPath, "test-profile"),
		}},
	}, {
		id: "job doesn't use cluster profiles",
		cfg: &prowconfig.Config{
			JobConfig: prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"org/repo": {
						makePresubmit("no-cluster-profile", pjapi.KubernetesAgent, []string{}),
					},
				},
			},
		},
		profiles: []config.ConfigMapSource{{
			Filename: filepath.Join(config.ClusterProfilesPath, "test-profile"),
		}},
	}, {
		id: "job doesn't use the cluster profile",
		cfg: &prowconfig.Config{
			JobConfig: prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"org/repo": {
						makePresubmit("doesnt-use-cluster-profile", pjapi.KubernetesAgent, []string{"another-profile"}),
					},
				},
			},
		},
		profiles: []config.ConfigMapSource{{
			Filename: filepath.Join(config.ClusterProfilesPath, "test-profile"),
		}},
	}, {
		id: "multiple jobs, one uses cluster the profile",
		cfg: &prowconfig.Config{
			JobConfig: prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"org/repo": {
						makePresubmit("no-cluster-profile", pjapi.KubernetesAgent, []string{}),
					},
					"some/other-repo": {
						makePresubmit("uses-cluster-profile", pjapi.KubernetesAgent, []string{"test-profile"}),
						makePresubmit("uses-another-profile", pjapi.KubernetesAgent, []string{"another-profile"}),
					},
				},
			},
		},
		profiles: []config.ConfigMapSource{{
			Filename: filepath.Join(config.ClusterProfilesPath, "test-profile"),
		}},
		expected: []string{"uses-cluster-profile"},
	}} {
		t.Run(tc.id, func(t *testing.T) {
			ret := GetPresubmitsForClusterProfiles(tc.cfg, tc.profiles)
			var names []string
			for _, jobs := range ret {
				for _, j := range jobs {
					names = append(names, j.Name)
				}
			}
			if !reflect.DeepEqual(names, tc.expected) {
				t.Fatalf("want %s, got %s", tc.expected, names)
			}
		})
	}
}

func TestGetChangedPeriodics(t *testing.T) {
	basePeriodic := []prowconfig.Periodic{
		{
			JobBase: prowconfig.JobBase{
				Agent: "kubernetes",
				Name:  "test-base-periodic",
				Spec: &v1.PodSpec{
					Containers: []v1.Container{{
						Command: []string{"ci-operator"},
						Args:    []string{"--artifact-dir=$(ARTIFACTS)", "--target=images"},
					}},
				},
			},
		},
	}

	testCases := []struct {
		name            string
		configGenerator func(*testing.T) (before, after *prowconfig.Config)
		expected        config.Periodics
	}{
		{
			name: "no differences mean nothing is identified as a diff",
			configGenerator: func(t *testing.T) (*prowconfig.Config, *prowconfig.Config) {
				return makeConfigWithPeriodics(basePeriodic), makeConfigWithPeriodics(basePeriodic)
			},
			expected: config.Periodics{},
		},
		{
			name: "new job added",
			configGenerator: func(t *testing.T) (*prowconfig.Config, *prowconfig.Config) {
				var p []prowconfig.Periodic
				var pNew prowconfig.Periodic
				if err := deepcopy.Copy(&p, basePeriodic); err != nil {
					t.Fatal(err)
				}

				pNew = p[0]
				pNew.Name = "test-base-periodic-new"
				p = append(p, pNew)

				return makeConfigWithPeriodics(basePeriodic), makeConfigWithPeriodics(p)

			},
			expected: func() config.Periodics {
				var p []prowconfig.Periodic
				var pNew prowconfig.Periodic
				if err := deepcopy.Copy(&p, basePeriodic); err != nil {
					t.Fatal(err)
				}
				pNew = p[0]
				pNew.Name = "test-base-periodic-new"

				return config.Periodics{pNew.Name: pNew}
			}(),
		},
		{
			name: "different spec is identified as a diff - single change",
			configGenerator: func(t *testing.T) (*prowconfig.Config, *prowconfig.Config) {
				var p []prowconfig.Periodic
				if err := deepcopy.Copy(&p, basePeriodic); err != nil {
					t.Fatal(err)
				}
				p[0].Spec.Containers[0].Command = []string{"test-command"}
				return makeConfigWithPeriodics(basePeriodic), makeConfigWithPeriodics(p)

			},
			expected: func() config.Periodics {
				var p []prowconfig.Periodic
				if err := deepcopy.Copy(&p, basePeriodic); err != nil {
					t.Fatal(err)
				}
				p[0].Spec.Containers[0].Command = []string{"test-command"}
				return config.Periodics{p[0].Name: p[0]}
			}(),
		},
		{
			name: "different spec is identified as a diff - massive changes",
			configGenerator: func(t *testing.T) (*prowconfig.Config, *prowconfig.Config) {
				var p []prowconfig.Periodic
				if err := deepcopy.Copy(&p, basePeriodic); err != nil {
					t.Fatal(err)
				}
				p[0].Spec.Containers[0].Command = []string{"test-command"}
				p[0].Spec.Containers[0].Args = []string{"testarg", "testarg", "testarg"}
				p[0].Spec.Volumes = []v1.Volume{{
					Name: "test-volume",
					VolumeSource: v1.VolumeSource{
						EmptyDir: &v1.EmptyDirVolumeSource{},
					}},
				}
				return makeConfigWithPeriodics(basePeriodic), makeConfigWithPeriodics(p)
			},
			expected: func() config.Periodics {
				var p []prowconfig.Periodic
				if err := deepcopy.Copy(&p, basePeriodic); err != nil {
					t.Fatal(err)
				}
				p[0].Spec.Containers[0].Command = []string{"test-command"}
				p[0].Spec.Containers[0].Args = []string{"testarg", "testarg", "testarg"}
				p[0].Spec.Volumes = []v1.Volume{{
					Name: "test-volume",
					VolumeSource: v1.VolumeSource{
						EmptyDir: &v1.EmptyDirVolumeSource{},
					}},
				}
				return config.Periodics{p[0].Name: p[0]}
			}(),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			before, after := testCase.configGenerator(t)
			p := GetChangedPeriodics(before, after, logrus.NewEntry(logrus.New()))
			if !reflect.DeepEqual(testCase.expected, p) {
				t.Fatalf("Name:%s\nExpected %#v\nFound:%#v\n", testCase.name, testCase.expected, p)
			}
		})
	}
}

func makeConfigWithPeriodics(p []prowconfig.Periodic) *prowconfig.Config {
	return &prowconfig.Config{
		JobConfig: prowconfig.JobConfig{
			Periodics: p,
		},
	}
}

func TestGetChangedClusterJobs(t *testing.T) {
	baseConfig := prowconfig.Config{
		JobConfig: prowconfig.JobConfig{
			PresubmitsStatic: map[string][]prowconfig.Presubmit{
				"org/repo": {{JobBase: prowconfig.JobBase{Name: "test-base-presubmit", Cluster: "base"}}},
			},
			Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "test-base-periodic", Cluster: "base"}}},
		},
	}

	var testCases = []struct {
		name               string
		configGenerator    func(*testing.T) (before, after *prowconfig.Config)
		expectedPeriodics  config.Periodics
		expectedPresubmits config.Presubmits
	}{
		{
			name: "no cluster changes, nothing to rehearse",
			configGenerator: func(t *testing.T) (before, after *prowconfig.Config) {
				var a, b prowconfig.Config
				if err := deepcopy.Copy(&a, baseConfig); err != nil {
					t.Fatal(err)
				}
				if err := deepcopy.Copy(&b, baseConfig); err != nil {
					t.Fatal(err)
				}
				return &a, &b
			},
			expectedPresubmits: config.Presubmits{},
			expectedPeriodics:  config.Periodics{},
		},
		{
			name: "all cluster changes, many to rehearse",
			configGenerator: func(t *testing.T) (before, after *prowconfig.Config) {
				var a, b prowconfig.Config
				if err := deepcopy.Copy(&a, baseConfig); err != nil {
					t.Fatal(err)
				}
				if err := deepcopy.Copy(&b, baseConfig); err != nil {
					t.Fatal(err)
				}
				b.PresubmitsStatic["org/repo"][0].Cluster = "new"
				b.Periodics[0].Cluster = "new"
				return &a, &b
			},
			expectedPresubmits: config.Presubmits{
				"org/repo": {{JobBase: prowconfig.JobBase{Name: "test-base-presubmit", Cluster: "new"}}},
			},
			expectedPeriodics: config.Periodics{"test-base-periodic": {JobBase: prowconfig.JobBase{Name: "test-base-periodic", Cluster: "new"}}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			before, after := testCase.configGenerator(t)
			presubmits, periodics := GetChangedClusterJobs(before, after, logrus.NewEntry(logrus.New()))
			if !reflect.DeepEqual(testCase.expectedPresubmits, presubmits) {
				t.Errorf("%s:\nexpected\n%#v\nfound:%#v\n", testCase.name, testCase.expectedPresubmits, presubmits)
			}
			if !reflect.DeepEqual(testCase.expectedPeriodics, periodics) {
				t.Errorf("%s:\nexpected\n%#v\nfound:%#v\n", testCase.name, testCase.expectedPeriodics, periodics)
			}
		})
	}
}
