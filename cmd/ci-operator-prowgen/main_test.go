package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"testing"

	ciop "github.com/openshift/ci-operator/pkg/api"
	kubeapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	prowconfig "k8s.io/test-infra/prow/config"
	prowkube "k8s.io/test-infra/prow/kube"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/diff"

	jc "github.com/openshift/ci-operator-prowgen/pkg/jobconfig"
)

func TestGeneratePodSpec(t *testing.T) {
	tests := []struct {
		configFile     string
		target         string
		additionalArgs []string

		expected *kubeapi.PodSpec
	}{
		{
			configFile:     "config.json",
			target:         "target",
			additionalArgs: []string{},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args:            []string{"--give-pr-author-access-to-namespace=true", "--artifact-dir=$(ARTIFACTS)", "--target=target"},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{{
						Name: "CONFIG_SPEC",
						ValueFrom: &kubeapi.EnvVarSource{
							ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "ci-operator-configs",
								},
								Key: "config.json",
							},
						},
					}},
				}},
			},
		},
		{
			configFile:     "config.yml",
			target:         "target",
			additionalArgs: []string{"--promote", "--some=thing"},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args:            []string{"--give-pr-author-access-to-namespace=true", "--artifact-dir=$(ARTIFACTS)", "--target=target", "--promote", "--some=thing"},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{{
						Name: "CONFIG_SPEC",
						ValueFrom: &kubeapi.EnvVarSource{
							ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "ci-operator-configs",
								},
								Key: "config.yml",
							},
						},
					}},
				}},
			},
		},
	}

	for _, tc := range tests {
		var podSpec *kubeapi.PodSpec
		if len(tc.additionalArgs) == 0 {
			podSpec = generatePodSpec(tc.configFile, tc.target)
		} else {
			podSpec = generatePodSpec(tc.configFile, tc.target, tc.additionalArgs...)
		}
		if !equality.Semantic.DeepEqual(podSpec, tc.expected) {
			t.Errorf("expected PodSpec diff:\n%s", diff.ObjectDiff(tc.expected, podSpec))
		}
	}
}

func TestGeneratePodSpecTemplate(t *testing.T) {
	tests := []struct {
		org        string
		repo       string
		configFile string
		release    string
		test       ciop.TestStepConfiguration

		expected *kubeapi.PodSpec
	}{
		{
			org:        "organization",
			repo:       "repo",
			configFile: "organization-repo-branch.json",
			release:    "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftAnsibleClusterTestConfiguration: &ciop.OpenshiftAnsibleClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
				},
			},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []kubeapi.Volume{
					{
						Name: "job-definition",
						VolumeSource: kubeapi.VolumeSource{
							ConfigMap: &kubeapi.ConfigMapVolumeSource{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "prow-job-cluster-launch-e2e",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: kubeapi.VolumeSource{
							Projected: &kubeapi.ProjectedVolumeSource{
								Sources: []kubeapi.VolumeProjection{
									{
										Secret: &kubeapi.SecretProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
												Name: "cluster-secrets-gcp",
											},
										},
									},
									{
										ConfigMap: &kubeapi.ConfigMapProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
												Name: "cluster-profile-gcp",
											},
										},
									},
								},
							},
						},
					},
				},
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test"},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{
						{
							Name: "CONFIG_SPEC",
							ValueFrom: &kubeapi.EnvVarSource{
								ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
									LocalObjectReference: kubeapi.LocalObjectReference{
										Name: "ci-operator-configs",
									},
									Key: "organization-repo-branch.json",
								},
							},
						},
						{Name: "CLUSTER_TYPE", Value: "gcp"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
						{Name: "RPM_REPO_OPENSHIFT_ORIGIN", Value: "https://rpms.svc.ci.openshift.org/openshift-origin-v4.0/"},
					},
					VolumeMounts: []kubeapi.VolumeMount{
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-e2e.yaml"},
					},
				}},
			},
		},
		{
			org:        "organization",
			repo:       "repo",
			configFile: "organization-repo-branch.json",
			release:    "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftInstallerClusterTestConfiguration: &ciop.OpenshiftInstallerClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "aws"},
				},
			},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []kubeapi.Volume{
					{
						Name: "job-definition",
						VolumeSource: kubeapi.VolumeSource{
							ConfigMap: &kubeapi.ConfigMapVolumeSource{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "prow-job-cluster-launch-installer-e2e",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: kubeapi.VolumeSource{
							Projected: &kubeapi.ProjectedVolumeSource{
								Sources: []kubeapi.VolumeProjection{
									{
										Secret: &kubeapi.SecretProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
												Name: "cluster-secrets-aws",
											},
										},
									},
								},
							},
						},
					},
				},
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test"},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{
						{
							Name: "CONFIG_SPEC",
							ValueFrom: &kubeapi.EnvVarSource{
								ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
									LocalObjectReference: kubeapi.LocalObjectReference{
										Name: "ci-operator-configs",
									},
									Key: "organization-repo-branch.json",
								},
							},
						},
						{Name: "CLUSTER_TYPE", Value: "aws"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
					},
					VolumeMounts: []kubeapi.VolumeMount{
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-installer-e2e.yaml"},
					},
				}},
			},
		},
	}

	for _, tc := range tests {
		var podSpec *kubeapi.PodSpec
		podSpec = generatePodSpecTemplate(tc.org, tc.repo, tc.configFile, tc.release, &tc.test)
		if !equality.Semantic.DeepEqual(podSpec, tc.expected) {
			t.Errorf("expected PodSpec diff:\n%s", diff.ObjectDiff(tc.expected, podSpec))
		}
	}
}

