package prowgen

import (
	"sort"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	utilpointer "k8s.io/utils/pointer"

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

func TestGeneratePresubmitForTest(t *testing.T) {
	tests := []struct {
		description string

		test              string
		repoInfo          *ProwgenInfo
		jobRelease        string
		clone             bool
		runIfChanged      string
		skipIfOnlyChanged string
		optional          bool
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
			description:  "presubmit with run_if_changed",
			test:         "testname",
			repoInfo:     &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			runIfChanged: "^README.md$",
		},
		{
			description:       "presubmit with skip_if_only_changed",
			test:              "testname",
			repoInfo:          &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			skipIfOnlyChanged: "^README.md$",
		},
		{
			description: "optional presubmit",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			optional:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			test := ciop.TestStepConfiguration{As: tc.test}
			jobBaseGen := NewProwJobBaseBuilderForTest(&ciop.ReleaseBuildConfiguration{}, tc.repoInfo, newFakePodSpecBuilder(), test)
			testhelper.CompareWithFixture(t, generatePresubmitForTest(jobBaseGen, tc.test, tc.repoInfo, tc.runIfChanged, tc.skipIfOnlyChanged, tc.optional))
		})
	}
}

func TestGeneratePeriodicForTest(t *testing.T) {
	tests := []struct {
		description string

		test              string
		repoInfo          *ProwgenInfo
		jobRelease        string
		clone             bool
		cron              string
		interval          string
		releaseController bool
	}{
		{
			description: "periodic for standard test",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			cron:        "@yearly",
		},
		{
			description: "periodic for a test in a variant config",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "also"}},
			cron:        "@yearly",
		},
		{
			description: "periodic using interval",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			interval:    "6h",
		},
	}
	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			test := ciop.TestStepConfiguration{As: tc.test}
			jobBaseGen := NewProwJobBaseBuilderForTest(&ciop.ReleaseBuildConfiguration{}, tc.repoInfo, newFakePodSpecBuilder(), test)
			testhelper.CompareWithFixture(t, GeneratePeriodicForTest(jobBaseGen, tc.repoInfo, tc.cron, tc.interval, tc.releaseController, nil))
		})
	}
}

func TestGeneratePostSubmitForTest(t *testing.T) {
	testname := "postsubmit"
	tests := []struct {
		name       string
		repoInfo   *ProwgenInfo
		jobRelease string
		clone      bool
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
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			test := ciop.TestStepConfiguration{As: testname}
			jobBaseGen := NewProwJobBaseBuilderForTest(&ciop.ReleaseBuildConfiguration{}, tc.repoInfo, newFakePodSpecBuilder(), test)
			testhelper.CompareWithFixture(t, generatePostsubmitForTest(jobBaseGen, tc.repoInfo))
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
				PromotionConfiguration: &ciop.PromotionConfiguration{Namespace: "ci"},
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
					Namespace: "ci",
					AdditionalImages: map[string]string{
						"out": "out-1",
					},
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
					{As: "unit", Cron: utilpointer.StringPtr(cron), Cluster: "build01", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "bin"}},
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
	}

	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			jobConfig := GenerateJobs(tc.config, tc.repoInfo)
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
