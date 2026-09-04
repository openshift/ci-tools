package prowgen

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	utilpointer "k8s.io/utils/pointer"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/api"
	ciop "github.com/openshift/ci-tools/pkg/api"
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

func clusterProfileResolverFunc(profiles ...*api.ClusterProfile) func(string) (*api.ClusterProfile, error) {
	return func(name string) (*api.ClusterProfile, error) {
		for _, cp := range profiles {
			if cp.Name == name {
				return cp, nil
			}
		}
		return nil, fmt.Errorf("cluster profile %q not found", name)
	}
}

func TestShouldAlwaysRun(t *testing.T) {
	tests := []struct {
		description     string
		test            string
		alwaysRun       bool
		generateOptions generatePresubmitOption
	}{
		{
			description: "shouldAlwaysRun must return true",
			test:        "testname",
			alwaysRun:   true,
			generateOptions: func(options *generatePresubmitOptions) {
				options.runIfChanged = ""
				options.skipIfOnlyChanged = ""
				options.defaultDisable = false
				options.pipelineRunIfChanged = ""
				options.pipelineSkipIfOnlyChanged = ""
			},
		},
		{
			description: "shouldAlwaysRun must return false because runIfChanged is defined",
			test:        "testname",
			alwaysRun:   false,
			generateOptions: func(options *generatePresubmitOptions) {
				options.runIfChanged = "/docs/*"
				options.skipIfOnlyChanged = ""
				options.defaultDisable = false
				options.pipelineRunIfChanged = ""
				options.pipelineSkipIfOnlyChanged = ""
			},
		},
		{
			description: "shouldAlwaysRun must return false because defaultDisable is true",
			test:        "testname",
			alwaysRun:   false,
			generateOptions: func(options *generatePresubmitOptions) {
				options.runIfChanged = ""
				options.skipIfOnlyChanged = ""
				options.defaultDisable = true
				options.pipelineRunIfChanged = ""
				options.pipelineSkipIfOnlyChanged = ""
			},
		},
		{
			description: "shouldAlwaysRun must return false because pipelineSkipIfOnlyChanged is defined",
			test:        "testname",
			alwaysRun:   false,
			generateOptions: func(options *generatePresubmitOptions) {
				options.runIfChanged = ""
				options.skipIfOnlyChanged = ""
				options.defaultDisable = false
				options.pipelineRunIfChanged = ""
				options.pipelineSkipIfOnlyChanged = "^docs/"
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			options := &generatePresubmitOptions{}
			generateOptions := tc.generateOptions
			if generateOptions == nil {
				generateOptions = func(options *generatePresubmitOptions) {}
			}
			generateOptions(options)
			alwaysRun := options.shouldAlwaysRun()
			if tc.alwaysRun != alwaysRun {
				t.Errorf("got different always_run than exapected, should be %t but received %t", tc.alwaysRun, alwaysRun)
			}
		})
	}
}

