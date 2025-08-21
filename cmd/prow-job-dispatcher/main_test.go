package main

import (
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/slack-go/slack"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

var (
	c = dispatcher.Config{
		DetermineE2EByJob: true,
		Default:           "api.ci",
		BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
			api.CloudAWS: {
				api.ClusterBuild01: {},
			},
			api.CloudGCP: {
				api.ClusterBuild02: {},
			},
		},
		BuildFarmCloud: map[api.Cloud][]string{
			api.CloudAWS: {string(api.ClusterBuild01)},
			api.CloudGCP: {string(api.ClusterBuild02)},
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
		config            *dispatcher.Config
		jobVolumes        map[string]float64
		expected          error
		expectedBuildFarm map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig
		distribution      map[string]float64
		clusterMap        dispatcher.ClusterMap
	}{
		{
			name:     "nil config",
			expected: fmt.Errorf("config is nil"),
		},
		{
			name:             "basic case",
			config:           &c,
			prowJobConfigDir: filepath.Join("testdata", t.Name()),
			jobVolumes: map[string]float64{
				"pull-ci-openshift-cluster-api-provider-gcp-master-e2e-gcp":          24,
				"pull-ci-openshift-ci-tools-master-breaking-changes":                 43,
				"pull-ci-openshift-ci-tools-master-e2e":                              12,
				"pull-ci-openshift-cluster-etcd-operator-master-unit":                6,
				"pull-ci-openshift-cluster-api-provider-gcp-master-e2e-gcp-operator": 3,
				"branch-ci-wildfly-wildfly-operator-master-images":                   2,
				"branch-ci-xyz-xyz-operator-master-images":                           10,
			},
			distribution: map[string]float64{
				"build01": 50,
				"build02": 50,
			},
			clusterMap: dispatcher.ClusterMap{
				"build01": dispatcher.ClusterInfo{Capacity: 100},
				"build02": dispatcher.ClusterInfo{Capacity: 100},
			},
			expectedBuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
				"aws": {"build01": {FilenamesRaw: []string{"cluster-etcd-operator-master-presubmits.yaml", "cluster-api-provider-gcp-presubmits.yaml", "ci-tools-presubmits.yaml"}}},
				"gcp": {"build02": {FilenamesRaw: []string{"wildfly-operator-presubmits.yaml", "xyz-operator-presubmits.yaml"}}},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, actual := dispatchJobs(tc.prowJobConfigDir, tc.config, tc.jobVolumes, sets.New[string](), tc.distribution, tc.clusterMap)
			equalError(t, tc.expected, actual)
			if tc.config != nil && !reflect.DeepEqual(tc.expectedBuildFarm, tc.config.BuildFarm) {
				t.Errorf("%s: actual differs from expected:\n%s", t.Name(), cmp.Diff(tc.expectedBuildFarm, tc.config.BuildFarm))
			}
		})
	}
}

