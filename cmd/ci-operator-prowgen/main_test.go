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
	prowconfig "k8s.io/test-infra/prow/config"
	prowkube "k8s.io/test-infra/prow/kube"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/diff"
)

func TestGeneratePodSpec(t *testing.T) {
	tests := []struct {
		org            string
		repo           string
		configFile     string
		target         string
		additionalArgs []string

		expected *kubeapi.PodSpec
	}{
		{
			org:            "organization",
			repo:           "repo",
			configFile:     "config.json",
			target:         "target",
			additionalArgs: []string{},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []kubeapi.Container{{
					Image:   "ci-operator:latest",
					Command: []string{"ci-operator"},
					Args:    []string{"--artifact-dir=$(ARTIFACTS)", "--target=target"},
					Env: []kubeapi.EnvVar{{
						Name: "CONFIG_SPEC",
						ValueFrom: &kubeapi.EnvVarSource{
							ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "ci-operator-organization-repo",
								},
								Key: "config.json",
							},
						},
					}},
				}},
			},
		},
		{
			org:            "organization",
			repo:           "repo",
			configFile:     "config.yml",
			target:         "target",
			additionalArgs: []string{"--promote", "something"},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []kubeapi.Container{{
					Image:   "ci-operator:latest",
					Command: []string{"ci-operator"},
					Args:    []string{"--artifact-dir=$(ARTIFACTS)", "--target=target", "--promote", "something"},
					Env: []kubeapi.EnvVar{{
						Name: "CONFIG_SPEC",
						ValueFrom: &kubeapi.EnvVarSource{
							ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "ci-operator-organization-repo",
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
			podSpec = generatePodSpec(tc.org, tc.repo, tc.configFile, tc.target)
		} else {
			podSpec = generatePodSpec(tc.org, tc.repo, tc.configFile, tc.target, tc.additionalArgs...)
		}
		if !equality.Semantic.DeepEqual(podSpec, tc.expected) {
			t.Errorf("expected PodSpec diff:\n%s", diff.ObjectDiff(tc.expected, podSpec))
		}
	}
}

func TestGeneratePresubmitForTest(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		org      string
		repo     string
		branch   string
		expected *prowconfig.Presubmit
	}{
		{
			name:   "testname",
			target: "target",
			org:    "org",
			repo:   "repo",
			branch: "branch",

			expected: &prowconfig.Presubmit{
				Agent:        "kubernetes",
				AlwaysRun:    true,
				Brancher:     prowconfig.Brancher{Branches: []string{"branch"}},
				Context:      "ci/prow/testname",
				Name:         "pull-ci-org-repo-branch-testname",
				RerunCommand: "/test testname",
				Trigger:      `((?m)^/test( all| testname),?(\\s+|$))`,
				UtilityConfig: prowconfig.UtilityConfig{
					DecorationConfig: &prowkube.DecorationConfig{SkipCloning: true},
					Decorate:         true,
				},
			},
		},
	}
	for _, tc := range tests {
		presubmit := generatePresubmitForTest(testDescription{tc.name, tc.target}, tc.org, tc.repo, tc.branch, "config.yaml")
		presubmit.Spec = nil // tested in generatePodSpec

		if !equality.Semantic.DeepEqual(presubmit, tc.expected) {
			t.Errorf("expected presubmit diff:\n%s", diff.ObjectDiff(tc.expected, presubmit))
		}
	}
}

func TestGeneratePostSubmitForTest(t *testing.T) {
	tests := []struct {
		name           string
		target         string
		org            string
		repo           string
		branch         string
		labels         map[string]string
		additionalArgs []string

		expected *prowconfig.Postsubmit
	}{
		{
			name:           "name",
			target:         "target",
			org:            "organization",
			repo:           "repository",
			branch:         "branch",
			labels:         map[string]string{},
			additionalArgs: []string{},

			expected: &prowconfig.Postsubmit{
				Agent:    "kubernetes",
				Name:     "branch-ci-organization-repository-branch-name",
				Brancher: prowconfig.Brancher{Branches: []string{"branch"}},
				UtilityConfig: prowconfig.UtilityConfig{
					DecorationConfig: &prowkube.DecorationConfig{SkipCloning: true},
					Decorate:         true,
				},
			},
		},
		{
			name:           "name",
			target:         "target",
			org:            "organization",
			repo:           "repository",
			branch:         "branch",
			labels:         map[string]string{},
			additionalArgs: []string{"--promote", "additionalArg"},

			expected: &prowconfig.Postsubmit{
				Agent:    "kubernetes",
				Name:     "branch-ci-organization-repository-branch-name",
				Brancher: prowconfig.Brancher{Branches: []string{"branch"}},
				UtilityConfig: prowconfig.UtilityConfig{
					DecorationConfig: &prowkube.DecorationConfig{SkipCloning: true},
					Decorate:         true,
				},
			},
		},
		{
			name:           "Name",
			target:         "Target",
			org:            "Organization",
			repo:           "Repository",
			branch:         "Branch",
			labels:         map[string]string{"artifacts": "images"},
			additionalArgs: []string{"--promote", "additionalArg"},

			expected: &prowconfig.Postsubmit{
				Agent:    "kubernetes",
				Name:     "branch-ci-Organization-Repository-Branch-Name",
				Brancher: prowconfig.Brancher{Branches: []string{"Branch"}},
				Labels:   map[string]string{"artifacts": "images"},
				UtilityConfig: prowconfig.UtilityConfig{
					DecorationConfig: &prowkube.DecorationConfig{SkipCloning: true},
					Decorate:         true,
				},
			},
		},
	}
	for _, tc := range tests {
		var postsubmit *prowconfig.Postsubmit

		if len(tc.additionalArgs) == 0 {
			postsubmit = generatePostsubmitForTest(testDescription{tc.name, tc.target}, tc.org, tc.repo, tc.branch, "branch.yaml", tc.labels)
		} else {
			postsubmit = generatePostsubmitForTest(testDescription{tc.name, tc.target}, tc.org, tc.repo, tc.branch, "branch.yaml", tc.labels, tc.additionalArgs...)
			// tests that additional args were propagated to the PodSpec
			if !equality.Semantic.DeepEqual(postsubmit.Spec.Containers[0].Args[2:], tc.additionalArgs) {
				t.Errorf("additional args not propagated to postsubmit:\n%s", diff.ObjectDiff(tc.additionalArgs, postsubmit.Spec.Containers[0].Args[2:]))
			}
		}

		postsubmit.Spec = nil // tested in TestGeneratePodSpec

		if !equality.Semantic.DeepEqual(postsubmit, tc.expected) {
			t.Errorf("expected postsubmit diff:\n%s", diff.ObjectDiff(tc.expected, postsubmit))
		}
	}
}

func TestGenerateJobs(t *testing.T) {
	tests := []struct {
		id     string
		config *ciop.ReleaseBuildConfiguration
		org    string
		repo   string
		branch string

		expectedPresubmits  map[string][]string
		expectedPostsubmits map[string][]string
		expected            *prowconfig.JobConfig
	}{
		{
			id: "two tests and empty Images so only two test presubmits are generated",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{{As: "derTest"}, {As: "leTest"}},
			},
			org:    "organization",
			repo:   "repository",
			branch: "branch",
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "pull-ci-organization-repository-branch-derTest"},
					{Name: "pull-ci-organization-repository-branch-leTest"},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{},
			},
		}, {
			id: "two tests and nonempty Images so two test presubmits and images pre/postsubmits are generated ",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:  []ciop.TestStepConfiguration{{As: "derTest"}, {As: "leTest"}},
				Images: []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
			},
			org:    "organization",
			repo:   "repository",
			branch: "branch",
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "pull-ci-organization-repository-branch-derTest"},
					{Name: "pull-ci-organization-repository-branch-leTest"},
					{Name: "pull-ci-organization-repository-branch-images"},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "branch-ci-organization-repository-branch-images"},
				}},
			},
		}, {
			id: "Promotion.Namespace is 'openshift' so artifact label is added",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:                  []ciop.TestStepConfiguration{},
				Images:                 []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				PromotionConfiguration: &ciop.PromotionConfiguration{Namespace: "openshift"},
			},
			org:    "organization",
			repo:   "repository",
			branch: "branch",
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "pull-ci-organization-repository-branch-images"},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {{
					Name:   "branch-ci-organization-repository-branch-images",
					Labels: map[string]string{"artifacts": "images"},
				}}},
			},
		}, {
			id: "Promotion.Namespace is not 'openshift' so no artifact label is added",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:                  []ciop.TestStepConfiguration{},
				Images:                 []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				PromotionConfiguration: &ciop.PromotionConfiguration{Namespace: "ci"},
			},
			org:    "organization",
			repo:   "repository",
			branch: "branch",
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "pull-ci-organization-repository-branch-images"},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "branch-ci-organization-repository-branch-images"},
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
			org:    "organization",
			repo:   "repository",
			branch: "branch",
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "pull-ci-organization-repository-branch-images"},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {{
					Name:   "branch-ci-organization-repository-branch-images",
					Labels: map[string]string{"artifacts": "images"},
				}}},
			},
		}, {
			id: "tag_specification.Namespace is not 'openshift' and no Promotion so artifact label is not added",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:  []ciop.TestStepConfiguration{},
				Images: []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				InputConfiguration: ciop.InputConfiguration{
					ReleaseTagConfiguration: &ciop.ReleaseTagConfiguration{Namespace: "ci"},
				},
			},
			org:    "organization",
			repo:   "repository",
			branch: "branch",
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "pull-ci-organization-repository-branch-images"},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "branch-ci-organization-repository-branch-images"},
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
			org:    "organization",
			repo:   "repository",
			branch: "branch",
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "pull-ci-organization-repository-branch-images"},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "branch-ci-organization-repository-branch-images"},
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
			org:    "organization",
			repo:   "repository",
			branch: "branch",
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "pull-ci-organization-repository-branch-images"},
				}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {{
					Name:   "branch-ci-organization-repository-branch-images",
					Labels: map[string]string{"artifacts": "images"},
				}}},
			},
		},
	}

	log.SetOutput(ioutil.Discard)
	for _, tc := range tests {
		jobConfig := generateJobs(tc.config, tc.org, tc.repo, tc.branch, "branch.yml")

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
			org, repo, branch, configFile, err := extractRepoElementsFromPath(tc.path)
			if !tc.expectedError {
				if err != nil {
					t.Errorf("returned unexpected error '%v", err)
				}
				if org != tc.expectedOrg {
					t.Errorf("org extracted incorrectly: got '%s', expected '%s'", org, tc.expectedOrg)
				}
				if repo != tc.expectedRepo {
					t.Errorf("repo extracted incorrectly: got '%s', expected '%s'", repo, tc.expectedRepo)
				}
				if branch != tc.expectedBranch {
					t.Errorf("branch extracted incorrectly: got '%s', expected '%s'", branch, tc.expectedBranch)
				}
				if configFile != tc.expectedConfigFilename {
					t.Errorf("configFilename extracted incorrectly: got '%s', expected '%s'", configFile, tc.expectedConfigFilename)
				}
			} else { // expected error
				if err == nil {
					t.Errorf("expected to return error, got org=%s repo=%s branch=%s instead", org, repo, branch)
				}
			}
		})
	}
}

