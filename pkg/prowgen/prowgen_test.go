package prowgen

import (
	"sort"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	utilpointer "k8s.io/utils/pointer"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/api"
	ciop "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func sorted(spec *corev1.PodSpec) {
	container := &spec.Containers[0]
	sort.Slice(spec.Volumes, func(i, j int) bool {
		return spec.Volumes[i].Name < spec.Volumes[j].Name
	})
	sort.Slice(container.Env, func(i, j int) bool {
		return container.Env[i].Name < container.Env[j].Name
	})
	sort.Slice(container.VolumeMounts, func(i, j int) bool {
		return container.VolumeMounts[i].Name < container.VolumeMounts[j].Name
	})

	canSortArgs := true
	for i := range container.Args {
		if !strings.HasPrefix(container.Args[i], "--") {
			canSortArgs = false
			break
		}
	}
	if canSortArgs {
		sort.Strings(container.Args)
	}
}

func TestGeneratePresubmitForTest(t *testing.T) {
	tests := []struct {
		description string

		test           string
		repoInfo       *ProwgenInfo
		jobRelease     string
		clone          bool
		generateOption generatePresubmitOption
	}{
		{
			description: "presubmit for standard test",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
		},
		{
			description: "presubmit for a test in a variant config",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "also"}},
		},
		{
			description: "presubmit with always_run false",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *generatePresubmitOptions) {
				options.defaultDisable = true
			},
		},
		{
			description: "presubmit with always_run but run_if_changed set",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *generatePresubmitOptions) {
				options.defaultDisable = true
				options.runIfChanged = ".*"
			},
		},
		{
			description: "presubmit with always_run but pipeline_run_if_changed set",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *generatePresubmitOptions) {
				options.defaultDisable = true
				options.pipelineRunIfChanged = ".*"
			},
		},
		{
			description: "presubmit with always_run=false and pipeline_run_if_changed",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *generatePresubmitOptions) {
				options.defaultDisable = false
				options.pipelineRunIfChanged = ".*"
			},
		},
		{
			description: "presubmit with always_run but optional true",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *generatePresubmitOptions) {
				options.defaultDisable = true
				options.optional = true
			},
		},
		{
			description: "presubmit with run_if_changed",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *generatePresubmitOptions) {
				options.runIfChanged = "^README.md$"
			},
		},
		{
			description: "presubmit with skip_if_only_changed",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *generatePresubmitOptions) {
				options.skipIfOnlyChanged = "^README.md$"
			},
		},
		{
			description: "optional presubmit",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *generatePresubmitOptions) {
				options.optional = true
			},
		},
		{
			description: "rehearsal disabled",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *generatePresubmitOptions) {
				options.disableRehearsal = true
			},
		},
		{
			description: "capabilities added",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *generatePresubmitOptions) {
				options.Capabilities = []string{"vpn", "arm64"}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			generateOption := tc.generateOption
			if generateOption == nil {
				generateOption = func(options *generatePresubmitOptions) {}
			}
			test := ciop.TestStepConfiguration{As: tc.test}
			jobBaseGen := NewProwJobBaseBuilderForTest(&ciop.ReleaseBuildConfiguration{}, tc.repoInfo, newFakePodSpecBuilder(), test)
			testhelper.CompareWithFixture(t, generatePresubmitForTest(jobBaseGen, tc.test, tc.repoInfo, generateOption))
		})
	}
}