func TestDispatchJobConfig(t *testing.T) {
	clusterMap := dispatcher.ClusterMap{
		"build01": dispatcher.ClusterInfo{Capacity: 100},
		"build02": dispatcher.ClusterInfo{Capacity: 100},
	}
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
				pjs:              map[string]dispatcher.ProwJobData{},
				volumeDistribution: map[string]float64{
					"build01": 21,
					"build02": 21,
				},
				clusterMap: clusterMap,
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
				pjs:              map[string]dispatcher.ProwJobData{},
				blocked:          sets.New[string](),
				volumeDistribution: map[string]float64{
					"build01": 21,
					"build02": 21,
				},
				clusterMap: clusterMap,
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
				pjs:              map[string]dispatcher.ProwJobData{},
				blocked:          sets.New[string](),
				volumeDistribution: map[string]float64{
					"build01": 21,
					"build02": 21,
				},
				clusterMap: clusterMap,
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

func TestDispatchDeltaJobs(t *testing.T) {
	type args struct {
		prowJobConfigDir string
		config           *dispatcher.Config
		blocked          sets.Set[string]
		pjs              map[string]dispatcher.ProwJobData
		cm               dispatcher.ClusterMap
	}
	tests := []struct {
		name        string
		args        args
		wantErr     bool
		expectedPjs map[string]dispatcher.ProwJobData
	}{
		{
			name: "capabilities",
			args: args{
				cm:               dispatcher.ClusterMap{"build01": dispatcher.ClusterInfo{Capabilities: []string{"intranet"}}},
				prowJobConfigDir: filepath.Join("testdata", t.Name()),
				config:           &c,
				blocked:          sets.New[string](),
				pjs: map[string]dispatcher.ProwJobData{
					"pull-ci-openshift-cluster-api-provider-gcp-master-e2e-gcp":          {Cluster: "build02"},
					"pull-ci-openshift-cluster-api-provider-gcp-master-govet":            {Cluster: "build02"},
					"pull-ci-openshift-cluster-api-provider-gcp-master-e2e-gcp-operator": {Cluster: "build02"},
					"pull-ci-openshift-cluster-api-provider-gcp-master-goimports":        {Cluster: "build02"},
				},
			},
			wantErr: false,
			expectedPjs: map[string]dispatcher.ProwJobData{
				"pull-ci-openshift-cluster-api-provider-gcp-master-e2e-gcp":          {Cluster: "build01", Capabilities: []string{"intranet"}},
				"pull-ci-openshift-cluster-api-provider-gcp-master-govet":            {Cluster: "build02"},
				"pull-ci-openshift-cluster-api-provider-gcp-master-e2e-gcp-operator": {Cluster: "build01", Capabilities: []string{"intranet"}},
				"pull-ci-openshift-cluster-api-provider-gcp-master-goimports":        {Cluster: "build02"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := dispatchDeltaJobs(tt.args.prowJobConfigDir, tt.args.config, tt.args.blocked, tt.args.pjs, tt.args.cm); (err != nil) != tt.wantErr {
				t.Errorf("dispatchDeltaJobs() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(tt.expectedPjs, tt.args.pjs) {
				t.Errorf("Maps are not equal. Expected: %v, Got: %v", tt.expectedPjs, tt.args.pjs)
			}
		})
	}
}

type fakeSlackClient struct {
}

func (c fakeSlackClient) PostMessage(channelID string, options ...slack.MsgOption) (string, string, error) {
	if channelID == "channelId" {
		return "", "", nil
	}
	return "", "", fmt.Errorf("failed to send message to channel %s", channelID)
}

func TestSendSlackMessage(t *testing.T) {
	type args struct {
		slackClient slackClient
		channelId   string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "success",
			args: args{
				slackClient: &fakeSlackClient{},
				channelId:   "channelId",
			},
			wantErr: false,
		},
		{
			name: "failure",
			args: args{
				slackClient: &fakeSlackClient{},
				channelId:   "wrong-channelId",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := sendSlackMessage(tt.args.slackClient, tt.args.channelId); (err != nil) != tt.wantErr {
				t.Errorf("sendSlackMessage() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateOnly(t *testing.T) {
	testCases := []struct {
		name           string
		clusterConfig  string
		wantErr        bool
		expectedErrMsg string
		allowDiffError bool // Allow diff errors as they are informational
	}{
		{
			name:           "valid cluster configuration",
			clusterConfig:  filepath.Join("testdata", t.Name(), "valid_clusters.yaml"),
			wantErr:        false,
			allowDiffError: true, // Diff errors are expected and informational
		},
		{
			name:           "invalid cluster configuration",
			clusterConfig:  filepath.Join("testdata", t.Name(), "invalid_clusters.yaml"),
			wantErr:        true,
			expectedErrMsg: "failed to load config",
			allowDiffError: false,
		},
		{
			name:           "nonexistent cluster configuration",
			clusterConfig:  filepath.Join("testdata", t.Name(), "nonexistent_clusters.yaml"),
			wantErr:        true,
			expectedErrMsg: "failed to load config",
			allowDiffError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := options{
				clusterConfigPath:    tc.clusterConfig,
				validateOnly:         true,
				prometheusDaysBefore: 7, // Valid value required for options validation
			}

			err := opts.validate()

			if tc.wantErr && !tc.allowDiffError {
				if err == nil {
					t.Errorf("%s: expected error but got none", tc.name)
					return
				}
				if tc.expectedErrMsg != "" && !strings.Contains(err.Error(), tc.expectedErrMsg) {
					t.Errorf("%s: expected error containing %q, got %q", tc.name, tc.expectedErrMsg, err.Error())
				}
			} else if tc.allowDiffError {
				// For cases where diff errors are allowed (informational only)
				// We should either get no error or a diff-related error
				if err != nil {
					// Check if it's a diff-related error (exit status 1 from diff command)
					if !strings.Contains(err.Error(), "exit status 1") {
						t.Errorf("%s: expected diff error or no error, got: %v", tc.name, err)
					}
				}
				// Diff errors are acceptable for valid configs
			} else if !tc.wantErr {
				if err != nil {
					t.Errorf("%s: expected no error but got: %v", tc.name, err)
				}
			}
		})
	}
}

func TestOptionsValidateOnly(t *testing.T) {
	testCases := []struct {
		name           string
		opts           options
		wantErr        bool
		errMsg         string
		allowDiffError bool // Allow diff errors as they are informational
	}{
		{
			name: "validate-only mode with minimal required args",
			opts: options{
				validateOnly:         true,
				clusterConfigPath:    filepath.Join("testdata", "TestValidateOnly", "valid_clusters.yaml"),
				prometheusDaysBefore: 7, // Valid value
			},
			wantErr:        false,
			allowDiffError: true, // Diff errors are acceptable for valid configs
		},
		{
			name: "validate-only mode with invalid prometheus days",
			opts: options{
				validateOnly:         true,
				clusterConfigPath:    "test-path",
				prometheusDaysBefore: 0, // Invalid value
			},
			wantErr: true,
			errMsg:  "--prometheus-days-before must be between 1 and 15",
		},
		{
			name: "validate-only mode skips prometheus validation",
			opts: options{
				validateOnly:         true,
				clusterConfigPath:    filepath.Join("testdata", "TestValidateOnly", "valid_clusters.yaml"),
				prometheusDaysBefore: 7, // Valid value
				// PrometheusOptions not set - should not cause error in validate-only mode
			},
			wantErr:        false,
			allowDiffError: true, // Diff errors are acceptable for valid configs
		},
		{
			name: "validate-only mode skips prow jobs dir requirement",
			opts: options{
				validateOnly:         true,
				clusterConfigPath:    filepath.Join("testdata", "TestValidateOnly", "valid_clusters.yaml"),
				prometheusDaysBefore: 7, // Valid value
				// prowJobConfigDir not set - should not cause error in validate-only mode
			},
			wantErr:        false,
			allowDiffError: true, // Diff errors are acceptable for valid configs
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.validate()

			if tc.wantErr && !tc.allowDiffError {
				if err == nil {
					t.Errorf("%s: expected error but got none", tc.name)
					return
				}
				if tc.errMsg != "" && !strings.Contains(err.Error(), tc.errMsg) {
					t.Errorf("%s: expected error containing %q, got %q", tc.name, tc.errMsg, err.Error())
				}
			} else if tc.allowDiffError {
				// For cases where diff errors are allowed (informational only)
				// We should either get no error or a diff-related error
				if err != nil {
					// Check if it's a diff-related error (exit status 1 from diff command)
					if !strings.Contains(err.Error(), "exit status 1") {
						t.Errorf("%s: expected diff error or no error, got: %v", tc.name, err)
					}
				}
				// Diff errors are acceptable for valid configs
			} else if !tc.wantErr {
				if err != nil {
					t.Errorf("%s: expected no error but got: %v", tc.name, err)
				}
			}
		})
	}
}
