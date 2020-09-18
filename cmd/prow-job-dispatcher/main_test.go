package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "k8s.io/test-infra/prow/config"
)

func TestValidate(t *testing.T) {
	testCases := []struct {
		name     string
		given    *options
		expected error
	}{
		{
			name:     "empty options",
			given:    &options{},
			expected: fmt.Errorf("mandatory argument --prow-jobs-dir wasn't set"),
		},
		{
			name: "set only prow-jobs-dir",
			given: &options{
				prowJobConfigDir: "--prow-jobs-dir",
			},
			expected: fmt.Errorf("mandatory argument --config-path wasn't set"),
		},
		{
			name: "OK if no username and no possword",
			given: &options{
				prowJobConfigDir:     "prow-jobs-dir",
				configPath:           "some-path",
				prometheusDaysBefore: 1,
			},
		},
		{
			name: "prometheus username is set while password not",
			given: &options{
				prowJobConfigDir:   "prow-jobs-dir",
				configPath:         "some-path",
				prometheusUsername: "user",
			},
			expected: fmt.Errorf("--prometheus-username and --prometheus-password-path must be specified together"),
		},
		{
			name: "prometheus password path is set while username not",
			given: &options{
				prowJobConfigDir:       "prow-jobs-dir",
				configPath:             "some-path",
				prometheusPasswordPath: "some-path",
			},
			expected: fmt.Errorf("--prometheus-username and --prometheus-password-path must be specified together"),
		},
		{
			name: "prometheus days before cannot be 16",
			given: &options{
				prowJobConfigDir:     "prow-jobs-dir",
				configPath:           "some-path",
				prometheusDaysBefore: 16,
			},
			expected: fmt.Errorf("--prometheus-days-before must be between 1 and 15"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.given.validate()
			equalError(t, tc.expected, actual)
		})
	}
}

func TestComplete(t *testing.T) {
	dir, err := ioutil.TempDir("", "test")
	if err != nil {
		t.Error("Failed to create the temp dir")
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("Failed to remove the temp dir: %s", dir)
		}
	}()
	passwordPath := filepath.Join(dir, "secret.txt")
	if err := ioutil.WriteFile(passwordPath, []byte("some-pass"), 0644); err != nil {
		t.Errorf("Failed to password to the file: %s", passwordPath)
	}
	emptyPasswordPath := filepath.Join(dir, "empty-secret.txt")
	if err := ioutil.WriteFile(emptyPasswordPath, []byte{}, 0644); err != nil {
		t.Errorf("Failed to password to the file: %s", emptyPasswordPath)
	}

	testCases := []struct {
		name            string
		secrets         sets.String
		given           *options
		expected        error
		expectedSecrets sets.String
	}{
		{
			name: "password path is set",
			given: &options{
				prometheusPasswordPath: passwordPath,
			},
			expectedSecrets: sets.NewString("some-pass"),
		},
		{
			name: "password path is set but file does not exist",
			given: &options{
				prometheusPasswordPath: "not-exist",
			},
			expected:        fmt.Errorf("open not-exist: no such file or directory"),
			expectedSecrets: sets.NewString(),
		},
		{
			name: "empty password",
			given: &options{
				prometheusPasswordPath: emptyPasswordPath,
			},
			expected:        fmt.Errorf("no content in file: %s", emptyPasswordPath),
			expectedSecrets: sets.NewString(),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			secrets := sets.NewString()
			actual := tc.given.complete(&secrets)
			equalError(t, tc.expected, actual)
			if !reflect.DeepEqual(tc.expectedSecrets, secrets) {
				t.Errorf("%s: actual differs from expected:\n%s", t.Name(), cmp.Diff(tc.expectedSecrets, secrets))
			}
		})
	}
}