func TestGeneratePresubmitForTest(t *testing.T) {
	newTrue := true

	tests := []struct {
		name     string
		repoInfo *configFilePathElements
		expected *prowconfig.Presubmit
	}{{
		name:     "testname",
		repoInfo: &configFilePathElements{org: "org", repo: "repo", branch: "branch"},

		expected: &prowconfig.Presubmit{
			JobBase: prowconfig.JobBase{
				Agent: "kubernetes",
				Name:  "pull-ci-org-repo-branch-testname",
				UtilityConfig: prowconfig.UtilityConfig{
					DecorationConfig: &prowkube.DecorationConfig{SkipCloning: &newTrue},
					Decorate:         true,
				},
			},
			AlwaysRun:    true,
			Brancher:     prowconfig.Brancher{Branches: []string{"branch"}},
			Context:      "ci/prow/testname",
			RerunCommand: "/test testname",
			Trigger:      "(?m)^/test (?:.*? )?testname(?: .*?)?$",
		},
	}}
	for _, tc := range tests {
		presubmit := generatePresubmitForTest(tc.name, tc.repoInfo, nil) // podSpec tested in generatePodSpec
		if !equality.Semantic.DeepEqual(presubmit, tc.expected) {
			t.Errorf("expected presubmit diff:\n%s", diff.ObjectDiff(tc.expected, presubmit))
		}
	}
}