func TestGeneratePeriodicForTest(t *testing.T) {
	tests := []struct {
		description string

		test           string
		repoInfo       *ProwgenInfo
		jobRelease     string
		clone          bool
		generateOption GeneratePeriodicOption
	}{
		{
			description: "periodic for standard test",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.Cron = "@yearly"
			},
		},
		{
			description: "periodic for a test in a variant config",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "also"}},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.Cron = "@yearly"
			},
		},
		{
			description: "periodic using interval",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.Interval = "6h"
			},
		},
		{
			description: "periodic with disabled rehearsal",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.DisableRehearsal = true
				options.Cron = "@yearly"
			},
		},
		{
			description: "periodic using minimum_interval",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.MinimumInterval = "4h"
			},
		},
		{
			description: "periodic with capabilities",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.Cron = "@yearly"
				options.Capabilities = []string{"vpn", "arm64"}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			generateOption := tc.generateOption
			if generateOption == nil {
				generateOption = func(options *GeneratePeriodicOptions) {}
			}
			test := ciop.TestStepConfiguration{As: tc.test}
			jobBaseGen := NewProwJobBaseBuilderForTest(&ciop.ReleaseBuildConfiguration{}, tc.repoInfo, newFakePodSpecBuilder(), test)
			testhelper.CompareWithFixture(t, GeneratePeriodicForTest(jobBaseGen, tc.repoInfo, generateOption))
		})
	}
}

func TestGeneratePostSubmitForTest(t *testing.T) {
	testname := "postsubmit"
	tests := []struct {
		name           string
		repoInfo       *ProwgenInfo
		jobRelease     string
		generateOption generatePostsubmitOption
	}{
		{
			name: "Lowercase org repo and branch",
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		},
		{
			name: "Uppercase org, repo and branch",
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "Organization",
				Repo:   "Repository",
				Branch: "Branch",
			}},
		},
		{
			name: "postsubmit with run_if_changed",
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
			generateOption: func(options *generatePostsubmitOptions) {
				options.runIfChanged = "^README.md$"
			},
		},
		{
			name: "postsubmit with capabilities",
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
			generateOption: func(options *generatePostsubmitOptions) {
				options.Capabilities = []string{"vpn", "arm64"}
			},
		},
		{
			name: "postsubmit with skip_if_only_changed",
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
			generateOption: func(options *generatePostsubmitOptions) {
				options.skipIfOnlyChanged = "^README.md$"
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			generateOption := tc.generateOption
			if generateOption == nil {
				generateOption = func(options *generatePostsubmitOptions) {}
			}
			test := ciop.TestStepConfiguration{As: testname}
			jobBaseGen := NewProwJobBaseBuilderForTest(&ciop.ReleaseBuildConfiguration{}, tc.repoInfo, newFakePodSpecBuilder(), test)
			testhelper.CompareWithFixture(t, generatePostsubmitForTest(jobBaseGen, tc.repoInfo, generateOption))
		})
	}
}

const (
	cron = "0 0 * * *"
)

