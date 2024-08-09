package main

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/testhelper"
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
				prowJobConfigDir:     "prow-jobs-dir",
				configPath:           "some-path",
				prometheusDaysBefore: 1,
				PrometheusOptions: dispatcher.PrometheusOptions{
					PrometheusUsername: "user",
				},
			},
			expected: fmt.Errorf("--prometheus-username and --prometheus-password-path must be specified together"),
		},
		{
			name: "prometheus password path is set while username not",
			given: &options{
				prowJobConfigDir:     "prow-jobs-dir",
				configPath:           "some-path",
				prometheusDaysBefore: 1,
				PrometheusOptions: dispatcher.PrometheusOptions{
					PrometheusPasswordPath: "some-path",
				},
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

var (
	c = dispatcher.Config{
		Default: "api.ci",
		BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
			api.CloudAWS: {
				api.ClusterBuild01: {},
			},
			api.CloudGCP: {
				api.ClusterBuild02: {},
			},
		},
		Groups: map[api.Cluster]dispatcher.Group{
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
		expectedBuildFarm map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig
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
			expectedBuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
				"aws": {"build01": {FilenamesRaw: []string{"ci-tools-presubmits.yaml"}}},
				"gcp": {"build02": {FilenamesRaw: []string{"cluster-api-provider-gcp-presubmits.yaml", "cluster-etcd-operator-master-presubmits.yaml", "wildfly-operator-presubmits.yaml"}}},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, actual := dispatchJobs(context.TODO(), tc.prowJobConfigDir, tc.maxConcurrency, tc.config, tc.jobVolumes, sets.New[string]())
			equalError(t, tc.expected, actual)
			if tc.config != nil && !reflect.DeepEqual(tc.expectedBuildFarm, tc.config.BuildFarm) {
				t.Errorf("%s: actual differs from expected:\n%s", t.Name(), cmp.Diff(tc.expectedBuildFarm, tc.config.BuildFarm))
			}
		})
	}
}