func TestGeneratePostSubmitForTest(t *testing.T) {
	newTrue := true
	tests := []struct {
		name     string
		repoInfo *configFilePathElements
		labels   map[string]string

		treatBranchesAsExplicit bool

		expected *prowconfig.Postsubmit
	}{
		{
			name: "name",
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "branch.yaml",
			},
			labels: map[string]string{},

			expected: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Agent: "kubernetes",
					Name:  "branch-ci-organization-repository-branch-name",
					UtilityConfig: prowconfig.UtilityConfig{
						DecorationConfig: &prowkube.DecorationConfig{SkipCloning: &newTrue},
						Decorate:         true,
					},
				},

				Brancher: prowconfig.Brancher{Branches: []string{"branch"}},
			},
		},
		{
			name: "Name",
			repoInfo: &configFilePathElements{
				org:            "Organization",
				repo:           "Repository",
				branch:         "Branch",
				configFilename: "config.yaml",
			},
			labels: map[string]string{"artifacts": "images"},

			expected: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Agent:  "kubernetes",
					Name:   "branch-ci-Organization-Repository-Branch-Name",
					Labels: map[string]string{"artifacts": "images"},
					UtilityConfig: prowconfig.UtilityConfig{
						DecorationConfig: &prowkube.DecorationConfig{SkipCloning: &newTrue},
						Decorate:         true,
					}},
				Brancher: prowconfig.Brancher{Branches: []string{"Branch"}},
			},
		},
		{
			name: "name",
			repoInfo: &configFilePathElements{
				org:            "Organization",
				repo:           "Repository",
				branch:         "Branch",
				configFilename: "config.yaml",
			},
			labels: map[string]string{"artifacts": "images"},

			treatBranchesAsExplicit: true,

			expected: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Agent:  "kubernetes",
					Name:   "branch-ci-Organization-Repository-Branch-name",
					Labels: map[string]string{"artifacts": "images"},
					UtilityConfig: prowconfig.UtilityConfig{
						DecorationConfig: &prowkube.DecorationConfig{SkipCloning: &newTrue},
						Decorate:         true,
					}},
				Brancher: prowconfig.Brancher{Branches: []string{"^Branch$"}},
			},
		},

		{
			name: "name",
			repoInfo: &configFilePathElements{
				org:            "Organization",
				repo:           "Repository",
				branch:         "Branch-.*",
				configFilename: "config.yaml",
			},
			labels: map[string]string{"artifacts": "images"},

			treatBranchesAsExplicit: true,

			expected: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Agent:  "kubernetes",
					Name:   "branch-ci-Organization-Repository-Branch-name",
					Labels: map[string]string{"artifacts": "images"},
					UtilityConfig: prowconfig.UtilityConfig{
						DecorationConfig: &prowkube.DecorationConfig{SkipCloning: &newTrue},
						Decorate:         true,
					}},
				Brancher: prowconfig.Brancher{Branches: []string{"Branch-.*"}},
			},
		},
	}
	for _, tc := range tests {
		postsubmit := generatePostsubmitForTest(tc.name, tc.repoInfo, tc.treatBranchesAsExplicit, tc.labels, nil) // podSpec tested in TestGeneratePodSpec
		if !equality.Semantic.DeepEqual(postsubmit, tc.expected) {
			t.Errorf("expected postsubmit diff:\n%s", diff.ObjectDiff(tc.expected, postsubmit))
		}
	}
}