func TestGenerateJobs(t *testing.T) {
	tests := []struct {
		id       string
		keep     bool
		config   *ciop.ReleaseBuildConfiguration
		repoInfo *ProwgenInfo
	}{
		{
			id: "two tests and empty Images so only two test presubmits are generated",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}},
					{As: "leTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}}},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		}, {
			id: "two tests and nonempty Images so two test presubmits and images pre/postsubmits are generated ",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}},
					{As: "leTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}}},
				Images:                 []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				PromotionConfiguration: &ciop.PromotionConfiguration{},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		}, {
			id: "template test",
			config: &ciop.ReleaseBuildConfiguration{
				InputConfiguration: ciop.InputConfiguration{
					ReleaseTagConfiguration: &ciop.ReleaseTagConfiguration{Name: "origin-v4.0"}},
				Tests: []ciop.TestStepConfiguration{
					{
						As: "oTeste",
						OpenshiftAnsibleClusterTestConfiguration: &ciop.OpenshiftAnsibleClusterTestConfiguration{
							ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
						},
					},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		}, {
			id: "Promotion configuration causes --promote job",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:                  []ciop.TestStepConfiguration{},
				Images:                 []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				PromotionConfiguration: &ciop.PromotionConfiguration{Targets: []api.PromotionTarget{{Namespace: "ci"}}},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		}, {
			id:   "Promotion configuration causes --promote job with unique targets",
			keep: true,
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{},
				Images: []ciop.ProjectDirectoryImageBuildStepConfiguration{
					{To: "out-1", From: "base"},
					{To: "out-2", From: "base"},
				},
				PromotionConfiguration: &ciop.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "ci",
						AdditionalImages: map[string]string{
							"out": "out-1",
						},
					}},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		}, {
			id: "no Promotion configuration has no branch job",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:  []ciop.TestStepConfiguration{},
				Images: []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				InputConfiguration: ciop.InputConfiguration{
					ReleaseTagConfiguration: &ciop.ReleaseTagConfiguration{Namespace: "openshift"},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		}, {
			id: "operator section creates ci-index presubmit job",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{},
				Operator: &ciop.OperatorStepConfiguration{
					Bundles: []ciop.Bundle{{
						DockerfilePath: "bundle.Dockerfile",
						ContextDir:     "manifests",
					}},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		}, {
			id:   "operator section creates ci-index-my-bundle presubmit job",
			keep: true,
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{},
				Operator: &ciop.OperatorStepConfiguration{
					Bundles: []ciop.Bundle{{
						As:             "my-bundle",
						DockerfilePath: "bundle.Dockerfile",
						ContextDir:     "manifests",
					}},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		}, {
			id:   "operator section without index creates ci-index-my-bundle presubmit job",
			keep: true,
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{},
				Operator: &ciop.OperatorStepConfiguration{
					Bundles: []ciop.Bundle{{
						As:                "my-bundle",
						DockerfilePath:    "bundle.Dockerfile",
						ContextDir:        "manifests",
						SkipBuildingIndex: true,
					}},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		}, {
			id: "two tests and empty Images with one test configured as a postsubmit",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}},
					{As: "leTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}, Postsubmit: true}},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		}, {
			id: "kvm label",
			config: &ciop.ReleaseBuildConfiguration{
				Resources: map[string]ciop.ResourceRequirements{
					"*": {Requests: ciop.ResourceList{"devices.kubevirt.io/kvm": "1"}},
				},
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		},
		{
			id: "cluster label for presubmit",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", Cluster: "build01", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		},
		{
			id: "cluster label for periodic",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", Cron: utilpointer.String(cron), Cluster: "build01", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		},
		{
			id: "periodic with presubmit",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", Cron: utilpointer.String(cron), Presubmit: true, Cluster: "build01", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		},
		{
			id: "cluster label for postsubmit",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", Postsubmit: true, Cluster: "build01", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		},
		{
			id: "disabled rehearsals at job level",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
					{As: "lint", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
					{As: "periodic-unit", Cron: utilpointer.String(cron), ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
					{As: "periodic-lint", Cron: utilpointer.String(cron), ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ProwgenInfo{
				Config: config.Prowgen{Rehearsals: config.Rehearsals{DisabledRehearsals: []string{"unit", "periodic-unit"}}},
				Metadata: ciop.Metadata{
					Org:    "organization",
					Repo:   "repository",
					Branch: "branch",
				}},
		},
		{
			id: "disabled rehearsals at repo level",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
					{As: "periodic-unit", Cron: utilpointer.String(cron), ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ProwgenInfo{
				Config: config.Prowgen{Rehearsals: config.Rehearsals{DisableAll: true}},
				Metadata: ciop.Metadata{
					Org:    "organization",
					Repo:   "repository",
					Branch: "branch",
				}},
		},
		{
			id: "multiarch postsubmit images",
			config: &ciop.ReleaseBuildConfiguration{
				Images: []ciop.ProjectDirectoryImageBuildStepConfiguration{
					{
						From:                    "os",
						To:                      "ci-tools",
						AdditionalArchitectures: []string{"arm64"},
					},
					{
						From:                    "os",
						To:                      "test",
						AdditionalArchitectures: []string{"arm64", "ppc64-le"},
					},
				},
				PromotionConfiguration: &ciop.PromotionConfiguration{},
			},
			repoInfo: &ProwgenInfo{
				Metadata: ciop.Metadata{
					Org:    "organization",
					Repo:   "repository",
					Branch: "branch",
				},
			},
		},
		{
			id: "multiarch test job",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{
						As:               "unit",
						NodeArchitecture: "arm64",
					},
				},
			},
			repoInfo: &ProwgenInfo{
				Metadata: ciop.Metadata{
					Org:    "organization",
					Repo:   "repository",
					Branch: "branch",
				},
			},
		},
		{
			id: "images job is configured for slack reporting",
			config: &ciop.ReleaseBuildConfiguration{
				Images:                 []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				PromotionConfiguration: &ciop.PromotionConfiguration{},
			},
			repoInfo: &ProwgenInfo{
				Metadata: ciop.Metadata{
					Org:    "organization",
					Repo:   "repository",
					Branch: "branch",
				},
				Config: config.Prowgen{
					SlackReporterConfigs: []config.SlackReporterConfig{
						{
							Channel:           "some-channel",
							JobStatesToReport: []prowv1.ProwJobState{"error"},
							ReportTemplate:    "some template",
							JobNames:          []string{"images", "e2e"},
						},
					},
				},
			},
		},
		{
			id: "periodic with capabilities",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", Capabilities: []string{"vpn"}, Cron: utilpointer.String(cron), ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		},
		{
			id: "periodic/presubmit with capabilities",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", Capabilities: []string{"vpn", "arm64"}, Cron: utilpointer.String(cron), Presubmit: true, ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			jobConfig, err := GenerateJobs(tc.config, tc.repoInfo)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.keep {
				pruneForTests(jobConfig) // prune the fields that are tested in TestGeneratePre/PostsubmitForTest
			}
			testhelper.CompareWithFixture(t, sortPodspecsInJobconfig(jobConfig))
		})
	}
}

func sortPodspecsInJobconfig(jobConfig *prowconfig.JobConfig) *prowconfig.JobConfig {
	for repo := range jobConfig.PresubmitsStatic {
		for i := range jobConfig.PresubmitsStatic[repo] {
			if jobConfig.PresubmitsStatic[repo][i].Spec != nil {
				sorted(jobConfig.PresubmitsStatic[repo][i].Spec)
			}
		}
	}
	for repo := range jobConfig.PostsubmitsStatic {
		for i := range jobConfig.PostsubmitsStatic[repo] {
			if jobConfig.PostsubmitsStatic[repo][i].Spec != nil {
				sorted(jobConfig.PostsubmitsStatic[repo][i].Spec)
			}
		}
	}

	for i := range jobConfig.Periodics {
		if jobConfig.Periodics[i].Spec != nil {
			sorted(jobConfig.Periodics[i].Spec)
		}
	}

	return jobConfig
}

func pruneForTests(jobConfig *prowconfig.JobConfig) {
	for repo := range jobConfig.PresubmitsStatic {
		for i := range jobConfig.PresubmitsStatic[repo] {
			jobConfig.PresubmitsStatic[repo][i].AlwaysRun = false
			jobConfig.PresubmitsStatic[repo][i].Context = ""
			jobConfig.PresubmitsStatic[repo][i].Trigger = ""
			jobConfig.PresubmitsStatic[repo][i].RerunCommand = ""
			jobConfig.PresubmitsStatic[repo][i].Agent = ""
			jobConfig.PresubmitsStatic[repo][i].Spec = nil
			jobConfig.PresubmitsStatic[repo][i].Brancher = prowconfig.Brancher{}
			jobConfig.PresubmitsStatic[repo][i].UtilityConfig = prowconfig.UtilityConfig{}
		}
	}
	for repo := range jobConfig.PostsubmitsStatic {
		for i := range jobConfig.PostsubmitsStatic[repo] {
			jobConfig.PostsubmitsStatic[repo][i].Agent = ""
			jobConfig.PostsubmitsStatic[repo][i].Spec = nil
			jobConfig.PostsubmitsStatic[repo][i].Brancher = prowconfig.Brancher{}
			jobConfig.PostsubmitsStatic[repo][i].UtilityConfig = prowconfig.UtilityConfig{}
		}
	}
}
