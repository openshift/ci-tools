package diffs

import (
	"reflect"
	"testing"

	"github.com/getlantern/deepcopy"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/sets"
	utilpointer "k8s.io/utils/pointer"
	pjapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

var ignoreUnexported = cmpopts.IgnoreUnexported(
	prowconfig.Presubmit{},
	prowconfig.Brancher{},
	prowconfig.RegexpChangeMatcher{},
	prowconfig.Periodic{},
)

func TestGetChangedCiopConfigs(t *testing.T) {
	baseCiopConfig := config.DataWithInfo{
		Configuration: cioperatorapi.ReleaseBuildConfiguration{
			InputConfiguration: cioperatorapi.InputConfiguration{
				ReleaseTagConfiguration: &cioperatorapi.ReleaseTagConfiguration{
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
			Metadata: cioperatorapi.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			Filename: "org-repo-branch.yaml",
		},
	}

	testCases := []struct {
		name                                     string
		configGenerator                          func(*testing.T) (before, after config.DataByFilename)
		expected                                 func() config.DataByFilename
		expectedAffectedJobs                     map[string]sets.Set[string]
		expectedDisabledDueToNetworkAccessToggle []string
	}{{
		name: "no changes",
		configGenerator: func(t *testing.T) (config.DataByFilename, config.DataByFilename) {
			before := config.DataByFilename{"org-repo-branch.yaml": baseCiopConfig}
			after := config.DataByFilename{"org-repo-branch.yaml": baseCiopConfig}
			return before, after
		},
		expected:             func() config.DataByFilename { return config.DataByFilename{} },
		expectedAffectedJobs: map[string]sets.Set[string]{},
	}, {
		name: "new config",
		configGenerator: func(t *testing.T) (config.DataByFilename, config.DataByFilename) {
			before := config.DataByFilename{"org-repo-branch.yaml": baseCiopConfig}
			after := config.DataByFilename{
				"org-repo-branch.yaml":         baseCiopConfig,
				"org-repo-another-branch.yaml": baseCiopConfig,
			}
			return before, after
		},
		expected: func() config.DataByFilename {
			return config.DataByFilename{"org-repo-another-branch.yaml": baseCiopConfig}
		},
		expectedAffectedJobs: map[string]sets.Set[string]{},
	}, {
		name: "changed config",
		configGenerator: func(t *testing.T) (config.DataByFilename, config.DataByFilename) {
			before := config.DataByFilename{"org-repo-branch.yaml": baseCiopConfig}
			afterConfig := config.DataWithInfo{}
			if err := deepcopy.Copy(&afterConfig, &baseCiopConfig); err != nil {
				t.Fatal(err)
			}
			afterConfig.Configuration.InputConfiguration.ReleaseTagConfiguration.Name = "another-name"
			after := config.DataByFilename{"org-repo-branch.yaml": afterConfig}
			return before, after
		},
		expected: func() config.DataByFilename {
			expected := config.DataWithInfo{}
			if err := deepcopy.Copy(&expected, &baseCiopConfig); err != nil {
				t.Fatal(err)
			}
			expected.Configuration.InputConfiguration.ReleaseTagConfiguration.Name = "another-name"
			return config.DataByFilename{"org-repo-branch.yaml": expected}
		},
		expectedAffectedJobs: map[string]sets.Set[string]{},
	}, {
		name: "changed tests",
		configGenerator: func(t *testing.T) (config.DataByFilename, config.DataByFilename) {
			before := config.DataByFilename{"org-repo-branch.yaml": baseCiopConfig}
			afterConfig := config.DataWithInfo{}
			if err := deepcopy.Copy(&afterConfig, &baseCiopConfig); err != nil {
				t.Fatal(err)
			}
			afterConfig.Configuration.Tests[0].Commands = "changed commands"
			after := config.DataByFilename{"org-repo-branch.yaml": afterConfig}
			return before, after
		},
		expected: func() config.DataByFilename {
			expected := config.DataWithInfo{}
			if err := deepcopy.Copy(&expected, &baseCiopConfig); err != nil {
				t.Fatal(err)
			}
			expected.Configuration.Tests[0].Commands = "changed commands"
			return config.DataByFilename{"org-repo-branch.yaml": expected}
		},
		expectedAffectedJobs: map[string]sets.Set[string]{"org-repo-branch.yaml": {"unit": sets.Empty{}}},
	}, {
		name: "changed multiple tests",
		configGenerator: func(t *testing.T) (config.DataByFilename, config.DataByFilename) {
			before := config.DataByFilename{"org-repo-branch.yaml": baseCiopConfig}
			afterConfig := config.DataWithInfo{}
			if err := deepcopy.Copy(&afterConfig, &baseCiopConfig); err != nil {
				t.Fatal(err)
			}
			afterConfig.Configuration.Tests[0].Commands = "changed commands"
			afterConfig.Configuration.Tests[1].Commands = "changed commands"
			after := config.DataByFilename{"org-repo-branch.yaml": afterConfig}
			return before, after
		},
		expected: func() config.DataByFilename {
			expected := config.DataWithInfo{}
			if err := deepcopy.Copy(&expected, &baseCiopConfig); err != nil {
				t.Fatal(err)
			}
			expected.Configuration.Tests[0].Commands = "changed commands"
			expected.Configuration.Tests[1].Commands = "changed commands"
			return config.DataByFilename{"org-repo-branch.yaml": expected}
		},
		expectedAffectedJobs: map[string]sets.Set[string]{
			"org-repo-branch.yaml": {
				"unit": sets.Empty{},
				"e2e":  sets.Empty{},
			},
		},
	}, {
		name: "one potential rehearsal disabled due to un-restricted network access set to 'false'",
		configGenerator: func(t *testing.T) (config.DataByFilename, config.DataByFilename) {
			before := config.DataByFilename{"org-repo-branch.yaml": baseCiopConfig}
			afterConfig := config.DataWithInfo{}
			if err := deepcopy.Copy(&afterConfig, &baseCiopConfig); err != nil {
				t.Fatal(err)
			}
			afterConfig.Configuration.Tests[0].RestrictNetworkAccess = utilpointer.Bool(false)
			afterConfig.Configuration.Tests[1].RestrictNetworkAccess = utilpointer.Bool(true)
			after := config.DataByFilename{"org-repo-branch.yaml": afterConfig}
			return before, after
		},
		expected: func() config.DataByFilename {
			expected := config.DataWithInfo{}
			if err := deepcopy.Copy(&expected, &baseCiopConfig); err != nil {
				t.Fatal(err)
			}
			expected.Configuration.Tests[0].RestrictNetworkAccess = utilpointer.Bool(false)
			expected.Configuration.Tests[1].RestrictNetworkAccess = utilpointer.Bool(true)
			return config.DataByFilename{"org-repo-branch.yaml": expected}
		},
		expectedAffectedJobs: map[string]sets.Set[string]{
			"org-repo-branch.yaml": {
				"unit": sets.Empty{},
				"e2e":  sets.Empty{},
			},
		},
		expectedDisabledDueToNetworkAccessToggle: []string{"pull-ci-org-repo-branch-unit"},
	}}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			before, after := tc.configGenerator(t)
			actual, affectedJobs, disabledDueToNetworkAccessToggle := GetChangedCiopConfigs(before, after, logrus.NewEntry(logrus.New()))
			expected := tc.expected()

			if diff := cmp.Diff(expected, actual); diff != "" {
				t.Errorf("Detected changed ci-operator config changes differ from expected:\n%s", diff)
			}

			if diff := cmp.Diff(tc.expectedAffectedJobs, affectedJobs, ignoreUnexported); diff != "" {
				t.Errorf("Affected jobs differ from expected:\n%s", diff)
			}

			if diff := cmp.Diff(tc.expectedDisabledDueToNetworkAccessToggle, disabledDueToNetworkAccessToggle); diff != "" {
				t.Errorf("Actual disabledDueToNetworkAccessToggle differs from expected:\n%s", diff)
			}
		})
	}
}