func TestGenerateJobs(t *testing.T) {
	tests := []struct {
		id       string
		config   *ciop.ReleaseBuildConfiguration
		repoInfo *configFilePathElements

		expectedPresubmits  map[string][]string
		expectedPostsubmits map[string][]string
		expected            *prowconfig.JobConfig
	}{
		{
			id: "two tests and empty Images so only two test presubmits are generated",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}},
					{As: "leTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}}},
			},
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "pull-ci-organization-repository-branch-derTest"}},
					{JobBase: prowconfig.JobBase{Name: "pull-ci-organization-repository-branch-leTest"}},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{},
			},
		}, {
			id: "two tests and nonempty Images so two test presubmits and images pre/postsubmits are generated ",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}},
					{As: "leTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}}},
				Images: []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
			},
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "pull-ci-organization-repository-branch-derTest"}},
					{JobBase: prowconfig.JobBase{Name: "pull-ci-organization-repository-branch-leTest"}},
					{JobBase: prowconfig.JobBase{Name: "pull-ci-organization-repository-branch-images"}},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "branch-ci-organization-repository-branch-images"}},
				}},
			},
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
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "pull-ci-organization-repository-branch-oTeste"}},
				}},
			},
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
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "pull-ci-organization-repository-branch-oTeste"}},
				}},
			},
		}, {
			id: "Promotion.Namespace is 'openshift' so artifact label is added",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:                  []ciop.TestStepConfiguration{},
				Images:                 []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				PromotionConfiguration: &ciop.PromotionConfiguration{Namespace: "openshift"},
			},
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "pull-ci-organization-repository-branch-images"}},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{Name: "branch-ci-organization-repository-branch-images",
						Labels: map[string]string{"artifacts": "images"},
					},
				}}},
			},
		}, {
			id: "Promotion.Namespace is not 'openshift' so no artifact label is added",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:                  []ciop.TestStepConfiguration{},
				Images:                 []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				PromotionConfiguration: &ciop.PromotionConfiguration{Namespace: "ci"},
			},
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "pull-ci-organization-repository-branch-images"}},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "branch-ci-organization-repository-branch-images"}},
				}},
			},
		}, {
			id: "no Promotion but tag_specification.Namespace is 'openshift' so artifact label is added",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:  []ciop.TestStepConfiguration{},
				Images: []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				InputConfiguration: ciop.InputConfiguration{
					ReleaseTagConfiguration: &ciop.ReleaseTagConfiguration{Namespace: "openshift"},
				},
			},
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "pull-ci-organization-repository-branch-images"}},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:   "branch-ci-organization-repository-branch-images",
						Labels: map[string]string{"artifacts": "images"},
					}}},
				}},
		}, {
			id: "tag_specification.Namespace is not 'openshift' and no Promotion so artifact label is not added",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:  []ciop.TestStepConfiguration{},
				Images: []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				InputConfiguration: ciop.InputConfiguration{
					ReleaseTagConfiguration: &ciop.ReleaseTagConfiguration{Namespace: "ci"},
				},
			},
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "pull-ci-organization-repository-branch-images"}},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "branch-ci-organization-repository-branch-images"}},
				}},
			},
		}, {
			id: "tag_specification.Namespace is 'openshift' and Promotion.Namespace is 'ci' so artifact label is not added",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:  []ciop.TestStepConfiguration{},
				Images: []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				InputConfiguration: ciop.InputConfiguration{
					ReleaseTagConfiguration: &ciop.ReleaseTagConfiguration{Namespace: "openshift"},
				},
				PromotionConfiguration: &ciop.PromotionConfiguration{Namespace: "ci"},
			},
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "pull-ci-organization-repository-branch-images"}},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "branch-ci-organization-repository-branch-images"}},
				}},
			},
		}, {
			id: "tag_specification.Namespace is 'ci' and Promotion.Namespace is 'openshift' so artifact label is added",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:  []ciop.TestStepConfiguration{},
				Images: []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				InputConfiguration: ciop.InputConfiguration{
					ReleaseTagConfiguration: &ciop.ReleaseTagConfiguration{Namespace: "ci"},
				},
				PromotionConfiguration: &ciop.PromotionConfiguration{Namespace: "openshift"},
			},
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "pull-ci-organization-repository-branch-images"}},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:   "branch-ci-organization-repository-branch-images",
						Labels: map[string]string{"artifacts": "images"},
					}}}},
			},
		},
	}

	log.SetOutput(ioutil.Discard)
	for _, tc := range tests {
		jobConfig := generateJobs(tc.config, tc.repoInfo)

		prune(jobConfig) // prune the fields that are tested in TestGeneratePre/PostsubmitForTest

		if !equality.Semantic.DeepEqual(jobConfig, tc.expected) {
			t.Errorf("testcase: %s\nexpected job config diff:\n%s", tc.id, diff.ObjectDiff(tc.expected, jobConfig))
		}
	}
}