func TestGeneratePresubmitForTest(t *testing.T) {
	clusterProfileResolver := clusterProfileResolverFunc(&ciop.ClusterProfile{
		Name:        "aws",
		ClusterType: "aws",
	})

	tests := []struct {
		description string

		test           ciop.TestStepConfiguration
		repoInfo       *ciop.Metadata
		jobRelease     string
		clone          bool
		generateOption generatePresubmitOption
	}{
		{
			description: "presubmit for standard test",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
		},
		{
			description: "presubmit for multistage test",
			test: ciop.TestStepConfiguration{
				As: "testname",
				MultiStageTestConfiguration: &ciop.MultiStageTestConfiguration{
					ClusterProfile: "aws",
				},
			},
			repoInfo: &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
		},
		{
			description: "presubmit for a test in a variant config",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "also"},
		},
		{
			description: "presubmit with always_run false",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.defaultDisable = true
			},
		},
		{
			description: "presubmit with always_run but run_if_changed set",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.defaultDisable = true
				options.runIfChanged = ".*"
			},
		},
		{
			description: "presubmit with always_run but pipeline_run_if_changed set",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.defaultDisable = true
				options.pipelineRunIfChanged = ".*"
			},
		},
		{
			description: "presubmit with always_run=false and pipeline_run_if_changed",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.defaultDisable = false
				options.pipelineRunIfChanged = ".*"
			},
		},
		{
			description: "presubmit with always_run but pipeline_skip_if_only_changed set",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.defaultDisable = true
				options.pipelineSkipIfOnlyChanged = "^docs/"
			},
		},
		{
			description: "presubmit with always_run=false and pipeline_skip_if_only_changed",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.defaultDisable = false
				options.pipelineSkipIfOnlyChanged = "^docs/"
			},
		},
		{
			description: "presubmit with always_run but optional true",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.defaultDisable = true
				options.optional = true
			},
		},
		{
			description: "presubmit with run_if_changed",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.runIfChanged = "^README.md$"
			},
		},
		{
			description: "presubmit with skip_if_only_changed",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.skipIfOnlyChanged = "^README.md$"
			},
		},
		{
			description: "optional presubmit",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.optional = true
			},
		},
		{
			description: "rehearsal disabled",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.disableRehearsal = true
			},
		},
		{
			description: "capabilities added",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.Capabilities = []string{"intranet", "arm64", "rce", "sshd-bastion"} // rce - release-controller-eligible, sshd-bastion - for multiarch P/Z libvirt jobs
			},
		},
		{
			description: "presubmit with max_concurrency",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.maxConcurrency = 4
			},
		},
		{
			description: "presubmit with skip_branches",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *generatePresubmitOptions) {
				options.skipBranches = []string{"^branch-foo$", "^branch-bar$"}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			generateOption := tc.generateOption
			if generateOption == nil {
				generateOption = func(options *generatePresubmitOptions) {}
			}
			jobBaseGen, err := NewProwJobBaseBuilderForTest(&ciop.ReleaseBuildConfiguration{}, tc.repoInfo, newFakePodSpecBuilder(), tc.test, clusterProfileResolver)
			if err != nil {
				t.Fatalf("failed to create the prowjob builder: %s", err)
			}
			testhelper.CompareWithFixture(t, generatePresubmitForTest(jobBaseGen, tc.test.As, tc.repoInfo, generateOption))
		})
	}
}

func TestGeneratePeriodicForTest(t *testing.T) {
	clusterProfileResolver := clusterProfileResolverFunc(&ciop.ClusterProfile{
		Name:        "aws",
		ClusterType: "aws",
	})

	tests := []struct {
		description string

		test           ciop.TestStepConfiguration
		repoInfo       *ciop.Metadata
		jobRelease     string
		clone          bool
		generateOption GeneratePeriodicOption
	}{
		{
			description: "periodic for standard test",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.Cron = "@yearly"
			},
		},
		{
			description: "periodic for multistage test",
			test: ciop.TestStepConfiguration{
				As: "testname",
				MultiStageTestConfiguration: &ciop.MultiStageTestConfiguration{
					ClusterProfile: "aws",
				},
			},
			repoInfo: &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.Cron = "@yearly"
			},
		},
		{
			description: "periodic for a test with retry",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.Cron = "@yearly"
				options.Retry = &prowconfig.Retry{RunAll: true, Attempts: 2, Interval: "3h"}
			},
		},
		{
			description: "periodic for a test in a variant config",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "also"},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.Cron = "@yearly"
			},
		},
		{
			description: "periodic using interval",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.Interval = "6h"
			},
		},
		{
			description: "periodic with disabled rehearsal",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.DisableRehearsal = true
				options.Cron = "@yearly"
			},
		},
		{
			description: "periodic using minimum_interval",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.MinimumInterval = "4h"
			},
		},
		{
			description: "periodic with capabilities",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.Cron = "@yearly"
				options.Capabilities = []string{"intranet", "arm64", "rce", "sshd-bastion"} // rce - release-controller-eligible, sshd-bastion - for multiarch P/Z libvirt jobs
			},
		},
		{
			description: "periodic with max_concurrency",
			test:        ciop.TestStepConfiguration{As: "testname"},
			repoInfo:    &ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			generateOption: func(options *GeneratePeriodicOptions) {
				options.Cron = "@yearly"
				options.MaxConcurrency = 3
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			generateOption := tc.generateOption
			if generateOption == nil {
				generateOption = func(options *GeneratePeriodicOptions) {}
			}
			jobBaseGen, err := NewProwJobBaseBuilderForTest(&ciop.ReleaseBuildConfiguration{},
				tc.repoInfo, newFakePodSpecBuilder(), tc.test, clusterProfileResolver)
			if err != nil {
				t.Fatalf("failed to create the prowjob builder: %s", err)
			}
			testhelper.CompareWithFixture(t, GeneratePeriodicForTest(jobBaseGen, tc.repoInfo, generateOption))
		})
	}
}

