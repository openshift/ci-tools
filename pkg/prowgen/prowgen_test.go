package prowgen

import (
	"fmt"
	"io/ioutil"
	"log"
	"testing"

	corev1 "k8s.io/api/core/v1"
	prowconfig "k8s.io/test-infra/prow/config"

	ciop "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestGeneratePodSpec(t *testing.T) {
	tests := []struct {
		description    string
		info           *ProwgenInfo
		secrets        []*ciop.Secret
		targets        []string
		additionalArgs []string
	}{
		{
			description: "standard use case",
			info:        &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			targets:     []string{"target"},
		},
		{
			description:    "additional args are included in podspec",
			info:           &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			targets:        []string{"target"},
			additionalArgs: []string{"--promote", "--some=thing"},
		},
		{
			description:    "additional args and secret are included in podspec",
			info:           &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			secrets:        []*ciop.Secret{{Name: "secret-name", MountPath: "/usr/local/test-secret"}},
			targets:        []string{"target"},
			additionalArgs: []string{"--promote", "--some=thing"},
		},
		{
			description: "multiple targets",
			info:        &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			targets:     []string{"target", "more", "and-more"},
		},
		{
			description: "private job",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				Config:   config.Prowgen{Private: true},
			},
			targets: []string{"target"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			testhelper.CompareWithFixture(t, generateCiOperatorPodSpec(tc.info, tc.secrets, tc.targets, tc.additionalArgs...))
		})
	}
}

func TestGeneratePodSpecMultiStage(t *testing.T) {
	info := ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}}
	test := ciop.TestStepConfiguration{
		As: "test",
		MultiStageTestConfiguration: &ciop.MultiStageTestConfiguration{
			ClusterProfile: ciop.ClusterProfileAWS,
		},
	}

	testhelper.CompareWithFixture(t, generatePodSpecMultiStage(&info, &test, true))
}

func TestGeneratePodSpecTemplate(t *testing.T) {
	tests := []struct {
		info    *ProwgenInfo
		release string
		test    ciop.TestStepConfiguration
	}{
		{
			info:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftAnsibleClusterTestConfiguration: &ciop.OpenshiftAnsibleClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
				},
			},
		},
		{
			info:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftInstallerClusterTestConfiguration: &ciop.OpenshiftInstallerClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "aws"},
				},
			},
		},
		{
			info:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftInstallerCustomTestImageClusterTestConfiguration: &ciop.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
					From:                     "pipeline:kubevirt-test",
					EnableNestedVirt:         true,
					NestedVirtImage:          "nested-virt-image-name",
				},
			},
		},
		{
			info:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftInstallerCustomTestImageClusterTestConfiguration: &ciop.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
					From:                     "pipeline:kubevirt-test",
					EnableNestedVirt:         true,
				},
			},
		},
		{
			info:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftInstallerCustomTestImageClusterTestConfiguration: &ciop.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
					From:                     "pipeline:kubevirt-test",
					NestedVirtImage:          "",
					EnableNestedVirt:         false,
				},
			},
		},
		{
			info:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftInstallerCustomTestImageClusterTestConfiguration: &ciop.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
					From:                     "pipeline:kubevirt-test",
				},
			},
		},
	}

	for idx, tc := range tests {
		t.Run(fmt.Sprintf("testcase-%d", idx), func(t *testing.T) {
			testhelper.CompareWithFixture(t, generatePodSpecTemplate(tc.info, tc.release, &tc.test))
		})
	}
}

func TestGeneratePresubmitForTest(t *testing.T) {
	tests := []struct {
		description string

		test       string
		repoInfo   *ProwgenInfo
		jobRelease string
	}{{
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
			description: "presubmit with job release specified",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			jobRelease:  "4.6",
		},
	}
	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			testhelper.CompareWithFixture(t, generatePresubmitForTest(tc.test, tc.repoInfo, jobconfig.Generated, nil, nil, tc.jobRelease)) // podSpec tested in generatePodSpec
		})
	}
}

