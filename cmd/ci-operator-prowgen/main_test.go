package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

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
    name: origin-v3.11
    namespace: openshift
    tag: base
build_root:
  image_stream_tag:
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
  name: origin-v3.11
  namespace: openshift
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
    name: origin-v3.11
    namespace: openshift
    tag: base
build_root:
  image_stream_tag:
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
  name: origin-v3.11
  namespace: openshift
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
  name: origin-v3.11
  namespace: openshift
promotion:
  name: test
  namespace: ci
build_root:
  image_stream_tag:
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
			id:        "Custom test timeout",
			org:       "super",
			component: "duper",
			branch:    "branch",
			configYAML: []byte(`base_images:
  base:
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
  name: origin-v3.11
  namespace: openshift
promotion:
  name: test
  namespace: ci
build_root:
  image_stream_tag:
    namespace: openshift
    name: release
    tag: golang-1.10
tests:
- as: unit
  timeout: 8h
  steps:
    test:
    - as: test
      commands: make unit
      from: src
      resources:
        requests:
          cpu: 100m
    workflow: ipi-aws
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

			o := options{fromDir: fullConfigPath, toDir: baseProwConfigDir}
			if err := o.generateJobsToDir("", map[string]*config.Prowgen{}); err != nil {
				t.Fatalf("Unexpected error generating jobs from config: %v", err)
			}

			presubmitData, err := ioutil.ReadFile(presubmitPath)
			if err != nil {
				t.Fatalf("Unexpected error reading generated presubmits: %v", err)
			}
			testhelper.CompareWithFixture(t, presubmitData, testhelper.WithPrefix("presubmit-"))

			postsubmitData, err := ioutil.ReadFile(postsubmitPath)
			if err != nil {
				t.Fatalf("Unexpected error reading generated postsubmits: %v", err)
			}
			testhelper.CompareWithFixture(t, postsubmitData, testhelper.WithPrefix("postsubmit-"))
		})
	}
}
