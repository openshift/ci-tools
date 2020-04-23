package main

import (
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/test-infra/prow/config"
	prowconfig "k8s.io/test-infra/prow/config"
)

func TestDefaultJobConfig(t *testing.T) {
	jc := &config.JobConfig{
		PresubmitsStatic: map[string][]config.Presubmit{
			"a": {{}, {}, {JobBase: config.JobBase{Agent: "kubernetes", Cluster: "default"}}},
			"b": {{}, {}},
		},
		PostsubmitsStatic: map[string][]config.Postsubmit{
			"a": {{}, {}, {JobBase: config.JobBase{Agent: "kubernetes", Cluster: "default"}}},
			"b": {{}, {}},
		},
		Periodics: []config.Periodic{{}, {}, {JobBase: config.JobBase{Agent: "kubernetes", Cluster: "default"}}},
	}

	config := &Config{Default: "api.ci"}
	defaultJobConfig(jc, "", config)

	for k := range jc.PresubmitsStatic {
		for _, j := range jc.PresubmitsStatic[k] {
			if j.Agent == "kubernetes" && j.Cluster != "api.ci" {
				t.Errorf("expected cluster to be 'api.ci', was '%s'", j.Cluster)
			}
		}
	}
	for k := range jc.PostsubmitsStatic {
		for _, j := range jc.PostsubmitsStatic[k] {
			if j.Agent == "kubernetes" && j.Cluster != "api.ci" {
				t.Errorf("expected cluster to be 'api.ci', was '%s'", j.Cluster)
			}
		}
	}
	for _, j := range jc.Periodics {
		if j.Agent == "kubernetes" && j.Cluster != "api.ci" {
			t.Errorf("expected cluster to be 'api.ci', was '%s'", j.Cluster)
		}
	}
}

var (
	c = Config{
		Default:       "api.ci",
		NonKubernetes: "app.ci",
		Groups: map[string]Group{
			"api.ci": {
				Paths: []string{
					".*-postsubmits.yaml$",
					".*openshift/release/.*-periodics.yaml$",
					".*-periodics.yaml$",
				},
				PathREs: []*regexp.Regexp{
					regexp.MustCompile(".*-postsubmits.yaml$"),
					regexp.MustCompile(".*openshift/release/.*-periodics.yaml$"),
					regexp.MustCompile(".*-periodics.yaml$"),
				},
				Jobs: []string{
					"pull-ci-openshift-release-master-build01-dry",
					"pull-ci-openshift-release-master-core-dry",
					"pull-ci-openshift-release-master-services-dry",
					"periodic-acme-cert-issuer-for-build01",
				},
			},
			"ci/api-build01-ci-devcluster-openshift-com:6443": {
				Jobs: []string{
					"periodic-build01-upgrade",
					"periodic-ci-image-import-to-build01",
					"pull-ci-openshift-config-master-format",
					"pull-ci-openshift-psap-special-resource-operator-release-4.6-images",
					"pull-ci-openshift-psap-special-resource-operator-release-4.6-unit",
					"pull-ci-openshift-psap-special-resource-operator-release-4.6-verify",
				},
				Paths: []string{".*openshift-priv/.*-presubmits.yaml$"},
				PathREs: []*regexp.Regexp{
					regexp.MustCompile(".*openshift-priv/.*-presubmits.yaml$"),
				},
			},
		},
	}
)

func TestLoadConfig(t *testing.T) {
	testCases := []struct {
		name string

		configPath    string
		expected      *Config
		expectedError error
	}{
		{
			name:          "file not exist",
			expectedError: fmt.Errorf("failed to read the config file \"testdata/TestLoadConfig/file_not_exist.yaml\": open testdata/TestLoadConfig/file_not_exist.yaml: no such file or directory"),
		},
		{
			name:          "invalid yaml",
			expectedError: fmt.Errorf("failed to unmarshal the config \"invalid yaml format\\n\": error unmarshaling JSON: while decoding JSON: json: cannot unmarshal string into Go value of type main.Config"),
		},
		{
			name:          "invalid regex",
			expectedError: fmt.Errorf("[failed to compile regex config.Groups[default].Paths[0] from \"[\": error parsing regexp: missing closing ]: `[`, failed to compile regex config.Groups[default].Paths[1] from \"[0-9]++\": error parsing regexp: invalid nested repetition operator: `++`]"),
		},
		{
			name:     "good config",
			expected: &c,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := loadConfig(filepath.Join("testdata", fmt.Sprintf("%s.yaml", t.Name())))
			if !reflect.DeepEqual(tc.expected, actual) {
				t.Errorf("%s: actual differs from expected:\n%s", t.Name(), cmp.Diff(tc.expected, actual))
			}
			equalError(t, tc.expectedError, err)
		})
	}
}

func TestGetClusterForJob(t *testing.T) {
	testCases := []struct {
		name string

		config   *Config
		jobBase  prowconfig.JobBase
		path     string
		expected string
	}{
		{
			name:     "some job",
			config:   &c,
			jobBase:  config.JobBase{Agent: "kubernetes", Name: "some-job"},
			path:     "org/repo/some-postsubmits.yaml",
			expected: "api.ci",
		},
		{
			name:     "job must on build01",
			config:   &c,
			jobBase:  config.JobBase{Agent: "kubernetes", Name: "periodic-build01-upgrade"},
			expected: "ci/api-build01-ci-devcluster-openshift-com:6443",
		},
		{
			name:     "some periodic job in release repo",
			config:   &c,
			jobBase:  config.JobBase{Agent: "kubernetes", Name: "promote-release-openshift-machine-os-content-e2e-aws-4.1"},
			path:     "ci-operator/jobs/openshift/release/openshift-release-release-4.1-periodics.yaml",
			expected: "api.ci",
		},
		{
			name:     "some jenkins job",
			config:   &c,
			jobBase:  config.JobBase{Agent: "jenkins", Name: "test_branch_wildfly_images"},
			path:     "ci-operator/jobs/openshift-s2i/s2i-wildfly/openshift-s2i-s2i-wildfly-master-postsubmits.yaml",
			expected: "app.ci",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.config.getClusterForJob(tc.jobBase, tc.path)
			if !reflect.DeepEqual(tc.expected, actual) {
				t.Errorf("%s: actual differs from expected:\n%s", t.Name(), cmp.Diff(tc.expected, actual))
			}
		})
	}
}

func equalError(t *testing.T, expected, actual error) {
	if (expected == nil) != (actual == nil) {
		t.Errorf("%s: expecting error \"%v\", got \"%v\"", t.Name(), expected, actual)
	}
	if expected != nil && actual != nil && expected.Error() != actual.Error() {
		t.Errorf("%s: expecting error msg %q, got %q", t.Name(), expected.Error(), actual.Error())
	}
}