func TestGeneratePostSubmitForTest(t *testing.T) {
	testname := "postsubmit"
	tests := []struct {
		name           string
		repoInfo       *ciop.Metadata
		jobRelease     string
		generateOption generatePostsubmitOption
	}{
		{
			name: "Lowercase org repo and branch",
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			name: "Uppercase org, repo and branch",
			repoInfo: &ciop.Metadata{
				Org:    "Organization",
				Repo:   "Repository",
				Branch: "Branch",
			},
		},
		{
			name: "postsubmit with run_if_changed",
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
			generateOption: func(options *generatePostsubmitOptions) {
				options.runIfChanged = "^README.md$"
			},
		},
		{
			name: "postsubmit with capabilities",
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
			generateOption: func(options *generatePostsubmitOptions) {
				options.Capabilities = []string{"intranet", "arm64", "rce", "sshd-bastion"} // rce - release-controller-eligible, sshd-bastion - for multiarch P/Z libvirt jobs
			},
		},
		{
			name: "postsubmit with skip_if_only_changed",
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
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
			jobBaseGen, err := NewProwJobBaseBuilderForTest(&ciop.ReleaseBuildConfiguration{}, tc.repoInfo,
				newFakePodSpecBuilder(), test, func(clusterProfile string) (*ciop.ClusterProfile, error) { return nil, nil })
			if err != nil {
				t.Fatalf("failed to create the prowjob builder: %s", err)
			}

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
		repoInfo *ciop.Metadata
	}{
		{
			id: "two tests and empty Images so only two test presubmits are generated",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}},
					{As: "leTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}}},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		}, {
			id: "two tests and nonempty Images so two test presubmits and images pre/postsubmits are generated ",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}},
					{As: "leTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}}},
				Images:                 ciop.ImageConfiguration{Items: []ciop.ProjectDirectoryImageBuildStepConfiguration{{}}},
				PromotionConfiguration: &ciop.PromotionConfiguration{},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "promotion postsubmit and periodic ",
			config: &ciop.ReleaseBuildConfiguration{
				Images:                 ciop.ImageConfiguration{Items: []ciop.ProjectDirectoryImageBuildStepConfiguration{{}}},
				PromotionConfiguration: &ciop.PromotionConfiguration{Cron: "5 4 * * *"},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		}, {
			id: "Promotion configuration causes --promote job",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:                  []ciop.TestStepConfiguration{},
				Images:                 ciop.ImageConfiguration{Items: []ciop.ProjectDirectoryImageBuildStepConfiguration{{}}},
				PromotionConfiguration: &ciop.PromotionConfiguration{Targets: []api.PromotionTarget{{Namespace: "ci"}}},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		}, {
			id:   "Promotion configuration causes --promote job with unique targets",
			keep: true,
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{},
				Images: ciop.ImageConfiguration{Items: []ciop.ProjectDirectoryImageBuildStepConfiguration{
					{To: "out-1", From: "base"},
					{To: "out-2", From: "base"},
				}},
				PromotionConfiguration: &ciop.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "ci",
						AdditionalImages: map[string]string{
							"out": "out-1",
						},
					}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		}, {
			id: "no Promotion configuration has no branch job",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:  []ciop.TestStepConfiguration{},
				Images: ciop.ImageConfiguration{Items: []ciop.ProjectDirectoryImageBuildStepConfiguration{{}}},
				InputConfiguration: ciop.InputConfiguration{
					ReleaseTagConfiguration: &ciop.ReleaseTagConfiguration{Namespace: "openshift"},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
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
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
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
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
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
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		}, {
			id:   "operator section creates bundle with capabilities",
			keep: true,
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{},
				Operator: &ciop.OperatorStepConfiguration{
					Bundles: []ciop.Bundle{{
						As:             "my-bundle-caps",
						DockerfilePath: "bundle.Dockerfile",
						ContextDir:     "manifests",
						Capabilities:   []string{"privileged", "nested-kvm"},
					}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		}, {
			id:   "operator section creates ci-bundle-my-bundle presubmit job with skip_if_only_changed",
			keep: true,
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{},
				Operator: &ciop.OperatorStepConfiguration{
					Bundles: []ciop.Bundle{{
						As:                "my-bundle",
						DockerfilePath:    "bundle.Dockerfile",
						ContextDir:        "manifests",
						SkipBuildingIndex: true,
						SkipIfOnlyChanged: `^(docs/|.*\.md$)`,
					}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		}, {
			id: "operator bundle job with skip_if_only_changed propagated to presubmit",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{},
				Operator: &ciop.OperatorStepConfiguration{
					Bundles: []ciop.Bundle{{
						As:                "my-bundle",
						DockerfilePath:    "bundle.Dockerfile",
						ContextDir:        "manifests",
						SkipIfOnlyChanged: `^(docs/|.*\.md$)`,
					}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		}, {
			id: "operator bundle job with run_if_changed propagated to presubmit",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{},
				Operator: &ciop.OperatorStepConfiguration{
					Bundles: []ciop.Bundle{{
						As:             "my-bundle",
						DockerfilePath: "bundle.Dockerfile",
						ContextDir:     "manifests",
						RunIfChanged:   `^(Dockerfile|src/)`,
					}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		}, {
			id: "skip operator presubmits via ci-operator config",
			config: &ciop.ReleaseBuildConfiguration{
				Prowgen: &ciop.ProwgenOverrides{SkipOperatorPresubmits: true},
				Tests:   []ciop.TestStepConfiguration{},
				Operator: &ciop.OperatorStepConfiguration{
					Bundles: []ciop.Bundle{{
						As:             "my-bundle",
						DockerfilePath: "bundle.Dockerfile",
						ContextDir:     "manifests",
					}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		}, {
			id: "two tests and empty Images with one test configured as a postsubmit",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}},
					{As: "leTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}, Postsubmit: true}},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
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
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "cluster label for presubmit",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", Cluster: "build01", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "cluster label for periodic",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", Cron: utilpointer.String(cron), Cluster: "build01", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "periodic with presubmit",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", Cron: utilpointer.String(cron), Presubmit: true, Cluster: "build01", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "cluster label for postsubmit",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", Postsubmit: true, Cluster: "build01", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "disabled rehearsals at job level",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", DisableRehearsal: true, ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
					{As: "lint", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
					{As: "periodic-unit", DisableRehearsal: true, Cron: utilpointer.String(cron), ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
					{As: "periodic-lint", Cron: utilpointer.String(cron), ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "disabled rehearsals at repo level",
			config: &ciop.ReleaseBuildConfiguration{
				Prowgen: &ciop.ProwgenOverrides{DisableRehearsals: true},
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
					{As: "periodic-unit", Cron: utilpointer.String(cron), ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "ci-operator config overrides prowgen rehearsals",
			config: &ciop.ReleaseBuildConfiguration{
				Prowgen: &ciop.ProwgenOverrides{DisableRehearsals: true},
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "per-test disable rehearsal from ci-operator config",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", DisableRehearsal: true, ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
					{As: "lint", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "multiarch postsubmit images",
			config: &ciop.ReleaseBuildConfiguration{
				Images: ciop.ImageConfiguration{Items: []ciop.ProjectDirectoryImageBuildStepConfiguration{
					{
						From:                    "os",
						To:                      "ci-tools",
						AdditionalArchitectures: []string{"arm64"},
					},
					{
						From:                    "os",
						To:                      "test",
						AdditionalArchitectures: []string{"arm64", "ppc64le"},
					},
				}},
				PromotionConfiguration: &ciop.PromotionConfiguration{},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "multiarch postsubmit images, using capabilities",
			config: &ciop.ReleaseBuildConfiguration{
				Images: ciop.ImageConfiguration{Items: []ciop.ProjectDirectoryImageBuildStepConfiguration{
					{
						From:         "os",
						To:           "ci-tools",
						Capabilities: []string{"arm64"},
					},
					{
						From:         "os",
						To:           "test",
						Capabilities: []string{"arm64", "ppc64le"},
					},
				}},
				PromotionConfiguration: &ciop.PromotionConfiguration{},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "merge capabilities and architecture labels",
			config: &ciop.ReleaseBuildConfiguration{
				Images: ciop.ImageConfiguration{Items: []ciop.ProjectDirectoryImageBuildStepConfiguration{
					{
						From:                    "os",
						To:                      "ci-tools",
						Capabilities:            []string{"arm64"},
						AdditionalArchitectures: []string{"ppc64le"},
					},
				}},
				PromotionConfiguration: &ciop.PromotionConfiguration{},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
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
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "periodic with capabilities",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", Capabilities: []string{"intranet"}, Cron: utilpointer.String(cron), ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "periodic/presubmit with capabilities",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", Capabilities: []string{"intranet", "arm64", "rce", "sshd-bastion"}, Cron: utilpointer.String(cron), Presubmit: true, ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}}, // rce - release-controller-eligible, sshd-bastion - for multiarch P/Z libvirt jobs
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "sharded presubmit",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "unit", ShardCount: intPointer(3), ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "images job with skip_if_only_changed propagated to presubmit",
			config: &ciop.ReleaseBuildConfiguration{
				Images: ciop.ImageConfiguration{
					Items:             []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
					SkipIfOnlyChanged: `^(docs/|.*\.md$)`,
				},
				PromotionConfiguration: &ciop.PromotionConfiguration{},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "images job with run_if_changed propagated to presubmit",
			config: &ciop.ReleaseBuildConfiguration{
				Images: ciop.ImageConfiguration{
					Items:        []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
					RunIfChanged: `^(Dockerfile|src/)`,
				},
				PromotionConfiguration: &ciop.PromotionConfiguration{},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "images job with pipeline_skip_if_only_changed propagated to presubmit only",
			config: &ciop.ReleaseBuildConfiguration{
				Images: ciop.ImageConfiguration{
					Items:                     []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
					PipelineSkipIfOnlyChanged: `^(docs/|.*\.md$)`,
				},
				PromotionConfiguration: &ciop.PromotionConfiguration{},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "postsubmit with max_concurrency from test config",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}},
					{As: "leTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}, Postsubmit: true, MaxConcurrency: 6}},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id: "periodic with max_concurrency from test config",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}, Cron: utilpointer.String(cron), MaxConcurrency: 3},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
		{
			id:   "presubmit with per-test skip_branches",
			keep: true,
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", SkipBranches: []string{"^branch-foo$", "^branch-bar$"}, ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}},
				},
			},
			repoInfo: &ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			jobConfig, err := GenerateJobs(tc.config, tc.repoInfo, func(clusterProfile string) (*ciop.ClusterProfile, error) {
				return nil, nil
			})
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

func intPointer(val int) *int {
	return &val
}

func TestBundleWithCapabilities(t *testing.T) {
	tests := []struct {
		name                 string
		bundle               ciop.Bundle
		expectedCapabilities map[string]string
	}{
		{
			name: "bundle with multiple capabilities",
			bundle: ciop.Bundle{
				As:             "test-bundle",
				DockerfilePath: "bundle.Dockerfile",
				ContextDir:     "manifests",
				Capabilities:   []string{"privileged", "nested-kvm"},
			},
			expectedCapabilities: map[string]string{
				"capability/privileged": "privileged",
				"capability/nested-kvm": "nested-kvm",
			},
		},
		{
			name: "bundle with single capability",
			bundle: ciop.Bundle{
				As:             "test-bundle-single",
				DockerfilePath: "bundle.Dockerfile",
				ContextDir:     "manifests",
				Capabilities:   []string{"privileged"},
			},
			expectedCapabilities: map[string]string{
				"capability/privileged": "privileged",
			},
		},
		{
			name: "bundle without capabilities",
			bundle: ciop.Bundle{
				As:             "test-bundle-no-caps",
				DockerfilePath: "bundle.Dockerfile",
				ContextDir:     "manifests",
			},
			expectedCapabilities: map[string]string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := &ciop.ReleaseBuildConfiguration{
				Operator: &ciop.OperatorStepConfiguration{
					Bundles: []ciop.Bundle{tc.bundle},
				},
			}
			repoInfo := &ciop.Metadata{
				Org:    "test-org",
				Repo:   "test-repo",
				Branch: "main",
			}

			jobConfig, err := GenerateJobs(config, repoInfo, func(clusterProfile string) (*ciop.ClusterProfile, error) {
				return nil, nil
			})
			if err != nil {
				t.Fatalf("unexpected error generating jobs: %v", err)
			}

			if len(jobConfig.PresubmitsStatic) == 0 {
				t.Fatal("expected presubmits to be generated")
			}

			var bundleJob *prowconfig.Presubmit
			for _, presubmits := range jobConfig.PresubmitsStatic {
				for i := range presubmits {
					if strings.Contains(presubmits[i].Name, tc.bundle.As) {
						bundleJob = &presubmits[i]
						break
					}
				}
			}

			if bundleJob == nil {
				t.Fatalf("could not find presubmit job for bundle %s", tc.bundle.As)
			}

			for expectedLabel, expectedValue := range tc.expectedCapabilities {
				actualValue, exists := bundleJob.Labels[expectedLabel]
				if !exists {
					t.Errorf("expected label %s to be present in job labels", expectedLabel)
					continue
				}
				if actualValue != expectedValue {
					t.Errorf("expected label %s to have value %s, got %s", expectedLabel, expectedValue, actualValue)
				}
			}

			for label := range bundleJob.Labels {
				if strings.HasPrefix(label, "capability/") {
					if _, expected := tc.expectedCapabilities[label]; !expected {
						t.Errorf("unexpected capability label %s found in job", label)
					}
				}
			}
		})
	}
}

func TestProjectImageCapabilitiesForTest(t *testing.T) {
	config := &ciop.ReleaseBuildConfiguration{
		Images: ciop.ImageConfiguration{Items: []ciop.ProjectDirectoryImageBuildStepConfiguration{
			{
				To:                      "required-arm",
				Capabilities:            []string{"intranet"},
				AdditionalArchitectures: []string{"arm64"},
			},
			{To: "optional-kvm", Capabilities: []string{"nested-kvm"}, Optional: true},
			{To: "optional-child", From: "optional-kvm", Optional: true},
			{To: "independent"},
		}},
		InputConfiguration: ciop.InputConfiguration{Releases: map[string]ciop.UnresolvedRelease{
			"with-built-images":    {Integration: &ciop.Integration{IncludeBuiltImages: true}},
			"without-built-images": {Integration: &ciop.Integration{}},
		}},
	}

	tests := []struct {
		name     string
		test     ciop.TestStepConfiguration
		expected []string
	}{
		{
			name:     "container inherits capabilities through image dependencies",
			test:     ciop.TestStepConfiguration{ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "optional-child"}},
			expected: []string{"nested-kvm"},
		},
		{
			name:     "unrelated container is not constrained by other image builds",
			test:     ciop.TestStepConfiguration{ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "independent"}},
			expected: []string{},
		},
		{
			name: "cluster profile requires all non-optional images",
			test: ciop.TestStepConfiguration{MultiStageTestConfigurationLiteral: &ciop.MultiStageTestConfigurationLiteral{
				ClusterProfileLiteral: &ciop.ClusterProfileLiteral{},
			}},
			expected: []string{"arm64", "intranet"},
		},
		{
			name: "inline cluster profile requires all non-optional images",
			test: ciop.TestStepConfiguration{MultiStageTestConfiguration: &ciop.MultiStageTestConfiguration{
				ClusterProfile: "aws",
			}},
			expected: []string{"arm64", "intranet"},
		},
		{
			name: "literal step dependency selects optional image chain",
			test: ciop.TestStepConfiguration{MultiStageTestConfigurationLiteral: &ciop.MultiStageTestConfigurationLiteral{
				Test: []ciop.LiteralTestStep{{Dependencies: []ciop.StepDependency{{Name: "optional-child"}}}},
			}},
			expected: []string{"nested-kvm"},
		},
		{
			name: "literal dependency pullspec override is case insensitive",
			test: ciop.TestStepConfiguration{MultiStageTestConfigurationLiteral: &ciop.MultiStageTestConfigurationLiteral{
				Test:                []ciop.LiteralTestStep{{Dependencies: []ciop.StepDependency{{Name: "optional-child", Env: "OPTIONAL_IMAGE"}}}},
				DependencyOverrides: ciop.DependencyOverrides{"optional_image": "quay.io/example/external:latest"},
			}},
			expected: []string{},
		},
		{
			name: "unresolved dependency pullspec override is case insensitive",
			test: ciop.TestStepConfiguration{MultiStageTestConfiguration: &ciop.MultiStageTestConfiguration{
				Test:                []ciop.TestStep{{LiteralTestStep: &ciop.LiteralTestStep{Dependencies: []ciop.StepDependency{{Name: "optional-child", Env: "Optional_Image"}}}}},
				Dependencies:        ciop.TestDependencies{"OPTIONAL_IMAGE": "optional-child"},
				DependencyOverrides: ciop.DependencyOverrides{"optional_image": "quay.io/example/external:latest"},
			}},
			expected: []string{},
		},
		{
			name: "release assembled with built images requires non-optional images",
			test: ciop.TestStepConfiguration{MultiStageTestConfigurationLiteral: &ciop.MultiStageTestConfigurationLiteral{
				Test: []ciop.LiteralTestStep{{From: "release:with-built-images"}},
			}},
			expected: []string{"arm64", "intranet"},
		},
		{
			name: "release without built images does not select image builds",
			test: ciop.TestStepConfiguration{MultiStageTestConfigurationLiteral: &ciop.MultiStageTestConfigurationLiteral{
				Test: []ciop.LiteralTestStep{{From: "release:without-built-images"}},
			}},
			expected: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testhelper.Diff(t, "capabilities", projectImageCapabilitiesForTest(config, tc.test), tc.expected)
		})
	}
}

func TestGenerateJobsPropagatesDependentImageCapabilities(t *testing.T) {
	config := &ciop.ReleaseBuildConfiguration{
		Images: ciop.ImageConfiguration{Items: []ciop.ProjectDirectoryImageBuildStepConfiguration{
			{To: "required-arm", Capabilities: []string{"arm64"}},
			{To: "optional-kvm", Capabilities: []string{"nested-kvm"}, Optional: true},
			{To: "optional-child", From: "optional-kvm", Optional: true},
			{To: "independent"},
		}},
		Tests: []ciop.TestStepConfiguration{
			{
				As:                         "dependent",
				Capabilities:               []string{"intranet"},
				ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "optional-child"},
			},
			{
				As:                         "independent",
				ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "independent"},
			},
		},
	}
	jobConfig, err := GenerateJobs(config, &ciop.Metadata{Org: "org", Repo: "repo", Branch: "main"}, clusterProfileResolverFunc())
	if err != nil {
		t.Fatalf("generate jobs: %v", err)
	}

	var dependent, independent map[string]string
	for _, job := range jobConfig.PresubmitsStatic["org/repo"] {
		switch {
		case strings.HasSuffix(job.Name, "-dependent"):
			dependent = job.Labels
		case strings.HasSuffix(job.Name, "-independent"):
			independent = job.Labels
		}
	}
	if dependent == nil || independent == nil {
		t.Fatalf("failed to find generated test jobs: dependent=%v independent=%v", dependent != nil, independent != nil)
	}
	for key, value := range map[string]string{
		"capability/intranet":   "intranet",
		"capability/nested-kvm": "nested-kvm",
	} {
		if dependent[key] != value {
			t.Errorf("dependent job label %s: got %q, want %q", key, dependent[key], value)
		}
	}
	if _, ok := dependent["capability/arm64"]; ok {
		t.Error("dependent job unexpectedly inherited capability from an unrelated image")
	}
	for key := range independent {
		if strings.HasPrefix(key, "capability/") {
			t.Errorf("independent job unexpectedly has capability label %s", key)
		}
	}
}

func TestGenerateJobsPropagatesArm64ImageCapabilityToClusterProfileTest(t *testing.T) {
	config := &ciop.ReleaseBuildConfiguration{
		Images: ciop.ImageConfiguration{Items: []ciop.ProjectDirectoryImageBuildStepConfiguration{{
			To:                      "hypershift-operator",
			AdditionalArchitectures: []string{"arm64"},
		}}},
		Tests: []ciop.TestStepConfiguration{{
			As: "e2e-aks",
			MultiStageTestConfigurationLiteral: &ciop.MultiStageTestConfigurationLiteral{
				ClusterProfileLiteral: &ciop.ClusterProfileLiteral{Name: "hypershift-aks", ClusterType: "azure"},
			},
		}},
	}
	jobConfig, err := GenerateJobs(config, &ciop.Metadata{Org: "openshift", Repo: "hypershift", Branch: "main"}, clusterProfileResolverFunc())
	if err != nil {
		t.Fatalf("generate jobs: %v", err)
	}

	for _, job := range jobConfig.PresubmitsStatic["openshift/hypershift"] {
		if strings.HasSuffix(job.Name, "-e2e-aks") {
			if actual := job.Labels["capability/arm64"]; actual != "arm64" {
				t.Fatalf("generated e2e-aks capability/arm64 label: got %q, want %q", actual, "arm64")
			}
			return
		}
	}
	t.Fatal("failed to find generated e2e-aks ProwJob")
}