func TestDispatchJobConfig(t *testing.T) {
	testCases := []struct {
		name        string
		cv          *clusterVolume
		jc          *prowconfig.JobConfig
		path        string
		config      *dispatcher.Config
		jobVolumes  map[string]float64
		expected    string
		expectedErr error
	}{
		{
			name: "basic case: non e2e job chooses build01",
			cv: &clusterVolume{
				clusterVolumeMap: map[string]map[string]float64{"aws": {"build01": 0}, "gcp": {"build02": 0}},
				cloudProviders:   sets.New[string]("aws", "gcp"),
				pjs:              map[string]string{},
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
				cloudProviders:   sets.New[string]("aws", "gcp"),
				pjs:              map[string]string{},
				blocked:          sets.New[string](),
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
				cloudProviders:   sets.New[string]("aws", "gcp"),
				pjs:              map[string]string{},
				blocked:          sets.New[string](),
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
			actual, actualErr := tc.cv.dispatchJobConfig(tc.jc, tc.path, tc.config, tc.jobVolumes)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

func TestGetCloudProvidersForE2ETests(t *testing.T) {
	testCases := []struct {
		name     string
		jc       *prowconfig.JobConfig
		expected sets.Set[string]
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
			expected: sets.New[string](),
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
			expected: sets.New[string]("aws"),
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
			expected: sets.New[string]("aws", "gcp"),
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

func TestAddEnabledClusters(t *testing.T) {
	getProvider := func(cluster string) (api.Cloud, error) {
		if cluster == "build02" || cluster == "build04" {
			return "gcp", nil
		}
		return "aws", nil
	}
	tests := []struct {
		name    string
		config  *dispatcher.Config
		enabled sets.Set[string]
	}{
		{
			name:    "Add gcp cluster build04",
			config:  &dispatcher.Config{},
			enabled: sets.New[string]("build04"),
		},
		{
			name:    "Add multiple clusters, different providers",
			config:  &dispatcher.Config{},
			enabled: sets.New[string]("build03", "build04", "build05"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addEnabledClusters(tc.config, tc.enabled, getProvider)
			for cluster := range tc.enabled {
				provider, _ := getProvider(cluster)
				if _, exists := tc.config.BuildFarm[provider][api.Cluster(cluster)]; !exists {
					t.Errorf("%s: expected %s cluster to be added under %s provider in BuildFarm", t.Name(), cluster, provider)
				}
				c, exists := tc.config.BuildFarmCloud[provider]
				if !exists {
					t.Errorf("%s: cloud provider %s not found in BuildFarmCloud", t.Name(), provider)
				}
				clusters := sets.New[string](c...)
				if !clusters.Has(cluster) {
					t.Errorf("%s: expected %s cluster to be added under %s provider in BuildFarmCloud", t.Name(), cluster, provider)
				}
			}
		})
	}
}

func TestRemoveDisabledClusters(t *testing.T) {
	cfgWithCloud := &dispatcher.Config{
		BuildFarmCloud: map[api.Cloud][]string{
			"aws": {"build01"},
			"gcp": {"build02"},
		},
		BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
			api.CloudAWS: {
				api.ClusterBuild01: {},
			},
			api.CloudGCP: {
				api.ClusterBuild02: {},
			},
		},
	}
	tests := []struct {
		name     string
		config   *dispatcher.Config
		disabled sets.Set[string]
	}{
		{
			name:     "Remove gcp and aws clusters",
			config:   cfgWithCloud,
			disabled: sets.New[string]("build01", "build02"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			removeDisabledClusters(tc.config, tc.disabled)
			for provider := range tc.config.BuildFarm {
				for cluster := range tc.config.BuildFarm[provider] {
					if tc.disabled.Has(string(cluster)) {
						t.Errorf("%s: expected %s cluster to be removed from BuildFarm config", t.Name(), cluster)

					}
					if clusters, ok := tc.config.BuildFarmCloud[provider]; ok {
						if has := tc.disabled.HasAny(clusters...); has {
							t.Errorf("%s: expected %s cluster to be removed from BuildFarmCloud config", t.Name(), cluster)
						}
					}
				}
			}

		})
	}
}

func TestDetermineCluster(t *testing.T) {
	type fields struct {
		blocked sets.Set[string]
	}
	type args struct {
		cluster           string
		determinedCluster string
		defaultCluster    string
		canBeRelocated    bool
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   string
	}{
		{
			name: "relocate to cluster for a test group",
			fields: fields{
				blocked: sets.New[string](),
			},
			args: args{
				cluster:           "build01",
				determinedCluster: "build02",
				defaultCluster:    "build03",
				canBeRelocated:    true,
			},
			want: "build01",
		},
		{
			name: "can't relocate to cluster for a test group",
			fields: fields{
				blocked: sets.New[string](),
			},
			args: args{
				cluster:           "build01",
				determinedCluster: "build02",
				defaultCluster:    "build03",
				canBeRelocated:    false,
			},
			want: "build02",
		},
		{
			name: "both clusters are blocked, relocate to default",
			fields: fields{
				blocked: sets.New[string]("build01", "build02"),
			},
			args: args{
				cluster:           "build01",
				determinedCluster: "build02",
				defaultCluster:    "build03",
				canBeRelocated:    false,
			},
			want: "build03",
		},
		{
			name: "determined is blocked, relocate to a group cluster despite canBeRelocated=false",
			fields: fields{
				blocked: sets.New[string]("build02"),
			},
			args: args{
				cluster:           "build01",
				determinedCluster: "build02",
				defaultCluster:    "build03",
				canBeRelocated:    false,
			},
			want: "build01",
		},
		{
			name: "group cluster is blocked, use determined cluster",
			fields: fields{
				blocked: sets.New[string]("build01"),
			},
			args: args{
				cluster:           "build01",
				determinedCluster: "build02",
				defaultCluster:    "build03",
				canBeRelocated:    false,
			},
			want: "build02",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cv := &clusterVolume{
				clusterVolumeMap: make(map[string]map[string]float64),
				cloudProviders:   make(sets.Set[string]),
				pjs:              make(map[string]string),
				blocked:          tt.fields.blocked,
				mutex:            sync.Mutex{},
			}
			if got := cv.determineCluster(tt.args.cluster, tt.args.determinedCluster, tt.args.defaultCluster, tt.args.canBeRelocated); got != tt.want {
				t.Errorf("clusterVolume.determineCluster() = %v, want %v", got, tt.want)
			}
		})
	}
}