func TestGeneratePeriodicForTest(t *testing.T) {
	tests := []struct {
		description string

		test       string
		repoInfo   *ProwgenInfo
		jobRelease string
	}{{
		description: "periodic for standard test",
		test:        "testname",
		repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
	},
		{
			description: "periodic for a test in a variant config",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "also"}},
		},
		{
			description: "periodic for specific release",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			jobRelease:  "4.6",
		},
	}
	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			testhelper.CompareWithFixture(t, generatePeriodicForTest(tc.test, tc.repoInfo, jobconfig.Generated, nil, true, "@yearly", nil, tc.jobRelease)) // podSpec tested in generatePodSpec
		})
	}
}

func TestGeneratePostSubmitForTest(t *testing.T) {
	tests := []struct {
		name       string
		repoInfo   *ProwgenInfo
		jobRelease string
	}{
		{
			name: "first",
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
		},
		{
			name: "second",
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "Organization",
				Repo:   "Repository",
				Branch: "Branch",
			}},
		},
		{
			name: "third",
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "Organization",
				Repo:   "Repository",
				Branch: "Branch",
			}},
		},
		{
			name: "fourth",
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
			jobRelease: "4.6",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testhelper.CompareWithFixture(t, generatePostsubmitForTest(tc.name, tc.repoInfo, jobconfig.Generated, nil, nil, tc.jobRelease)) // podSpec tested in TestGeneratePodSpec
		})
	}
}

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
			id: "template test which doesn't require `tag_specification`",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{{
					As: "oTeste",
					OpenshiftInstallerClusterTestConfiguration: &ciop.OpenshiftInstallerClusterTestConfiguration{
						ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
					},
				}},
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
		},
	}

	log.SetOutput(ioutil.Discard)
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			jobConfig := GenerateJobs(tc.config, tc.repoInfo, jobconfig.Generated)
			if !tc.keep {
				pruneForTests(jobConfig) // prune the fields that are tested in TestGeneratePre/PostsubmitForTest
			}
			testhelper.CompareWithFixture(t, jobConfig)
		})
	}
}

func TestGenerateJobBase(t *testing.T) {
	path := "/some/where"
	var testCases = []struct {
		testName    string
		name        string
		prefix      string
		info        *ProwgenInfo
		label       jobconfig.ProwgenLabel
		podSpec     *corev1.PodSpec
		rehearsable bool
		pathAlias   *string
	}{
		{
			testName: "no special options",
			name:     "test",
			prefix:   "pull",
			info:     &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			label:    jobconfig.Generated,
			podSpec:  &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
		},
		{
			testName:    "rehearsable",
			name:        "test",
			prefix:      "pull",
			info:        &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			label:       jobconfig.Generated,
			podSpec:     &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
			rehearsable: true,
		},
		{
			testName: "config variant",
			name:     "test",
			prefix:   "pull",
			info:     &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "whatever"}},
			label:    jobconfig.Generated,
			podSpec:  &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
		},
		{
			testName:  "path alias",
			name:      "test",
			prefix:    "pull",
			info:      &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "whatever"}},
			label:     jobconfig.Generated,
			podSpec:   &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
			pathAlias: &path,
		},
		{
			testName: "hidden job for private repos",
			name:     "test",
			prefix:   "pull",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				Config:   config.Prowgen{Private: true},
			},
			label:   jobconfig.Generated,
			podSpec: &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
		},
		{
			testName: "expose job for private repos with public results",
			name:     "test",
			prefix:   "pull",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				Config:   config.Prowgen{Private: true, Expose: true},
			},
			label:   jobconfig.Generated,
			podSpec: &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
		},
		{
			testName: "expose option set but not private",
			name:     "test",
			prefix:   "pull",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				Config:   config.Prowgen{Private: false, Expose: true},
			},
			label:   jobconfig.Generated,
			podSpec: &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			testhelper.CompareWithFixture(t, generateJobBase(testCase.name, testCase.prefix, testCase.info, testCase.label, testCase.podSpec, testCase.rehearsable, testCase.pathAlias, ""))
		})
	}
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