func TestMergeJobConfig(t *testing.T) {
	tests := []struct {
		destination, source, expected *prowconfig.JobConfig
	}{
		{
			destination: &prowconfig.JobConfig{},
			source: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "source-job", Context: "ci/prow/source"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "source-job", Context: "ci/prow/source"},
				}},
			},
		}, {
			destination: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "another-job", Context: "ci/prow/another"},
				}},
			},
			source: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "source-job", Context: "ci/prow/source"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "source-job", Context: "ci/prow/source"},
					{Name: "another-job", Context: "ci/prow/another"},
				}},
			},
		}, {
			destination: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "same-job", Context: "ci/prow/same"},
				}},
			},
			source: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "same-job", Context: "ci/prow/different"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "same-job", Context: "ci/prow/different"},
				}},
			},
		}, {
			destination: &prowconfig.JobConfig{},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "source-job", Agent: "ci/prow/source"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "source-job", Agent: "ci/prow/source"},
				}},
			},
		}, {
			destination: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "another-job", Agent: "ci/prow/another"},
				}},
			},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "source-job", Agent: "ci/prow/source"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "source-job", Agent: "ci/prow/source"},
					{Name: "another-job", Agent: "ci/prow/another"},
				}},
			},
		}, {
			destination: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/same"},
				}},
			},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/different"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/different"},
				}},
			},
		}, {
			destination: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/same"},
				}},
			},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/same"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/same"},
				}},
			},
		},
	}
	for _, tc := range tests {
		mergeJobConfig(tc.destination, tc.source)

		if !equality.Semantic.DeepEqual(tc.destination, tc.expected) {
			t.Errorf("expected merged job config diff:\n%s", diff.ObjectDiff(tc.expected, tc.destination))
		}
	}
}

