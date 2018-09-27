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
						Limits:   kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(500, resource.DecimalSI)},
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
						Limits:   kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(500, resource.DecimalSI)},
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

func TestGeneratePresubmitForTest(t *testing.T) {
	tests := []struct {
		name     string
		repoInfo *configFilePathElements
		expected *prowconfig.Presubmit
	}{{
		name:     "testname",
		repoInfo: &configFilePathElements{org: "org", repo: "repo", branch: "branch"},

		expected: &prowconfig.Presubmit{
			Agent:        "kubernetes",
			AlwaysRun:    true,
			Brancher:     prowconfig.Brancher{Branches: []string{"branch"}},
			Context:      "ci/prow/testname",
			Name:         "pull-ci-org-repo-branch-testname",
			RerunCommand: "/test testname",
			Trigger:      `((?m)^/test( all| testname),?(\s+|$))`,
			UtilityConfig: prowconfig.UtilityConfig{
				DecorationConfig: &prowkube.DecorationConfig{SkipCloning: true},
				Decorate:         true,
			},
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
	tests := []struct {
		name     string
		repoInfo *configFilePathElements
		labels   map[string]string

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
			name: "Name",
			repoInfo: &configFilePathElements{
				org:            "Organization",
				repo:           "Repository",
				branch:         "Branch",
				configFilename: "config.yaml",
			},
			labels: map[string]string{"artifacts": "images"},

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
		postsubmit := generatePostsubmitForTest(tc.name, tc.repoInfo, tc.labels, nil) // podSpec tested in TestGeneratePodSpec
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
				Tests: []ciop.TestStepConfiguration{{As: "derTest"}, {As: "leTest"}},
			},
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
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
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
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
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
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
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
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
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
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
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
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
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
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
			repoInfo: &configFilePathElements{
				org:            "organization",
				repo:           "repository",
				branch:         "branch",
				configFilename: "konfig.yaml",
			},
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
			configYAML: []byte(`{
  "tag_specification": {
    "cluster": "https://api.ci.openshift.org", "namespace": "openshift", "name": "origin-v3.11", "tag": ""
  },
  "base_images": {
    "base": {
      "cluster": "https://api.ci.openshift.org", "namespace": "openshift", "name": "origin-v3.11", "tag": "base"
    }
  },
  "build_root": {
    "image_stream_tag":{
      "cluster": "https://api.ci.openshift.org",
      "namespace": "openshift",
      "name": "release",
      "tag": "golang-1.10"
    }
  },
  "images": [{"from": "base", "to": "service-serving-cert-signer"}],

  "tests": [{"as": "unit", "from": "src", "commands": "make test-unit"}]}`),
			prowOldPresubmitYAML:  []byte(""),
			prowOldPostsubmitYAML: []byte(""),
			prowExpectedPostsubmitYAML: []byte(`postsubmits:
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
          limits:
            cpu: 500m
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
    name: pull-ci-super-duper-branch-images
    rerun_command: /test images
    skip_cloning: true
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
          limits:
            cpu: 500m
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
    trigger: ((?m)^/test( all| images),?(\s+|$))
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
          limits:
            cpu: 500m
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
    trigger: ((?m)^/test( all| unit),?(\s+|$))
`),
		}, {
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
  "build_root": {
    "image_stream_tag":{
      "cluster": "https://api.ci.openshift.org",
      "namespace": "openshift",
      "name": "release",
      "tag": "golang-1.10"
    }
  },
  "images": [{"from": "base", "to": "service-serving-cert-signer"}],

  "tests": [{"as": "unit", "from": "src", "commands": "make test-unit"}]}`),
			prowOldPresubmitYAML: []byte(""),
			prowOldPostsubmitYAML: []byte(`postsubmits:
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
          limits:
            cpu: 500m
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
    name: pull-ci-super-duper-branch-images
    rerun_command: /test images
    skip_cloning: true
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
          limits:
            cpu: 500m
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
    trigger: ((?m)^/test( all| images),?(\s+|$))
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
          limits:
            cpu: 500m
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
    trigger: ((?m)^/test( all| unit),?(\s+|$))
`),
			prowExpectedPostsubmitYAML: []byte(`postsubmits:
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
          limits:
            cpu: 500m
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
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
          limits:
            cpu: 500m
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
  from: src
`),
			prowOldPresubmitYAML: []byte(""),
			prowOldPostsubmitYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    decorate: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    skip_cloning: true
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
          limits:
            cpu: 500m
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
    name: pull-ci-super-duper-branch-images
    rerun_command: /test images
    skip_cloning: true
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
          limits:
            cpu: 500m
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
    trigger: ((?m)^/test( all| images),?(\s+|$))
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
          limits:
            cpu: 500m
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
    trigger: ((?m)^/test( all| unit),?(\s+|$))
`),
			prowExpectedPostsubmitYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    decorate: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    skip_cloning: true
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
          limits:
            cpu: 500m
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
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
          limits:
            cpu: 500m
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

			fullConfigPath := filepath.Join(configDir, fmt.Sprintf("%s.yaml", tc.branch))
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