func TestGetChangedPresubmits(t *testing.T) {
	basePresubmit := []prowconfig.Presubmit{
		{
			JobBase: prowconfig.JobBase{
				Agent:   "kubernetes",
				Cluster: "api.ci",
				Name:    "test-base-presubmit",
				Labels:  map[string]string{"pj-rehearse.openshift.io/source-type": "changedPresubmit"},
				Spec: &v1.PodSpec{
					Containers: []v1.Container{{
						Command: []string{"ci-operator"},
						Args:    []string{"--target=images"},
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
		{
			name: "different cluster",
			configGenerator: func(t *testing.T) (*prowconfig.Config, *prowconfig.Config) {
				var p, base []prowconfig.Presubmit
				if err := deepcopy.Copy(&p, basePresubmit); err != nil {
					t.Fatal(err)
				}
				if err := deepcopy.Copy(&base, basePresubmit); err != nil {
					t.Fatal(err)
				}

				base[0].Cluster = "build01"
				return makeConfig(base), makeConfig(p)

			},
			expected: config.Presubmits{
				"org/repo": basePresubmit,
			},
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
		Metadata: cioperatorapi.Metadata{
			Org:    "org",
			Repo:   "repo",
			Branch: "branch",
		},
		Filename: "org-repo-branch.yaml",
	}

	affectedJobs := map[string]sets.Set[string]{
		"org-repo-branch.yaml": {
			"testjob": sets.Empty{},
		},
	}

	testCases := []struct {
		description        string
		prow               *prowconfig.Config
		ciop               config.DataByFilename
		expectedPresubmits config.Presubmits
		expectedPeriodics  config.Periodics
	}{
		{
			description: "return a presubmit using one of the input ciop configs",
			prow: &prowconfig.Config{
				JobConfig: prowconfig.JobConfig{
					PresubmitsStatic: map[string][]prowconfig.Presubmit{
						"org/repo": {
							prowconfig.Presubmit{
								JobBase: prowconfig.JobBase{
									Name:   baseCiopConfig.JobName(jobconfig.PresubmitPrefix, "testjob"),
									Labels: map[string]string{"pj-rehearse.openshift.io/source-type": "changedCiopConfigs"},
									Agent:  string(pjapi.KubernetesAgent),
								},
							},
						},
					},
				},
			},
			ciop:              config.DataByFilename{baseCiopConfig.Filename: {Info: baseCiopConfig}},
			expectedPeriodics: config.Periodics{},
			expectedPresubmits: config.Presubmits{
				"org/repo": {
					prowconfig.Presubmit{
						JobBase: prowconfig.JobBase{
							Name:   baseCiopConfig.JobName(jobconfig.PresubmitPrefix, "testjob"),
							Labels: map[string]string{"pj-rehearse.openshift.io/source-type": "changedCiopConfigs"},
							Agent:  string(pjapi.KubernetesAgent),
						},
					},
				},
			},
		},
		{
			description: "do not return a presubmit using a ciop config not present in input",
			prow: &prowconfig.Config{
				JobConfig: prowconfig.JobConfig{
					PresubmitsStatic: map[string][]prowconfig.Presubmit{
						"org/repo": {
							prowconfig.Presubmit{
								JobBase: prowconfig.JobBase{
									Name:   baseCiopConfig.JobName(jobconfig.PresubmitPrefix, "testjob"),
									Labels: map[string]string{"pj-rehearse.openshift.io/source-type": "changedCiopConfigs"},
									Agent:  string(pjapi.KubernetesAgent),
								},
							},
						}},
				},
			},
			ciop:               config.DataByFilename{},
			expectedPresubmits: config.Presubmits{},
			expectedPeriodics:  config.Periodics{},
		},
		{
			description: "handle jenkins presubmits",
			prow: &prowconfig.Config{
				JobConfig: prowconfig.JobConfig{
					PresubmitsStatic: map[string][]prowconfig.Presubmit{
						"org/repo": {
							prowconfig.Presubmit{
								JobBase: prowconfig.JobBase{
									Name:   baseCiopConfig.JobName(jobconfig.PresubmitPrefix, "testjob"),
									Labels: map[string]string{"pj-rehearse.openshift.io/source-type": "changedCiopConfigs"},
									Agent:  string(pjapi.JenkinsAgent),
								},
							},
						},
					},
				},
			},
			ciop:               config.DataByFilename{},
			expectedPresubmits: config.Presubmits{},
			expectedPeriodics:  config.Periodics{},
		},
		{
			description: "return a periodic using one of the input ciop configs",
			prow: &prowconfig.Config{
				JobConfig: prowconfig.JobConfig{
					Periodics: []prowconfig.Periodic{
						{
							JobBase: prowconfig.JobBase{
								Name:   baseCiopConfig.JobName(jobconfig.PeriodicPrefix, "testjob"),
								Labels: map[string]string{"pj-rehearse.openshift.io/source-type": "changedCiopConfigs"},
								Agent:  string(pjapi.KubernetesAgent),
							},
						},
					},
				},
			},
			ciop:               config.DataByFilename{baseCiopConfig.Filename: {Info: baseCiopConfig}},
			expectedPresubmits: config.Presubmits{},
			expectedPeriodics: config.Periodics{
				baseCiopConfig.JobName(jobconfig.PeriodicPrefix, "testjob"): {
					JobBase: prowconfig.JobBase{
						Name:   baseCiopConfig.JobName(jobconfig.PeriodicPrefix, "testjob"),
						Labels: map[string]string{"pj-rehearse.openshift.io/source-type": "changedCiopConfigs"},
						Agent:  string(pjapi.KubernetesAgent),
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			presubmits, periodics := GetJobsForCiopConfigs(tc.prow, tc.ciop, affectedJobs, logrus.WithField("test", tc.description))

			if diff := cmp.Diff(tc.expectedPresubmits, presubmits, ignoreUnexported); diff != "" {
				t.Errorf("Returned presubmits differ from expected:\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectedPeriodics, periodics, ignoreUnexported); diff != "" {
				t.Errorf("Returned periodics differ from expected:\n%s", diff)
			}
		})
	}
}

func podSpecReferencing(metadata cioperatorapi.Metadata) *v1.PodSpec {
	return &v1.PodSpec{
		Containers: []v1.Container{{
			Env: []v1.EnvVar{{
				ValueFrom: &v1.EnvVarSource{
					ConfigMapKeyRef: &v1.ConfigMapKeySelector{
						LocalObjectReference: v1.LocalObjectReference{
							Name: metadata.ConfigMapName(),
						},
						Key: metadata.Basename(),
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
		ciopConfigs config.DataByFilename
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
								Spec:  podSpecReferencing(cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "branch"}),
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
				"org-repo-branch.yaml": {Info: config.Info{Metadata: cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "branch"}}},
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
								Spec:  podSpecReferencing(cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "branch"}),
							},
						}},
					},
				},
			},
			ciopConfigs: map[string]config.DataWithInfo{
				"org-repo-branch.yaml": {Info: config.Info{Metadata: cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "branch"}}},
			},
			expected: []PostsubmitInContext{{
				Metadata: cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				Job: prowconfig.Postsubmit{
					JobBase: prowconfig.JobBase{
						Name:  "branch-ci-org-repo-branch-images",
						Agent: "kubernetes",
						Spec:  podSpecReferencing(cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "branch"}),
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
								Spec:  podSpecReferencing(cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "BRANCH"}),
							},
						}},
					},
				},
			},
			ciopConfigs: map[string]config.DataWithInfo{
				"org-repo-branch.yaml": {Info: config.Info{Metadata: cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "branch"}}},
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
								Spec:  podSpecReferencing(cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "branch"}),
							},
						}},
					},
				},
			},
			ciopConfigs: map[string]config.DataWithInfo{
				"org-repo-branch.yaml": {Info: config.Info{Metadata: cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "branch"}}},
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
									Name: p,
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
		profiles sets.Set[string]
		expected []string
	}{{
		id:       "empty",
		cfg:      &prowconfig.Config{},
		profiles: sets.New[string]("test-profile"),
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
		profiles: sets.New[string]("test-profile"),
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
		profiles: sets.New[string]("test-profile"),
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
		profiles: sets.New[string]("test-profile"),
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
		profiles: sets.New[string]("test-profile"),
		expected: []string{"uses-cluster-profile"},
	}} {
		t.Run(tc.id, func(t *testing.T) {
			ret := GetPresubmitsForClusterProfiles(tc.cfg, tc.profiles, logrus.WithField("test", tc.id))
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
				Agent:   "kubernetes",
				Cluster: "api.ci",
				Labels:  map[string]string{"pj-rehearse.openshift.io/source-type": "changedPeriodic"},
				Name:    "test-base-periodic",
				Spec: &v1.PodSpec{
					Containers: []v1.Container{{
						Command: []string{"ci-operator"},
						Args:    []string{"--target=images"},
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
		{
			name: "cluster changed",
			configGenerator: func(t *testing.T) (*prowconfig.Config, *prowconfig.Config) {
				var p []prowconfig.Periodic
				if err := deepcopy.Copy(&p, basePeriodic); err != nil {
					t.Fatal(err)
				}
				p[0].Cluster = "build01"
				return makeConfigWithPeriodics(basePeriodic), makeConfigWithPeriodics(p)

			},
			expected: func() config.Periodics {
				var p []prowconfig.Periodic
				if err := deepcopy.Copy(&p, basePeriodic); err != nil {
					t.Fatal(err)
				}
				p[0].Cluster = "build01"
				return config.Periodics{p[0].Name: p[0]}
			}(),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			before, after := testCase.configGenerator(t)
			p := GetChangedPeriodics(before, after, logrus.NewEntry(logrus.New()))
			if !reflect.DeepEqual(testCase.expected, p) {
				t.Fatalf(cmp.Diff(testCase.expected, p, ignoreUnexported))
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

func TestRehearsableDifferences(t *testing.T) {
	masterAgent := string(pjapi.KubernetesAgent)
	masterCluster := "api.ci"
	masterSpec := &v1.PodSpec{
		Containers: []v1.Container{{Name: "master"}},
	}
	masterJob := prowconfig.JobBase{Agent: masterAgent, Cluster: masterCluster, Spec: masterSpec}

	testCases := []struct {
		name   string
		master prowconfig.JobBase
		job    prowconfig.JobBase

		expected []string
	}{
		{
			name:   "nothing differs",
			master: masterJob,
			job:    masterJob,
		},
		{
			name:   "cluster differs",
			master: masterJob,
			job: prowconfig.JobBase{
				Agent:   masterAgent,
				Cluster: "OCP 6.7 Cluster",
				Spec:    masterSpec,
			},
			expected: []string{"cluster changed"},
		},
		{
			name:   "spec differs",
			master: masterJob,
			job: prowconfig.JobBase{
				Agent:   masterAgent,
				Cluster: masterCluster,
				Spec: &v1.PodSpec{
					Containers: []v1.Container{{Name: "NOT MASTER"}},
				},
			},
			expected: []string{"spec changed"},
		},
		{
			name: "agent differs",
			master: prowconfig.JobBase{
				Agent:   string(pjapi.JenkinsAgent),
				Cluster: masterCluster,
				Spec:    masterSpec,
			},
			job:      masterJob,
			expected: []string{"agent changed"},
		},
		{
			name: "everything differs",
			master: prowconfig.JobBase{
				Agent:   string(pjapi.JenkinsAgent),
				Cluster: "OCP 6.7 Cluster",
				Spec: &v1.PodSpec{
					Containers: []v1.Container{{Name: "NOT MASTER"}},
				},
			},
			job:      masterJob,
			expected: []string{"agent changed", "spec changed", "cluster changed"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reasons := rehearsableDifferences(tc.master, tc.job)
			if !reflect.DeepEqual(reasons, tc.expected) {
				t.Errorf("%s: expected '%v', got '%v'", tc.name, tc.expected, reasons)
			}
		})
	}
}
