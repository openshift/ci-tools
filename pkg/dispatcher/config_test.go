package dispatcher

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"syscall"
	"testing"

	"github.com/google/go-cmp/cmp"

	v1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/test-infra/prow/config"
	prowconfig "k8s.io/test-infra/prow/config"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

var (
	c = Config{
		Default: "api.ci",
		Groups: map[ClusterName]Group{
			"api.ci": {
				Paths: []string{
					".*-postsubmits.yaml$",
					".*-periodics.yaml$",
				},
				PathREs: []*regexp.Regexp{
					regexp.MustCompile(".*-postsubmits.yaml$"),
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

	configWithBuildFarm = Config{
		Default: "api.ci",
		BuildFarm: map[CloudProvider]JobGroups{
			CloudAWS: {
				ClusterBuild01: {},
			},
			CloudGCP: {
				ClusterBuild02: {},
			},
		},
		Groups: map[ClusterName]Group{
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
			"build01": {
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

	configWithBuildFarmWithJobs = Config{
		Default: "api.ci",
		BuildFarm: map[CloudProvider]JobGroups{
			CloudAWS: {
				ClusterBuild01: {
					Paths: []string{
						".*some-build-farm-presubmits.yaml$",
					},
					PathREs: []*regexp.Regexp{
						regexp.MustCompile(".*some-build-farm-presubmits.yaml$"),
					},
				},
			},
			CloudGCP: {
				ClusterBuild02: {},
			},
		},
		Groups: map[ClusterName]Group{
			"api.ci": {
				Paths: []string{
					".*-postsubmits.yaml$",
					".*-periodics.yaml$",
				},
				PathREs: []*regexp.Regexp{
					regexp.MustCompile(".*-postsubmits.yaml$"),
					regexp.MustCompile(".*-periodics.yaml$"),
				},
				Jobs: []string{
					"pull-ci-openshift-release-master-build01-dry",
					"pull-ci-openshift-release-master-core-dry",
					"pull-ci-openshift-release-master-services-dry",
					"periodic-acme-cert-issuer-for-build01",
				},
			},
			"build01": {
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
			expectedError: fmt.Errorf("failed to read the config file \"testdata/TestLoadConfig/file_not_exist.yaml\": %w", &os.PathError{Op: "open", Path: "testdata/TestLoadConfig/file_not_exist.yaml", Err: syscall.Errno(0x02)}),
		},
		{
			name:          "invalid yaml",
			expectedError: fmt.Errorf("failed to unmarshal the config \"invalid yaml format\\n\": %w", fmt.Errorf("error unmarshaling JSON: while decoding JSON: json: cannot unmarshal string into Go value of type dispatcher.Config")),
		},
		{
			name:          "invalid regex",
			expectedError: utilerrors.NewAggregate([]error{fmt.Errorf("[failed to compile regex config.Groups[default].Paths[0] from \"[\": error parsing regexp: missing closing ]: `[`, failed to compile regex config.Groups[default].Paths[1] from \"[0-9]++\": error parsing regexp: invalid nested repetition operator: `++`]")}),
		},
		{
			name:     "good config",
			expected: &c,
		},
		{
			name:     "good config with build farm",
			expected: &configWithBuildFarm,
		},
		{
			name:     "good config with build farm with jobs",
			expected: &configWithBuildFarmWithJobs,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := LoadConfig(filepath.Join("testdata", fmt.Sprintf("%s.yaml", t.Name())))
			if diff := cmp.Diff(tc.expected, actual, EquateRegexp); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

var (
	EquateRegexp = cmp.Comparer(func(x, y regexp.Regexp) bool {
		return x.String() == y.String()
	})
)

func TestGetClusterForJob(t *testing.T) {
	testCases := []struct {
		name        string
		config      *Config
		jobBase     prowconfig.JobBase
		path        string
		expected    ClusterName
		expectedErr error
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
			name:    "some jenkins job",
			config:  &c,
			jobBase: config.JobBase{Agent: "jenkins", Name: "test_branch_wildfly_images"},
			path:    "ci-operator/jobs/openshift-s2i/s2i-wildfly/openshift-s2i-s2i-wildfly-master-postsubmits.yaml",
		},
		{
			//https://github.com/openshift/release/pull/15918
			name: "error: PR 15918",
			config: &Config{
				Default: "api.ci",
				BuildFarm: map[CloudProvider]JobGroups{
					CloudAWS: {
						ClusterBuild01: {
							PathREs: []*regexp.Regexp{
								regexp.MustCompile(".*infra-periodics.yaml$"),
							},
						},
					},
					CloudGCP: {
						ClusterBuild02: {
							PathREs: []*regexp.Regexp{
								regexp.MustCompile(".*/openshift-openshift-azure-infra-periodics.yaml$"),
							},
						},
					},
				},
			},
			jobBase:     config.JobBase{Agent: "kubernetes", Name: "some-job"},
			path:        "ci-operator/jobs/openshift/openshift-azure/openshift-openshift-azure-infra-periodics.yaml",
			expectedErr: fmt.Errorf("path ci-operator/jobs/openshift/openshift-azure/openshift-openshift-azure-infra-periodics.yaml matches more than 1 regex: [.*/openshift-openshift-azure-infra-periodics.yaml$ .*infra-periodics.yaml$]"),
		},
		{
			//https://github.com/openshift/ci-tools/pull/1722
			name: "error: PR 1722",
			config: &Config{
				Default: "api.ci",
				BuildFarm: map[CloudProvider]JobGroups{
					CloudGCP: {
						ClusterBuild02: {
							PathREs: []*regexp.Regexp{
								regexp.MustCompile(".*kubevirt-kubevirt-ssp-operator-master-presubmits.yaml$"),
								regexp.MustCompile(".*kubevirt-ssp-operator-master-presubmits.yaml$"),
							},
						},
					},
				},
			},
			jobBase:     config.JobBase{Agent: "kubernetes", Name: "some-job"},
			path:        "ci-operator/jobs/kubevirt/kubevirt-ssp-operator/kubevirt-kubevirt-ssp-operator-master-presubmits.yaml",
			expectedErr: fmt.Errorf("path ci-operator/jobs/kubevirt/kubevirt-ssp-operator/kubevirt-kubevirt-ssp-operator-master-presubmits.yaml matches more than 1 regex: [.*kubevirt-kubevirt-ssp-operator-master-presubmits.yaml$ .*kubevirt-ssp-operator-master-presubmits.yaml$]"),
		},
		{
			//https://github.com/openshift/ci-tools/pull/1722
			name: "fix: PR 1722",
			config: &Config{
				Default: "api.ci",
				BuildFarm: map[CloudProvider]JobGroups{
					CloudGCP: {
						ClusterBuild02: {
							PathREs: []*regexp.Regexp{
								regexp.MustCompile(".*/kubevirt-kubevirt-ssp-operator-master-presubmits.yaml$"),
								regexp.MustCompile(".*/kubevirt-ssp-operator-master-presubmits.yaml$"),
							},
						},
					},
				},
			},
			jobBase:  config.JobBase{Agent: "kubernetes", Name: "some-job"},
			path:     "ci-operator/jobs/kubevirt/kubevirt-ssp-operator/kubevirt-kubevirt-ssp-operator-master-presubmits.yaml",
			expected: ClusterBuild02,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualErr := tc.config.GetClusterForJob(tc.jobBase, tc.path)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

func TestDetermineClusterForJob(t *testing.T) {
	testCases := []struct {
		name                   string
		config                 *Config
		jobBase                prowconfig.JobBase
		path                   string
		expected               ClusterName
		expectedCanBeRelocated bool
		expectedErr            error
	}{
		{
			name:     "some job",
			config:   &configWithBuildFarmWithJobs,
			jobBase:  config.JobBase{Agent: "kubernetes", Name: "some-job"},
			path:     "org/repo/some-postsubmits.yaml",
			expected: "api.ci",
		},
		{
			name:     "job must on build01",
			config:   &configWithBuildFarmWithJobs,
			jobBase:  config.JobBase{Agent: "kubernetes", Name: "periodic-build01-upgrade"},
			expected: "build01",
		},
		{
			name:     "some periodic job in release repo",
			config:   &configWithBuildFarmWithJobs,
			jobBase:  config.JobBase{Agent: "kubernetes", Name: "promote-release-openshift-machine-os-content-e2e-aws-4.1"},
			path:     "ci-operator/jobs/openshift/release/openshift-release-release-4.1-periodics.yaml",
			expected: "api.ci",
		},
		{
			name:     "some jenkins job",
			config:   &configWithBuildFarmWithJobs,
			jobBase:  config.JobBase{Agent: "jenkins", Name: "test_branch_wildfly_images"},
			path:     "ci-operator/jobs/openshift-s2i/s2i-wildfly/openshift-s2i-s2i-wildfly-master-postsubmits.yaml",
			expected: "",
		},
		{
			name:     "some job without agent",
			config:   &configWithBuildFarmWithJobs,
			jobBase:  config.JobBase{Name: "no-agent-job"},
			path:     "ci-operator/jobs/openshift-s2i/s2i-wildfly/openshift-s2i-s2i-wildfly-master-postsubmits.yaml",
			expected: "api.ci",
		},
		{
			name:                   "some job in build farm",
			config:                 &configWithBuildFarmWithJobs,
			jobBase:                config.JobBase{Agent: "kubernetes", Name: "some-build-farm-job"},
			path:                   "org/repo/some-build-farm-presubmits.yaml",
			expected:               "build01",
			expectedCanBeRelocated: true,
		},
		{
			name:     "Vsphere job",
			config:   &configWithBuildFarmWithJobs,
			jobBase:  config.JobBase{Agent: "kubernetes", Name: "yalayala-vsphere"},
			expected: "vsphere",
		},
		{
			name:   "applyconfig job for vsphere",
			config: &configWithBuildFarmWithJobs,
			jobBase: config.JobBase{Agent: "kubernetes", Name: "pull-ci-openshift-release-master-vsphere-dry", Spec: &v1.PodSpec{
				Containers: []v1.Container{
					{Image: "registry.svc.ci.openshift.org/ci/applyconfig:latest"},
				},
			},
			},
			expected:               "api.ci",
			expectedCanBeRelocated: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, canBeRelocated, actualErr := tc.config.DetermineClusterForJob(tc.jobBase, tc.path)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedCanBeRelocated, canBeRelocated); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

func TestIsInBuildFarm(t *testing.T) {
	testCases := []struct {
		name        string
		config      *Config
		clusterName ClusterName
		expected    CloudProvider
	}{
		{
			name:        "build01",
			config:      &configWithBuildFarm,
			clusterName: ClusterBuild01,
			expected:    "aws",
		},
		{
			name:        "app.ci",
			config:      &configWithBuildFarm,
			clusterName: ClusterAPPCI,
			expected:    "",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.config.IsInBuildFarm(tc.clusterName)
			if !reflect.DeepEqual(tc.expected, actual) {
				t.Errorf("%s: actual differs from expected:\n%s", t.Name(), cmp.Diff(tc.expected, actual))
			}
		})
	}
}

func TestMatchingPathRegEx(t *testing.T) {
	testCases := []struct {
		name     string
		config   *Config
		path     string
		expected bool
	}{
		{
			name:     "matching: true",
			config:   &c,
			path:     "./ci-operator/jobs/openshift/ci-tools/openshift-ci-tools-master-postsubmits.yaml",
			expected: true,
		},
		{
			name:   "matching: false",
			config: &c,
			path:   "./ci-operator/jobs/openshift/ci-tools/openshift-ci-tools-master-presubmits.yaml",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.config.MatchingPathRegEx(tc.path)
			if !reflect.DeepEqual(tc.expected, actual) {
				t.Errorf("%s: actual differs from expected:\n%s", t.Name(), cmp.Diff(tc.expected, actual))
			}
		})
	}
}

func TestIsSSHBastionJob(t *testing.T) {
	testCases := []struct {
		name     string
		base     prowconfig.JobBase
		expected bool
	}{
		{
			name: "matching label: false",
			base: prowconfig.JobBase{
				Name: "some-job",
				Labels: map[string]string{
					"dptp.openshift.io/non-ssh-bastion": "true",
				},
			},
			expected: false,
		},
		{
			name: "matching label: true",
			base: prowconfig.JobBase{
				Name: "some-job",
				Labels: map[string]string{
					"dptp.openshift.io/ssh-bastion": "true",
				},
			},
			expected: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := isSSHBastionJob(tc.base)
			if !reflect.DeepEqual(tc.expected, actual) {
				t.Errorf("%s: actual differs from expected:\n%s", t.Name(), cmp.Diff(tc.expected, actual))
			}
		})
	}
}

func TestValidate(t *testing.T) {
	testCases := []struct {
		name     string
		config   *Config
		expected error
	}{
		{
			name: "basic case",
			config: &Config{
				Default: "api.ci",
				BuildFarm: map[CloudProvider]JobGroups{
					CloudAWS: {
						ClusterBuild01: {
							Jobs: []string{"a", "b"},
						},
					},
					CloudGCP: {
						ClusterBuild02: {
							Jobs: []string{"c", "b"},
						},
					},
				},
				Groups: map[ClusterName]Group{ClusterAPICI: {
					Jobs: []string{"c", "d"},
				}},
			},
			expected: fmt.Errorf("there are job names occurring more than once: [b c]"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.config.Validate()
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}