func prepareInputs(org, component, branch string, configYAML, prowConfigYAML []byte) (string, string, string, error) {
	dir, err := ioutil.TempDir("", "prowgen-test")
	if err != nil {
		return "", "", "", err
	}

	workDir := filepath.Join(dir, org, component)
	if err = os.MkdirAll(workDir, os.ModePerm); err != nil {
		return "", "", dir, err
	}

	fullConfigPath := filepath.Join(workDir, fmt.Sprintf("%s.yaml", branch))
	fullProwConfigPath := filepath.Join(workDir, "jobs.yaml")

	if err = ioutil.WriteFile(fullConfigPath, configYAML, 0664); err != nil {
		return "", "", dir, err
	}
	if err = ioutil.WriteFile(fullProwConfigPath, prowConfigYAML, 0664); err != nil {
		return "", "", dir, err
	}

	return fullConfigPath, fullProwConfigPath, dir, nil
}

func TestFromCIOperatorConfigToProwYaml(t *testing.T) {
	tests := []struct {
		id               string
		org              string
		component        string
		branch           string
		configYAML       []byte
		prowOldYAML      []byte
		prowExpectedYAML []byte
	}{
		{
			id:        "one test and images, no previous jobs. Expect test presubmit + pre/post submit images jobs",
			org:       "super",
			component: "duper",
			branch:    "branch",
			configYAML: []byte(`{
  "tag_specification": {
    "cluster": "https://api.ci.openshift.org", "namespace": "openshift", "name": "origin-v3.11", "tag": ""
  },
  "base_images": {
    "base": {
      "cluster": "https://api.ci.openshift.org", "namespace": "openshift", "name": "origin-v3.11", "tag": "base"
    }
  },
  "test_base_image": {
    "cluster": "https://api.ci.openshift.org", "namespace": "openshift", "name": "release", "tag": "golang-1.10"
  },
  "images": [{"from": "base", "to": "service-serving-cert-signer"}],

  "tests": [{"as": "unit", "from": "src", "commands": "make test-unit"}]}`),
			prowOldYAML: []byte(""),
			prowExpectedYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    branches:
    - branch
    decorate: true
    labels:
      artifacts: images
    name: branch-ci-super-duper-branch-images
    skip_cloning: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --target=[images]
        - --promote
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-super-duper
        image: ci-operator:latest
        name: ""
        resources: {}
      serviceAccountName: ci-operator
presubmits:
  super/duper:
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/unit
    decorate: true
    name: pull-ci-super-duper-branch-unit
    rerun_command: /test unit
    skip_cloning: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-super-duper
        image: ci-operator:latest
        name: ""
        resources: {}
      serviceAccountName: ci-operator
    trigger: ((?m)^/test( all| unit),?(\\s+|$))
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/images
    decorate: true
    name: pull-ci-super-duper-branch-images
    rerun_command: /test images
    skip_cloning: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-super-duper
        image: ci-operator:latest
        name: ""
        resources: {}
      serviceAccountName: ci-operator
    trigger: ((?m)^/test( all| images),?(\\s+|$))
`)}, {
			id:        "One test and images, one existing job. Expect one presubmit, pre/post submit images jobs. Existing job should not be changed.",
			org:       "super",
			component: "duper",
			branch:    "branch",
			configYAML: []byte(`{
  "tag_specification": {
    "cluster": "https://api.ci.openshift.org", "namespace": "openshift", "name": "origin-v3.11", "tag": ""
  },
  "base_images": {
    "base": {
      "cluster": "https://api.ci.openshift.org", "namespace": "openshift", "name": "origin-v3.11", "tag": "base"
    }
  },
  "test_base_image": {
    "cluster": "https://api.ci.openshift.org", "namespace": "openshift", "name": "release", "tag": "golang-1.10"
  },
  "images": [{"from": "base", "to": "service-serving-cert-signer"}],

  "tests": [{"as": "unit", "from": "src", "commands": "make test-unit"}]}`),
			prowOldYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    branches:
    - branch
    decorate: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    skip_cloning: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-super-duper
        image: ci-operator:latest
        name: ""
        resources: {}
      serviceAccountName: ci-operator
`),
			prowExpectedYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    branches:
    - branch
    decorate: true
    labels:
      artifacts: images
    name: branch-ci-super-duper-branch-images
    skip_cloning: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --target=[images]
        - --promote
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-super-duper
        image: ci-operator:latest
        name: ""
        resources: {}
      serviceAccountName: ci-operator
  - agent: kubernetes
    branches:
    - branch
    decorate: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    skip_cloning: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-super-duper
        image: ci-operator:latest
        name: ""
        resources: {}
      serviceAccountName: ci-operator
presubmits:
  super/duper:
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/unit
    decorate: true
    name: pull-ci-super-duper-branch-unit
    rerun_command: /test unit
    skip_cloning: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-super-duper
        image: ci-operator:latest
        name: ""
        resources: {}
      serviceAccountName: ci-operator
    trigger: ((?m)^/test( all| unit),?(\\s+|$))
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/images
    decorate: true
    name: pull-ci-super-duper-branch-images
    rerun_command: /test images
    skip_cloning: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-super-duper
        image: ci-operator:latest
        name: ""
        resources: {}
      serviceAccountName: ci-operator
    trigger: ((?m)^/test( all| images),?(\\s+|$))
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
tag_specification:
  cluster: https://api.ci.openshift.org
  name: origin-v3.11
  namespace: openshift
  tag: ''
test_base_image:
  cluster: https://api.ci.openshift.org
  name: release
  namespace: openshift
  tag: golang-1.10
tests:
- as: unit
  commands: make test-unit
  from: src
`),
			prowOldYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    decorate: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    skip_cloning: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-super-duper
        image: ci-operator:latest
        name: ""
        resources: {}
      serviceAccountName: ci-operator
`),
			prowExpectedYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    branches:
    - branch
    decorate: true
    labels:
      artifacts: images
    name: branch-ci-super-duper-branch-images
    skip_cloning: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --target=[images]
        - --promote
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-super-duper
        image: ci-operator:latest
        name: ""
        resources: {}
      serviceAccountName: ci-operator
  - agent: kubernetes
    decorate: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    skip_cloning: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-super-duper
        image: ci-operator:latest
        name: ""
        resources: {}
      serviceAccountName: ci-operator
presubmits:
  super/duper:
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/unit
    decorate: true
    name: pull-ci-super-duper-branch-unit
    rerun_command: /test unit
    skip_cloning: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-super-duper
        image: ci-operator:latest
        name: ""
        resources: {}
      serviceAccountName: ci-operator
    trigger: ((?m)^/test( all| unit),?(\\s+|$))
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/images
    decorate: true
    name: pull-ci-super-duper-branch-images
    rerun_command: /test images
    skip_cloning: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: branch.yaml
              name: ci-operator-super-duper
        image: ci-operator:latest
        name: ""
        resources: {}
      serviceAccountName: ci-operator
    trigger: ((?m)^/test( all| images),?(\\s+|$))
`),
		},
	}
	for _, tc := range tests {
		configPath, prowJobsPath, tempDir, err := prepareInputs(tc.org, tc.component, tc.branch, tc.configYAML, tc.prowOldYAML)
		if tempDir != "" {
			defer os.RemoveAll(tempDir)
		}
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
			continue
		}

		jobConfig, _, _, err := generateProwJobsFromConfigFile(configPath)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
			continue
		}
		err = mergeJobsIntoFile(prowJobsPath, jobConfig)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
			continue
		}

		data, err := ioutil.ReadFile(prowJobsPath)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
			continue
		}

		if bytes.Compare(data, tc.prowExpectedYAML) != 0 {
			t.Errorf("Generated Prow YAML differs from expected!\n%s", diff.StringDiff(string(tc.prowExpectedYAML), string(data)))
		}
	}
}