func prune(jobConfig *prowconfig.JobConfig) {
	for repo := range jobConfig.Presubmits {
		for i := range jobConfig.Presubmits[repo] {
			jobConfig.Presubmits[repo][i].AlwaysRun = false
			jobConfig.Presubmits[repo][i].Context = ""
			jobConfig.Presubmits[repo][i].Trigger = ""
			jobConfig.Presubmits[repo][i].RerunCommand = ""
			jobConfig.Presubmits[repo][i].Agent = ""
			jobConfig.Presubmits[repo][i].Spec = nil
			jobConfig.Presubmits[repo][i].Brancher = prowconfig.Brancher{}
			jobConfig.Presubmits[repo][i].UtilityConfig = prowconfig.UtilityConfig{}
		}
	}
	for repo := range jobConfig.Postsubmits {
		for i := range jobConfig.Postsubmits[repo] {
			jobConfig.Postsubmits[repo][i].Agent = ""
			jobConfig.Postsubmits[repo][i].Spec = nil
			jobConfig.Postsubmits[repo][i].Brancher = prowconfig.Brancher{}
			jobConfig.Postsubmits[repo][i].UtilityConfig = prowconfig.UtilityConfig{}
		}
	}
}

func TestExtractRepoElementsFromPath(t *testing.T) {
	testCases := []struct {
		path                   string
		expectedOrg            string
		expectedRepo           string
		expectedBranch         string
		expectedConfigFilename string
		expectedError          bool
	}{
		{"../../ci-operator/openshift/component/master.yaml", "openshift", "component", "master", "master.yaml", false},
		{"master.yaml", "", "", "", "", true},
		{"dir/master.yaml", "", "", "", "", true},
	}
	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			repoInfo, err := extractRepoElementsFromPath(tc.path)
			if !tc.expectedError {
				if err != nil {
					t.Errorf("returned unexpected error '%v", err)
				}
				if repoInfo.org != tc.expectedOrg {
					t.Errorf("org extracted incorrectly: got '%s', expected '%s'", repoInfo.org, tc.expectedOrg)
				}
				if repoInfo.repo != tc.expectedRepo {
					t.Errorf("repo extracted incorrectly: got '%s', expected '%s'", repoInfo.repo, tc.expectedRepo)
				}
				if repoInfo.branch != tc.expectedBranch {
					t.Errorf("branch extracted incorrectly: got '%s', expected '%s'", repoInfo.branch, tc.expectedBranch)
				}
				if repoInfo.configFilename != tc.expectedConfigFilename {
					t.Errorf("configFilename extracted incorrectly: got '%s', expected '%s'", repoInfo.configFilename, tc.expectedConfigFilename)
				}
			} else { // expected error
				if err == nil {
					t.Errorf("expected to return error, got org=%v", repoInfo)
				}
			}
		})
	}
}

