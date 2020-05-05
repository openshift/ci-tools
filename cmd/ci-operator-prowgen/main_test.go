package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/pmezard/go-difflib/difflib"
	prowconfig "k8s.io/test-infra/prow/config"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

var update = flag.Bool("update", false, "update fixtures")

var unexportedFields = []cmp.Option{
	cmpopts.IgnoreUnexported(prowconfig.Presubmit{}),
	cmpopts.IgnoreUnexported(prowconfig.Periodic{}),
	cmpopts.IgnoreUnexported(prowconfig.Brancher{}),
	cmpopts.IgnoreUnexported(prowconfig.RegexpChangeMatcher{}),
}

func TestFromCIOperatorConfigToProwYaml(t *testing.T) {
	tests := []struct {
		id                    string
		org                   string
		component             string
		branch                string
		variant               string
		configYAML            []byte
		prowOldPresubmitYAML  []byte
		prowOldPostsubmitYAML []byte
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
promotion:
  namespace: ci
  name: other
tests:
- as: unit
  commands: make test-unit
  container:
    from: src`),
			prowOldPresubmitYAML:  []byte(""),
			prowOldPostsubmitYAML: []byte(""),
		}, {
			id:        "Using a variant config, one test and images, one existing job. Expect one presubmit, pre/post submit images jobs. Existing job should not be changed.",
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
promotion:
  name: test
  namespace: ci
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
promotion:
  name: test
  namespace: ci
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

			basename := strings.Join([]string{tc.org, tc.component, tc.branch}, "-")
			if tc.variant != "" {
				basename = fmt.Sprintf("%s__%s", basename, tc.variant)
			}

			fullConfigPath := filepath.Join(configDir, fmt.Sprintf("%s.yaml", basename))
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

			if err := config.OperateOnCIOperatorConfig(fullConfigPath, generateJobsToDir(baseProwConfigDir, jobconfig.Generated, nil)); err != nil {
				t.Fatalf("Unexpected error generating jobs from config: %v", err)
			}

			presubmitData, err := ioutil.ReadFile(presubmitPath)
			if err != nil {
				t.Fatalf("Unexpected error reading generated presubmits: %v", err)
			}
			compareWithFixture(t, "presubmit-", string(presubmitData))

			postsubmitData, err := ioutil.ReadFile(postsubmitPath)
			if err != nil {
				t.Fatalf("Unexpected error reading generated postsubmits: %v", err)
			}
			compareWithFixture(t, "postsubmit-", string(postsubmitData))
		})
	}
}

func TestPruneStaleJobs(t *testing.T) {
	testCases := []struct {
		name           string
		jobconfig      *prowconfig.JobConfig
		expectedPruned bool
	}{
		{
			name: "stale generated presubmit is pruned",
			jobconfig: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{jobconfig.ProwJobLabelGenerated: string(jobconfig.Generated)}}}},
				},
			},
			expectedPruned: true,
		},
		{
			name: "stale generated postsubmit is pruned",
			jobconfig: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{jobconfig.ProwJobLabelGenerated: string(jobconfig.Generated)}}}},
				},
			},
			expectedPruned: true,
		},
		{
			name: "not stale generated presubmit is kept",
			jobconfig: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{jobconfig.ProwJobLabelGenerated: string(jobconfig.New)}}}},
				},
			},
			expectedPruned: false,
		},
		{
			name: "not stale generated postsubmit is kept",
			jobconfig: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{jobconfig.ProwJobLabelGenerated: string(jobconfig.New)}}}},
				},
			},
			expectedPruned: false,
		},
		{
			name: "not generated presubmit is kept",
			jobconfig: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Name: "job"}}},
				},
			},
			expectedPruned: false,
		},
		{
			name: "not generated postsubmit is kept",
			jobconfig: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
					"repo": {{JobBase: prowconfig.JobBase{Name: "job"}}},
				},
			},
			expectedPruned: false,
		},
		{
			name: "periodics are kept",
			jobconfig: &prowconfig.JobConfig{
				Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "job"}}},
			},
			expectedPruned: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Expect either unchanged or empty JobConfig
			expected := tc.jobconfig
			if tc.expectedPruned {
				expected = &prowconfig.JobConfig{}
			}

			if pruned := prune(tc.jobconfig); !reflect.DeepEqual(pruned, expected) {
				t.Errorf("Pruned config differs:\n%s", cmp.Diff(expected, pruned, unexportedFields...))
			}
		})
	}
}

func compareWithFixture(t *testing.T, prefix, output string) {
	golden, err := filepath.Abs(filepath.Join("testdata", strings.ReplaceAll(prefix+t.Name(), "/", "_")+".yaml"))
	if err != nil {
		t.Fatalf("failed to get absolute path to testdata file: %v", err)
	}
	if *update {
		if err := ioutil.WriteFile(golden, []byte(output), 0644); err != nil {
			t.Fatalf("failed to write updated fixture: %v", err)
		}
	}
	expected, err := ioutil.ReadFile(golden)
	if err != nil {
		t.Fatalf("failed to read testdata file: %v", err)
	}

	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(expected)),
		B:        difflib.SplitLines(output),
		FromFile: "Fixture",
		ToFile:   "Current",
		Context:  3,
	}
	diffStr, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		t.Fatal(err)
	}

	if diffStr != "" {
		t.Errorf("got diff between expected and actual result: \n%s\n\nIf this is expected, re-run the test with `-update` flag to update the fixture.", diffStr)
	}
}