var (
	c = dispatcher.Config{
		Default:       "api.ci",
		NonKubernetes: "app.ci",
		BuildFarm: map[dispatcher.CloudProvider]dispatcher.JobGroups{
			dispatcher.CloudAWS: {
				dispatcher.ClusterBuild01: {},
			},
			dispatcher.CloudGCP: {
				dispatcher.ClusterBuild02: {},
			},
		},
		Groups: map[dispatcher.ClusterName]dispatcher.Group{
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
)

func TestDispatchJobs(t *testing.T) {
	testCases := []struct {
		name              string
		prowJobConfigDir  string
		maxConcurrency    int
		config            *dispatcher.Config
		jobVolumes        map[string]float64
		expected          error
		expectedBuildFarm map[dispatcher.CloudProvider]dispatcher.JobGroups
	}{
		{
			name:     "nil config",
			expected: fmt.Errorf("config is nil"),
		},
		{
			name:             "basic case",
			config:           &c,
			prowJobConfigDir: filepath.Join("testdata", t.Name()),
			maxConcurrency:   1,
			jobVolumes: map[string]float64{
				"pull-ci-openshift-ci-tools-master-breaking-changes":  23,
				"pull-ci-openshift-ci-tools-master-e2e":               12,
				"pull-ci-openshift-cluster-etcd-operator-master-unit": 6,
			},
			expectedBuildFarm: map[dispatcher.CloudProvider]dispatcher.JobGroups{
				"aws": {"build01": {Paths: []string{".*ci-tools-presubmits.yaml$"}}},
				"gcp": {"build02": {Paths: []string{".*cluster-api-provider-gcp-presubmits.yaml$", ".*cluster-etcd-operator-master-presubmits.yaml$", ".*wildfly-operator-presubmits.yaml$"}}},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := dispatchJobs(context.TODO(), tc.prowJobConfigDir, tc.maxConcurrency, tc.config, tc.jobVolumes)
			equalError(t, tc.expected, actual)
			if tc.config != nil && !reflect.DeepEqual(tc.expectedBuildFarm, tc.config.BuildFarm) {
				t.Errorf("%s: actual differs from expected:\n%s", t.Name(), cmp.Diff(tc.expectedBuildFarm, tc.config.BuildFarm))
			}
		})
	}
}

func TestDispatchJobConfig(t *testing.T) {
	testCases := []struct {
		name       string
		cv         *clusterVolume
		jc         *prowconfig.JobConfig
		path       string
		config     *dispatcher.Config
		jobVolumes map[string]float64
		expected   string
	}{
		{
			name: "basic case: non e2e job chooses build01",
			cv: &clusterVolume{
				clusterVolumeMap: map[string]map[string]float64{"aws": {"build01": 0}, "gcp": {"build02": 0}},
				cloudProviders:   sets.NewString("aws", "gcp"),
			},
			config: &c,
			jc: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Name: "job",
						Spec: &corev1.PodSpec{
							Containers: []corev1.Container{
								{Env: []corev1.EnvVar{{Name: "CLUSTER_TYPE", Value: "openstack"}}},
							},
						}}}},
				},
			},
			path: "repo-presubmits.yaml",
			jobVolumes: map[string]float64{
				"pull-ci-openshift-ci-tools-master-breaking-changes":  23,
				"pull-ci-openshift-ci-tools-master-e2e":               12,
				"pull-ci-openshift-cluster-etcd-operator-master-unit": 6,
			},
			expected: "build01",
		},
		{
			name: "basic case: aws e2e job chooses build01",
			cv: &clusterVolume{
				clusterVolumeMap: map[string]map[string]float64{"aws": {"build01": 1}, "gcp": {"build02": 0}},
				cloudProviders:   sets.NewString("aws", "gcp"),
			},
			config: &c,
			jc: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Name: "job",
						Spec: &corev1.PodSpec{
							Containers: []corev1.Container{
								{Env: []corev1.EnvVar{{Name: "CLUSTER_TYPE", Value: "aws"}}},
							},
						}}}},
				},
			},
			path: "repo-presubmits.yaml",
			jobVolumes: map[string]float64{
				"pull-ci-openshift-ci-tools-master-breaking-changes":  23,
				"pull-ci-openshift-ci-tools-master-e2e":               12,
				"pull-ci-openshift-cluster-etcd-operator-master-unit": 6,
			},
			expected: "build01",
		},
		{
			name: "basic case: aws and gcp e2e job chooses build02",
			cv: &clusterVolume{
				clusterVolumeMap: map[string]map[string]float64{"aws": {"build01": 1}, "gcp": {"build02": 0}},
				cloudProviders:   sets.NewString("aws", "gcp"),
			},
			config: &c,
			jc: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {
						{JobBase: prowconfig.JobBase{Name: "job",
							Spec: &corev1.PodSpec{
								Containers: []corev1.Container{
									{Env: []corev1.EnvVar{{Name: "CLUSTER_TYPE", Value: "aws"}}},
								},
							}}},
						{JobBase: prowconfig.JobBase{Name: "job1",
							Spec: &corev1.PodSpec{
								Containers: []corev1.Container{
									{Env: []corev1.EnvVar{{Name: "CLUSTER_TYPE", Value: "gcp"}}},
								},
							}}},
					},
				},
			},
			path: "repo-presubmits.yaml",
			jobVolumes: map[string]float64{
				"pull-ci-openshift-ci-tools-master-breaking-changes":  23,
				"pull-ci-openshift-ci-tools-master-e2e":               12,
				"pull-ci-openshift-cluster-etcd-operator-master-unit": 6,
			},
			expected: "build02",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.cv.dispatchJobConfig(tc.jc, tc.path, tc.config, tc.jobVolumes)
			if !reflect.DeepEqual(tc.expected, actual) {
				t.Errorf("%s: actual differs from expected:\n%s", t.Name(), cmp.Diff(tc.expected, actual))
			}
		})
	}
}

func TestGetCloudProvidersForE2ETests(t *testing.T) {
	testCases := []struct {
		name     string
		jc       *prowconfig.JobConfig
		expected sets.String
	}{
		{
			name: "openstack",
			jc: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Name: "job",
						Spec: &corev1.PodSpec{
							Containers: []corev1.Container{
								{Env: []corev1.EnvVar{{Name: "CLUSTER_TYPE", Value: "openstack"}}},
							},
						}}}},
				},
			},
			expected: sets.NewString(),
		},
		{
			name: "aws",
			jc: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Name: "job",
						Spec: &corev1.PodSpec{
							Containers: []corev1.Container{
								{Env: []corev1.EnvVar{{Name: "CLUSTER_TYPE", Value: "aws"}}},
							},
						}}}},
				},
			},
			expected: sets.NewString("aws"),
		},
		{
			name: "several cloud providers",
			jc: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Name: "job",
						Spec: &corev1.PodSpec{
							Containers: []corev1.Container{
								{Env: []corev1.EnvVar{{Name: "CLUSTER_TYPE", Value: "aws"}}},
							},
						}}}},
					"repo1": {{JobBase: prowconfig.JobBase{Name: "job1",
						Spec: &corev1.PodSpec{
							Containers: []corev1.Container{
								{Env: []corev1.EnvVar{{Name: "CLUSTER_TYPE", Value: "aws"}}},
							},
						}}}},
					"repo2": {{JobBase: prowconfig.JobBase{Name: "job2",
						Spec: &corev1.PodSpec{
							Containers: []corev1.Container{
								{Env: []corev1.EnvVar{{Name: "CLUSTER_TYPE", Value: "gcp"}}},
							},
						}}}},
				},
			},
			expected: sets.NewString("aws", "gcp"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := getCloudProvidersForE2ETests(tc.jc)
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