func TestFromCIOperatorConfigToProwYaml(t *testing.T) {
	tests := []struct {
		id                         string
		org                        string
		component                  string
		branch                     string
		variant                    string
		configYAML                 []byte
		prowOldPresubmitYAML       []byte
		prowOldPostsubmitYAML      []byte
		prowExpectedPresubmitYAML  []byte
		prowExpectedPostsubmitYAML []byte
	}{
		{
			id:        "one test and images, no previous jobs. Expect test presubmit + pre/post submit images jobs",
			org:       "super",
			component: "duper",
			branch:    "branch",
			configYAML: []byte(`base_images:
  base:
    cluster: https://api.ci.openshift.org
    name: origin-v3.11
    namespace: openshift
    tag: base
build_root:
  image_stream_tag:
    cluster: https://api.ci.openshift.org
    name: release
    namespace: openshift
    tag: golang-1.10
images:
- from: base
  to: service-serving-cert-signer
resources:
  '*':
    limits:
      cpu: 500Mi
    requests:
      cpu: 10Mi
tag_specification:
  cluster: https://api.ci.openshift.org
  name: origin-v3.11
  namespace: openshift
  tag: ''
tests:
- as: unit
  commands: make test-unit
  container:
    from: src`),
			prowOldPresubmitYAML:  []byte(""),
			prowOldPostsubmitYAML: []byte(""),
			prowExpectedPostsubmitYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    branches:
    - ^branch$
    context: ""
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      artifacts: images
    name: branch-ci-super-duper-branch-images
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --promote
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
`),
			prowExpectedPresubmitYAML: []byte(`presubmits:
  super/duper:
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/images
    decorate: true
    decoration_config:
      skip_cloning: true
    name: pull-ci-super-duper-branch-images
    rerun_command: /test images
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
    trigger: '(?m)^/test (?:.*? )?images(?: .*?)?$'
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/unit
    decorate: true
    decoration_config:
      skip_cloning: true
    name: pull-ci-super-duper-branch-unit
    rerun_command: /test unit
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
    trigger: '(?m)^/test (?:.*? )?unit(?: .*?)?$'
`),
		}, {
			id:        "One test and images, one existing job. Expect one presubmit, pre/post submit images jobs. Existing job should not be changed.",
			org:       "super",
			component: "duper",
			branch:    "branch",
			variant:   "rhel",
			configYAML: []byte(`base_images:
  base:
    cluster: https://api.ci.openshift.org
    name: origin-v3.11
    namespace: openshift
    tag: base
build_root:
  image_stream_tag:
    cluster: https://api.ci.openshift.org
    name: release
    namespace: openshift
    tag: golang-1.10
images:
- from: base
  to: service-serving-cert-signer
resources:
  '*':
    limits:
      cpu: 500Mi
    requests:
      cpu: 10Mi
tag_specification:
  cluster: https://api.ci.openshift.org
  name: origin-v3.11
  namespace: openshift
  tag: ''
tests:
- as: unit
  commands: make test-unit
  container:
    from: src`),
			prowOldPresubmitYAML: []byte(""),
			prowOldPostsubmitYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    branches:
    - branch
    decorate: true
    decoration_config:
      skip_cloning: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
`),
			prowExpectedPresubmitYAML: []byte(`presubmits:
  super/duper:
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/rhel-images
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/variant: rhel
    name: pull-ci-super-duper-branch-rhel-images
    rerun_command: /test rhel-images
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch__rhel.yaml
              name: ci-operator-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
    trigger: '(?m)^/test (?:.*? )?rhel-images(?: .*?)?$'
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/rhel-unit
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/variant: rhel
    name: pull-ci-super-duper-branch-rhel-unit
    rerun_command: /test rhel-unit
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch__rhel.yaml
              name: ci-operator-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
    trigger: '(?m)^/test (?:.*? )?rhel-unit(?: .*?)?$'
`),
			prowExpectedPostsubmitYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    branches:
    - branch
    context: ""
    decorate: true
    decoration_config:
      skip_cloning: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
  - agent: kubernetes
    branches:
    - ^branch$
    context: ""
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      artifacts: images
      ci-operator.openshift.io/variant: rhel
    name: branch-ci-super-duper-branch-rhel-images
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --promote
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch__rhel.yaml
              name: ci-operator-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
`),
		}, {
			id:        "Input is YAML and it is correctly processed",
			org:       "super",
			component: "duper",
			branch:    "branch",
			configYAML: []byte(`base_images:
  base:
    cluster: https://api.ci.openshift.org
    name: origin-v3.11
    namespace: openshift
    tag: base
images:
- from: base
  to: service-serving-cert-signer
resources:
  '*':
    limits:
      cpu: 500Mi
    requests:
      cpu: 10Mi
tag_specification:
  cluster: https://api.ci.openshift.org
  name: origin-v3.11
  namespace: openshift
  tag: ''
build_root:
  image_stream_tag:
    cluster: https://api.ci.openshift.org
    namespace: openshift
    name: release
    tag: golang-1.10
tests:
- as: unit
  commands: make test-unit
  container:
    from: src
`),
			prowOldPresubmitYAML: []byte(""),
			prowOldPostsubmitYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    decorate: true
    decoration_config:
      skip_cloning: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
`),
			prowExpectedPresubmitYAML: []byte(`presubmits:
  super/duper:
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/images
    decorate: true
    decoration_config:
      skip_cloning: true
    name: pull-ci-super-duper-branch-images
    rerun_command: /test images
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
    trigger: '(?m)^/test (?:.*? )?images(?: .*?)?$'
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/unit
    decorate: true
    decoration_config:
      skip_cloning: true
    name: pull-ci-super-duper-branch-unit
    rerun_command: /test unit
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
    trigger: '(?m)^/test (?:.*? )?unit(?: .*?)?$'
`),
			prowExpectedPostsubmitYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    context: ""
    decorate: true
    decoration_config:
      skip_cloning: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
  - agent: kubernetes
    branches:
    - ^branch$
    context: ""
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      artifacts: images
    name: branch-ci-super-duper-branch-images
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --promote
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
`),
		},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			tempDir, err := ioutil.TempDir("", "prowgen-test")
			if err != nil {
				t.Fatalf("Unexpected error creating tmpdir: %v", err)
			}
			defer os.RemoveAll(tempDir)

			configDir := filepath.Join(tempDir, "config", tc.org, tc.component)
			if err = os.MkdirAll(configDir, os.ModePerm); err != nil {
				t.Fatalf("Unexpected error config dir: %v", err)
			}

			branch := tc.branch
			if len(tc.variant) > 0 {
				branch += "__" + tc.variant
			}

			fullConfigPath := filepath.Join(configDir, fmt.Sprintf("%s.yaml", branch))
			if err = ioutil.WriteFile(fullConfigPath, tc.configYAML, 0664); err != nil {
				t.Fatalf("Unexpected error writing config file: %v", err)
			}

			baseProwConfigDir := filepath.Join(tempDir, "jobs")
			fullProwConfigDir := filepath.Join(baseProwConfigDir, tc.org, tc.component)
			if err := os.MkdirAll(fullProwConfigDir, os.ModePerm); err != nil {
				t.Fatalf("Unexpected error creating jobs dir: %v", err)
			}
			presubmitPath := filepath.Join(fullProwConfigDir, fmt.Sprintf("%s-%s-%s-presubmits.yaml", tc.org, tc.component, tc.branch))
			if err = ioutil.WriteFile(presubmitPath, tc.prowOldPresubmitYAML, 0664); err != nil {
				t.Fatalf("Unexpected error writing old presubmits: %v", err)
			}
			postsubmitPath := filepath.Join(fullProwConfigDir, fmt.Sprintf("%s-%s-%s-postsubmits.yaml", tc.org, tc.component, tc.branch))
			if err = ioutil.WriteFile(postsubmitPath, tc.prowOldPostsubmitYAML, 0664); err != nil {
				t.Fatalf("Unexpected error writing old postsubmits: %v", err)
			}

			jobConfig, _, err := generateProwJobsFromConfigFile(fullConfigPath)
			if err != nil {
				t.Fatalf("Unexpected error generating jobs from config: %v", err)
			}
			err = jc.WriteToDir(baseProwConfigDir, tc.org, tc.component, jobConfig)
			if err != nil {
				t.Fatalf("Unexpected error writing jobs: %v", err)
			}

			presubmitData, err := ioutil.ReadFile(presubmitPath)
			if err != nil {
				t.Fatalf("Unexpected error reading generated presubmits: %v", err)
			}

			if bytes.Compare(presubmitData, tc.prowExpectedPresubmitYAML) != 0 {
				t.Errorf("Generated Prow presubmit YAML differs from expected!\n%s", diff.StringDiff(string(tc.prowExpectedPresubmitYAML), string(presubmitData)))
			}

			postsubmitData, err := ioutil.ReadFile(postsubmitPath)
			if err != nil {
				t.Fatalf("Unexpected error reading generated postsubmits: %v", err)
			}

			if bytes.Compare(postsubmitData, tc.prowExpectedPostsubmitYAML) != 0 {
				t.Errorf("Generated Prow postsubmit YAML differs from expected!\n%s", diff.StringDiff(string(tc.prowExpectedPostsubmitYAML), string(postsubmitData)))
			}
		})
	}
}
