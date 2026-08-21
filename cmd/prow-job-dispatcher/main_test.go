package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

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

func TestVerifyProwSchedulerCacheBound(t *testing.T) {
	for _, tc := range []struct {
		name      string
		config    string
		wantError string
	}{
		{
			name: "within bound",
			config: `scheduler:
  enabled: true
  external:
    url: http://prow-job-dispatcher
    cache:
      entry_timeout_interval: 10s
`,
		},
		{
			name: "cache exceeds bound",
			config: `scheduler:
  enabled: true
  external:
    cache:
      entry_timeout_interval: 1m
`,
			wantError: "exceeds propagation bound",
		},
		{
			name: "missing cache duration",
			config: `scheduler:
  enabled: true
  external: {}
`,
			wantError: "must be positive",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "prow-config.yaml")
			if err := os.WriteFile(path, []byte(tc.config), 0o600); err != nil {
				t.Fatal(err)
			}
			err := verifyProwSchedulerCacheBound(path, 30*time.Second)
			if tc.wantError == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantError, err)
			}
		})
	}
}

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
				"aws": {"build01": {FilenamesRaw: []string{"ci-tools-presubmits.yaml"}}},
				"gcp": {"build02": {FilenamesRaw: []string{"cluster-etcd-operator-master-presubmits.yaml", "cluster-api-provider-gcp-presubmits.yaml", "wildfly-operator-presubmits.yaml", "xyz-operator-presubmits.yaml"}}},
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

func TestFindClusterForJobConfigRespectsHysteresisAndMovementBudget(t *testing.T) {
	config := &dispatcher.Config{
		Default: "build01",
		BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
			api.CloudAWS: {"build01": {}, "build02": {}},
		},
		BuildFarmCloud: map[api.Cloud][]string{api.CloudAWS: {"build01", "build02"}},
	}
	jobConfig := &prowconfig.JobConfig{Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "job", Agent: "kubernetes"}}}}
	newVolume := func(load01, load02, hysteresis, budget float64) *clusterVolume {
		return &clusterVolume{
			clusterVolumeMap: map[string]map[string]float64{"aws": {"build01": load01, "build02": load02}},
			cloudProviders:   sets.New[string]("aws"), pjs: map[string]dispatcher.ProwJobData{}, blocked: sets.New[string](),
			volumeDistribution: map[string]float64{"build01": 1, "build02": 1},
			clusterMap: dispatcher.ClusterMap{
				"build01": {Provider: "aws", Capacity: 100},
				"build02": {Provider: "aws", Capacity: 100},
			},
			previousAssignments: map[string]dispatcher.ProwJobData{"job": {Cluster: "build01"}},
			hysteresis:          hysteresis, movementBudget: budget,
		}
	}

	t.Run("hysteresis keeps near-optimal prior placement", func(t *testing.T) {
		cv := newVolume(10, 9, .10, -1)
		cluster, err := cv.findClusterForJobConfig("", jobConfig, "periodics.yaml", config, map[string]float64{"job": 1})
		if err != nil {
			t.Fatal(err)
		}
		if cluster != "build01" {
			t.Fatalf("expected prior cluster within hysteresis, got %q", cluster)
		}
	})

	t.Run("movement budget prevents routine churn", func(t *testing.T) {
		cv := newVolume(100, 0, 0, 0)
		cluster, err := cv.findClusterForJobConfig("", jobConfig, "periodics.yaml", config, map[string]float64{"job": 10})
		if err != nil {
			t.Fatal(err)
		}
		if cluster != "build01" {
			t.Fatalf("expected movement budget to preserve prior cluster, got %q", cluster)
		}
	})

	t.Run("unlimited budget permits meaningful rebalance", func(t *testing.T) {
		cv := newVolume(100, 0, 0, -1)
		cluster, err := cv.findClusterForJobConfig("", jobConfig, "periodics.yaml", config, map[string]float64{"job": 10})
		if err != nil {
			t.Fatal(err)
		}
		if cluster != "build02" {
			t.Fatalf("expected rebalance to build02, got %q", cluster)
		}
	})
}

func TestPreviousClusterAndMovementUseOnlyExistingRelocatableJobs(t *testing.T) {
	jobs := []projectedJob{
		{determinedCluster: "build02", previousCluster: "build02", volume: 100},
		{determinedCluster: "build02", previousCluster: "build02", volume: 100},
		{determinedCluster: "build01", previousCluster: "build01", canBeRelocated: true, blocked: sets.New[string](), volume: 3},
		{determinedCluster: "build01", canBeRelocated: true, blocked: sets.New[string](), volume: 50},
	}
	if actual := previousClusterForProjectedJobs(jobs); actual != "build01" {
		t.Fatalf("previous relocatable cluster = %q, expected build01", actual)
	}
	if actual := movedDemandForCandidate("build02", "build01", jobs); actual != 3 {
		t.Fatalf("moved demand = %v, expected only the existing relocatable demand 3", actual)
	}
}

func TestMovementBudgetUsesConfiguredJobs(t *testing.T) {
	jobConfig := &prowconfig.JobConfig{Periodics: []prowconfig.Periodic{
		{JobBase: prowconfig.JobBase{Name: "sampled"}},
		{JobBase: prowconfig.JobBase{Name: "without-samples"}},
		{JobBase: prowconfig.JobBase{Name: "sampled"}},
	}}
	jobNames := sets.New[string]()
	addConfiguredJobNames(jobConfig, jobNames)
	jobVolumes := map[string]float64{
		"sampled": 9,
		"deleted": 100,
	}
	if actual := movementBudgetForConfiguredJobs(jobNames, jobVolumes, 10, 5); actual != 1.4 {
		t.Fatalf("movement budget = %v, expected 1.4", actual)
	}
}

func TestSortedStringKeys(t *testing.T) {
	want := []string{"a", "b", "c"}
	if diff := cmp.Diff(want, sortedStringKeys(map[string]int{"c": 3, "a": 1, "b": 2})); diff != "" {
		t.Fatalf("sorted keys differ (-want +got):\n%s", diff)
	}
}

func TestStaticJobVolumeAccumulationIsDeterministic(t *testing.T) {
	config := &dispatcher.Config{
		Default: "api.ci",
		BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
			api.CloudAWS: {"build01": {}, "build02": {}},
		},
		BuildFarmCloud: map[api.Cloud][]string{api.CloudAWS: {"build01", "build02"}},
	}
	clusterMap := dispatcher.ClusterMap{
		"build01": {Provider: "aws", Capacity: 100},
		"build02": {Provider: "aws", Capacity: 100},
	}
	jobVolumes := map[string]float64{"large": float64(1 << 53)}
	for i := 1; i <= 8; i++ {
		jobVolumes[fmt.Sprintf("small-%02d", i)] = 1
	}

	jobConfig := func(presubmits bool) *prowconfig.JobConfig {
		jc := &prowconfig.JobConfig{}
		if presubmits {
			jc.PresubmitsStatic = make(map[string][]prowconfig.Presubmit)
			for i := 8; i >= 1; i-- {
				name := fmt.Sprintf("small-%02d", i)
				jc.PresubmitsStatic[fmt.Sprintf("%02d-small", i)] = []prowconfig.Presubmit{{JobBase: prowconfig.JobBase{Name: name}}}
			}
			jc.PresubmitsStatic["00-large"] = []prowconfig.Presubmit{{JobBase: prowconfig.JobBase{Name: "large"}}}
			return jc
		}

		jc.PostsubmitsStatic = make(map[string][]prowconfig.Postsubmit)
		for i := 8; i >= 1; i-- {
			name := fmt.Sprintf("small-%02d", i)
			jc.PostsubmitsStatic[fmt.Sprintf("%02d-small", i)] = []prowconfig.Postsubmit{{JobBase: prowconfig.JobBase{Name: name}}}
		}
		jc.PostsubmitsStatic["00-large"] = []prowconfig.Postsubmit{{JobBase: prowconfig.JobBase{Name: "large"}}}
		return jc
	}
	newClusterVolume := func() *clusterVolume {
		return &clusterVolume{
			clusterVolumeMap:   map[string]map[string]float64{"aws": {"build01": 0, "build02": 0}},
			cloudProviders:     sets.New[string]("aws"),
			pjs:                map[string]dispatcher.ProwJobData{},
			blocked:            sets.New[string](),
			specialClusters:    map[string]float64{},
			volumeDistribution: map[string]float64{"build01": 1, "build02": 1},
			clusterMap:         clusterMap,
		}
	}
	expectedCluster := "build01"
	if stableClusterTieBreak("jobs.yaml", "build02") < stableClusterTieBreak("jobs.yaml", "build01") {
		expectedCluster = "build02"
	}

	for _, tc := range []struct {
		name       string
		presubmits bool
	}{
		{name: "presubmits", presubmits: true},
		{name: "postsubmits"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const iterations = 100
			want := float64(1 << 53)
			for i := 0; i < iterations; i++ {
				cv := newClusterVolume()
				jc := jobConfig(tc.presubmits)
				jobsForProjection, err := cv.classifyJobsForProjection(jc, "jobs.yaml", config, jobVolumes)
				if err != nil {
					t.Fatalf("iteration %d job classification returned an error: %v", i, err)
				}
				projected := projectedVolumeForCandidate("build01", string(config.Default), jobsForProjection)
				if projected != want {
					t.Fatalf("iteration %d projected volume = %v, want %v", i, projected, want)
				}
				selected, err := cv.findClusterForJobConfig("", jc, "jobs.yaml", config, jobVolumes)
				if err != nil {
					t.Fatalf("iteration %d dispatch returned an error: %v", i, err)
				}
				if selected != expectedCluster {
					t.Fatalf("iteration %d selected %q, want stable tie-break selection %q", i, selected, expectedCluster)
				}
				if recorded := cv.clusterVolumeMap["aws"][selected]; recorded != want {
					t.Fatalf("iteration %d recorded volume = %v, want %v", i, recorded, want)
				}
				otherCluster := "build01"
				if selected == otherCluster {
					otherCluster = "build02"
				}
				if recorded := cv.clusterVolumeMap["aws"][otherCluster]; recorded != 0 {
					t.Fatalf("iteration %d recorded volume %v on unselected cluster %q", i, recorded, otherCluster)
				}
			}
		})
	}
}

func TestFindClusterForJobConfigUsesProportionalCapacity(t *testing.T) {
	config := &dispatcher.Config{
		Default: "api.ci",
		BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
			api.CloudAWS: {
				"build01": {},
				"build05": {},
			},
		},
		BuildFarmCloud: map[api.Cloud][]string{api.CloudAWS: {"build01", "build05"}},
	}
	clusterMap := dispatcher.ClusterMap{
		"build01": {Provider: "aws", Capacity: 100},
		"build05": {Provider: "aws", Capacity: 50},
	}
	cv := &clusterVolume{
		clusterVolumeMap: map[string]map[string]float64{
			"aws": {"build01": 80, "build05": 20},
		},
		cloudProviders:     sets.New[string]("aws"),
		pjs:                map[string]dispatcher.ProwJobData{},
		blocked:            sets.New[string](),
		specialClusters:    map[string]float64{},
		volumeDistribution: map[string]float64{"build01": 100, "build05": 50},
		clusterMap:         clusterMap,
	}
	jobConfig := &prowconfig.JobConfig{Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "periodic-a"}}}}

	got, err := cv.findClusterForJobConfig("", jobConfig, "periodic-a.yaml", config, map[string]float64{"periodic-a": 10})
	if err != nil {
		t.Fatalf("findClusterForJobConfig() returned an error: %v", err)
	}
	if got != "build05" {
		t.Fatalf("expected reduced-capacity build05 to win by projected normalized load, got %q", got)
	}
}

func TestFindClusterForJobConfigProjectsOnlyCandidateDemand(t *testing.T) {
	config := &dispatcher.Config{
		Default: "api.ci",
		BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
			api.CloudAWS: {"build01": {}, "build05": {}},
		},
		BuildFarmCloud: map[api.Cloud][]string{api.CloudAWS: {"build01", "build05"}},
	}
	clusterMap := dispatcher.ClusterMap{
		"build01": {Provider: "aws", Capacity: 100},
		"build05": {Provider: "aws", Capacity: 50},
	}
	cv := &clusterVolume{
		clusterVolumeMap: map[string]map[string]float64{
			"aws": {"build01": 0, "build05": 0},
		},
		cloudProviders:     sets.New[string]("aws"),
		pjs:                map[string]dispatcher.ProwJobData{},
		blocked:            sets.New[string](),
		specialClusters:    map[string]float64{},
		volumeDistribution: map[string]float64{"build01": 100, "build05": 50},
		clusterMap:         clusterMap,
	}
	jobConfig := &prowconfig.JobConfig{Periodics: []prowconfig.Periodic{
		{JobBase: prowconfig.JobBase{Name: "pinned", Labels: map[string]string{api.ClusterLabel: "build01"}}},
		{JobBase: prowconfig.JobBase{Name: "relocatable"}},
	}}

	got, err := cv.findClusterForJobConfig("", jobConfig, "mixed-periodics.yaml", config, map[string]float64{
		"pinned":      1000,
		"relocatable": 1,
	})
	if err != nil {
		t.Fatalf("findClusterForJobConfig() returned an error: %v", err)
	}
	if got != "build05" {
		t.Fatalf("expected relocatable demand on build05, got %q", got)
	}
	if got := cv.clusterVolumeMap["aws"]["build01"]; got != 1000 {
		t.Fatalf("expected pinned demand 1000 on build01, got %v", got)
	}
	if got := cv.clusterVolumeMap["aws"]["build05"]; got != 1 {
		t.Fatalf("expected relocatable demand 1 on build05, got %v", got)
	}
}

func TestFindClusterForJobConfigPreservesManualClusterAssignments(t *testing.T) {
	config := &dispatcher.Config{
		Default:        "api.ci",
		ManualClusters: []api.Cluster{"app.ci"},
		BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
			api.CloudAWS: {"build01": {}, "build02": {}},
		},
		BuildFarmCloud: map[api.Cloud][]string{api.CloudAWS: {"build01", "build02"}},
	}
	clusterMap := dispatcher.ClusterMap{
		"build01": {Provider: "aws", Capacity: 100},
		"build02": {Provider: "aws", Capacity: 100},
	}
	jobConfig := &prowconfig.JobConfig{Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{
		Name:    "manual-job",
		Cluster: "app.ci",
	}}}}
	path := "manual-periodics.yaml"
	expectedCandidate := "build01"
	if stableClusterTieBreak(path, "build02") < stableClusterTieBreak(path, "build01") {
		expectedCandidate = "build02"
	}

	for _, tc := range []struct {
		name            string
		blocked         sets.Set[string]
		expectedCluster string
	}{
		{name: "manual cluster remains assigned", blocked: sets.New[string](), expectedCluster: "app.ci"},
		{name: "blocked manual cluster relocates", blocked: sets.New[string]("app.ci"), expectedCluster: expectedCandidate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cv := &clusterVolume{
				clusterVolumeMap:   map[string]map[string]float64{"aws": {"build01": 0, "build02": 0}},
				cloudProviders:     sets.New[string]("aws"),
				pjs:                map[string]dispatcher.ProwJobData{},
				blocked:            tc.blocked,
				specialClusters:    map[string]float64{},
				volumeDistribution: map[string]float64{"build01": 1, "build02": 1},
				clusterMap:         clusterMap,
			}

			selected, err := cv.findClusterForJobConfig("", jobConfig, path, config, map[string]float64{"manual-job": 7})
			if err != nil {
				t.Fatalf("findClusterForJobConfig() returned an error: %v", err)
			}
			if selected != expectedCandidate {
				t.Fatalf("selected candidate %q, expected %q", selected, expectedCandidate)
			}
			if got := cv.pjs["manual-job"].Cluster; got != tc.expectedCluster {
				t.Fatalf("manual-job assigned to %q, expected %q", got, tc.expectedCluster)
			}
			if tc.expectedCluster == "app.ci" {
				if got := cv.specialClusters["app.ci"]; got != 7 {
					t.Fatalf("manual cluster volume = %v, expected 7", got)
				}
				if got := cv.clusterVolumeMap["aws"]["build01"] + cv.clusterVolumeMap["aws"]["build02"]; got != 0 {
					t.Fatalf("manual job contributed %v volume to automatic candidates", got)
				}
			} else if got := cv.clusterVolumeMap["aws"][expectedCandidate]; got != 7 {
				t.Fatalf("relocated manual job volume = %v, expected 7 on %q", got, expectedCandidate)
			}
		})
	}
}

func TestFindClusterForJobConfigStickinessIncludesProjectedDemand(t *testing.T) {
	config := &dispatcher.Config{
		Default: "api.ci",
		BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
			api.CloudAWS: {"build01": {}, "build05": {}},
		},
		BuildFarmCloud: map[api.Cloud][]string{api.CloudAWS: {"build01", "build05"}},
	}
	clusterMap := dispatcher.ClusterMap{
		"build01": {Provider: "aws", Capacity: 100},
		"build05": {Provider: "aws", Capacity: 50},
	}
	jobConfig := &prowconfig.JobConfig{Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{
		Name:    "periodic-a",
		Cluster: "build05",
	}}}}

	testCases := []struct {
		name     string
		volume   float64
		expected string
	}{
		{name: "small group remains on existing cluster", volume: 10, expected: "build05"},
		{name: "oversized group uses proportional scoring", volume: 100, expected: "build01"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cv := &clusterVolume{
				clusterVolumeMap: map[string]map[string]float64{
					"aws": {"build01": 0, "build05": 0},
				},
				cloudProviders:     sets.New[string]("aws"),
				pjs:                map[string]dispatcher.ProwJobData{},
				blocked:            sets.New[string](),
				specialClusters:    map[string]float64{},
				volumeDistribution: map[string]float64{"build01": 100, "build05": 50},
				clusterMap:         clusterMap,
			}

			got, err := cv.findClusterForJobConfig("", jobConfig, "periodic-a.yaml", config, map[string]float64{"periodic-a": tc.volume})
			if err != nil {
				t.Fatalf("findClusterForJobConfig() returned an error: %v", err)
			}
			if got != tc.expected {
				t.Fatalf("selected %q, expected %q", got, tc.expected)
			}
		})
	}
}

func TestFindClusterForJobConfigBreaksTiesDeterministically(t *testing.T) {
	config := &dispatcher.Config{
		Default: "api.ci",
		BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
			api.CloudAWS: {"build01": {}, "build02": {}},
		},
		BuildFarmCloud: map[api.Cloud][]string{api.CloudAWS: {"build01", "build02"}},
	}
	clusterMap := dispatcher.ClusterMap{
		"build01": {Provider: "aws", Capacity: 100},
		"build02": {Provider: "aws", Capacity: 100},
	}
	jobConfig := &prowconfig.JobConfig{Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "periodic-a"}}}}
	path := "periodic-a.yaml"
	expected := "build01"
	if stableClusterTieBreak(path, "build02") < stableClusterTieBreak(path, "build01") {
		expected = "build02"
	}

	for i := 0; i < 50; i++ {
		cv := &clusterVolume{
			clusterVolumeMap: map[string]map[string]float64{
				"aws": {"build02": 0, "build01": 0},
			},
			cloudProviders:     sets.New[string]("aws"),
			pjs:                map[string]dispatcher.ProwJobData{},
			blocked:            sets.New[string](),
			specialClusters:    map[string]float64{},
			volumeDistribution: map[string]float64{"build01": 1, "build02": 1},
			clusterMap:         clusterMap,
		}
		got, err := cv.findClusterForJobConfig("", jobConfig, path, config, nil)
		if err != nil {
			t.Fatalf("iteration %d returned an error: %v", i, err)
		}
		if got != expected {
			t.Fatalf("iteration %d selected %q, expected stable selection %q", i, got, expected)
		}
	}
}

func TestFindClusterForJobConfigTieBreakIsIndependentOfCheckoutRoot(t *testing.T) {
	config := &dispatcher.Config{
		Default: "api.ci",
		BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
			api.CloudAWS: {"build01": {}, "build02": {}},
		},
		BuildFarmCloud: map[api.Cloud][]string{api.CloudAWS: {"build01", "build02"}},
	}
	clusterMap := dispatcher.ClusterMap{
		"build01": {Provider: "aws", Capacity: 100},
		"build02": {Provider: "aws", Capacity: 100},
	}
	jobConfig := &prowconfig.JobConfig{Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "periodic-a"}}}}
	relativePath := filepath.Join("org", "repo", "periodic-a.yaml")

	winnerForPath := func(path string) string {
		if stableClusterTieBreak(path, "build02") < stableClusterTieBreak(path, "build01") {
			return "build02"
		}
		return "build01"
	}
	firstRoot := "checkout-a"
	firstPath := filepath.Join(firstRoot, relativePath)
	secondRoot := ""
	for i := 0; i < 1000; i++ {
		candidateRoot := fmt.Sprintf("checkout-%d", i)
		if winnerForPath(filepath.Join(candidateRoot, relativePath)) != winnerForPath(firstPath) {
			secondRoot = candidateRoot
			break
		}
	}
	if secondRoot == "" {
		t.Fatal("failed to find checkout roots that expose absolute-path-dependent hashing")
	}

	var selected string
	for _, root := range []string{firstRoot, secondRoot} {
		cv := &clusterVolume{
			clusterVolumeMap: map[string]map[string]float64{
				"aws": {"build01": 0, "build02": 0},
			},
			cloudProviders:     sets.New[string]("aws"),
			pjs:                map[string]dispatcher.ProwJobData{},
			blocked:            sets.New[string](),
			specialClusters:    map[string]float64{},
			volumeDistribution: map[string]float64{"build01": 1, "build02": 1},
			clusterMap:         clusterMap,
			prowJobConfigDir:   root,
		}
		got, err := cv.findClusterForJobConfig("", jobConfig, filepath.Join(root, relativePath), config, nil)
		if err != nil {
			t.Fatalf("root %q returned an error: %v", root, err)
		}
		if selected == "" {
			selected = got
			continue
		}
		if got != selected {
			t.Fatalf("root %q selected %q, first root selected %q", root, got, selected)
		}
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

func TestSynchronizeBuildFarmUsesAuthoritativeInventory(t *testing.T) {
	build12Assignment := &dispatcher.BuildFarmConfig{
		FilenamesRaw: []string{"existing-presubmits.yaml"},
		Filenames:    sets.New[string]("existing-presubmits.yaml"),
	}
	config := &dispatcher.Config{
		BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
			api.CloudAWS: {"build01": {}},
			api.CloudGCP: {"build12": build12Assignment},
		},
		BuildFarmCloud: map[api.Cloud][]string{
			api.CloudAWS: {"build01"},
			api.CloudGCP: {"build12"},
		},
	}
	inventory := dispatcher.ClusterMap{
		"build02": {Provider: "gcp", Capacity: 100},
		"build12": {Provider: "aws", Capacity: 100},
	}

	changed, err := config.SynchronizeBuildFarm(inventory)
	if err != nil {
		t.Fatalf("synchronizeBuildFarm() returned an error: %v", err)
	}
	if !changed {
		t.Fatal("expected provider move and inventory changes to be reported")
	}
	if got := config.BuildFarm[api.CloudAWS]["build12"]; got != build12Assignment {
		t.Fatalf("build12 assignment was not preserved during provider move: got %#v", got)
	}
	if _, exists := config.BuildFarm[api.CloudAWS]["build01"]; exists {
		t.Fatal("cluster absent from inventory remained in build farm")
	}
	if got := config.BuildFarm[api.CloudGCP]["build02"]; got == nil || got.Filenames == nil {
		t.Fatalf("new inventory cluster was not initialized: got %#v", got)
	}
	if diff := cmp.Diff(map[api.Cloud][]string{
		api.CloudAWS: {"build12"},
		api.CloudGCP: {"build02"},
	}, config.BuildFarmCloud); diff != "" {
		t.Fatalf("BuildFarmCloud differs from authoritative inventory (-want +got):\n%s", diff)
	}

	changed, err = config.SynchronizeBuildFarm(inventory)
	if err != nil {
		t.Fatalf("second synchronizeBuildFarm() returned an error: %v", err)
	}
	if changed {
		t.Fatal("unchanged authoritative inventory was reported as changed")
	}
}

func TestSynchronizeBuildFarmRejectsDuplicateDispatcherCluster(t *testing.T) {
	config := &dispatcher.Config{BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
		api.CloudAWS: {"build01": {}},
		api.CloudGCP: {"build01": {}},
	}}
	if _, err := config.SynchronizeBuildFarm(dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 100}}); err == nil {
		t.Fatal("expected duplicate dispatcher cluster to be rejected")
	}
}

func TestClusterConfigReconcilerRetriesFailedGeneration(t *testing.T) {
	clusterMap := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 100}}
	blocked := sets.New[string]()
	publishedDigest, err := clusterInventoryDigest(clusterMap, blocked)
	if err != nil {
		t.Fatalf("failed to calculate published inventory digest: %v", err)
	}
	state := &clusterConfigReconciler{publishedInventoryDigest: publishedDigest}
	var forceValues []bool
	attempt := 0
	dispatch := func(force bool) error {
		forceValues = append(forceValues, force)
		attempt++
		if attempt == 1 {
			return errors.New("temporary failure")
		}
		return nil
	}
	controller := &fullDispatchController{dispatch: dispatch}

	attempted, err := state.reconcile(clusterMap, blocked, controller.reconcile)
	if !attempted || err == nil {
		t.Fatalf("first reconcile = attempted %t, err %v; expected attempted failure", attempted, err)
	}
	if state.hasObserved {
		t.Fatal("failed generation advanced observed state")
	}

	attempted, err = state.reconcile(clusterMap, blocked, controller.reconcile)
	if !attempted || err != nil {
		t.Fatalf("retry reconcile = attempted %t, err %v; expected success", attempted, err)
	}
	if !reflect.DeepEqual(forceValues, []bool{false, true}) {
		t.Fatalf("unexpected force sequence: got %v, want [false true]", forceValues)
	}

	attempted, err = state.reconcile(clusterMap, blocked, controller.reconcile)
	if attempted || err != nil {
		t.Fatalf("unchanged observed generation = attempted %t, err %v; expected no-op", attempted, err)
	}
}

func TestClusterConfigReconcilerForcesUnpublishedInventory(t *testing.T) {
	currentClusterMap := dispatcher.ClusterMap{
		"build01": {Provider: "aws", Capacity: 100, Capabilities: []string{"amd64"}},
	}
	currentBlocked := sets.New[string]()
	currentDigest, err := clusterInventoryDigest(currentClusterMap, currentBlocked)
	if err != nil {
		t.Fatalf("failed to calculate current inventory digest: %v", err)
	}
	digestFor := func(clusterMap dispatcher.ClusterMap, blocked sets.Set[string]) string {
		digest, err := clusterInventoryDigest(clusterMap, blocked)
		if err != nil {
			t.Fatalf("failed to calculate inventory digest: %v", err)
		}
		return digest
	}
	oldVersionDigest, err := clusterInventoryDigestForVersion(currentClusterMap, currentBlocked, inventoryDigestVersion-1)
	if err != nil {
		t.Fatalf("failed to calculate old-version inventory digest: %v", err)
	}

	testCases := []struct {
		name            string
		publishedDigest string
		expectedForce   bool
	}{
		{name: "matching digest reuses snapshot", publishedDigest: currentDigest, expectedForce: false},
		{name: "missing digest", expectedForce: true},
		{
			name: "capacity changed",
			publishedDigest: digestFor(dispatcher.ClusterMap{
				"build01": {Provider: "aws", Capacity: 50, Capabilities: []string{"amd64"}},
			}, currentBlocked),
			expectedForce: true,
		},
		{
			name: "capabilities changed",
			publishedDigest: digestFor(dispatcher.ClusterMap{
				"build01": {Provider: "aws", Capacity: 100, Capabilities: []string{"arm64"}},
			}, currentBlocked),
			expectedForce: true,
		},
		{
			name: "provider changed",
			publishedDigest: digestFor(dispatcher.ClusterMap{
				"build01": {Provider: "gcp", Capacity: 100, Capabilities: []string{"amd64"}},
			}, currentBlocked),
			expectedForce: true,
		},
		{
			name:            "blocked state changed",
			publishedDigest: digestFor(currentClusterMap, sets.New[string]("build02")),
			expectedForce:   true,
		},
		{name: "compiler version changed", publishedDigest: oldVersionDigest, expectedForce: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			state := &clusterConfigReconciler{publishedInventoryDigest: tc.publishedDigest}
			var forced bool
			attempted, err := state.reconcile(currentClusterMap, currentBlocked, func(force bool) error {
				forced = force
				return nil
			})
			if err != nil || !attempted {
				t.Fatalf("reconcile = attempted %t, err %v; expected success", attempted, err)
			}
			if forced != tc.expectedForce {
				t.Fatalf("forced = %t, expected %t", forced, tc.expectedForce)
			}
			if state.publishedInventoryDigest != currentDigest {
				t.Fatalf("published digest was not advanced after success")
			}
		})
	}
}

func TestClusterConfigReconcilerDoesNotAdvanceDigestOnFailure(t *testing.T) {
	previousClusterMap := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 50}}
	currentClusterMap := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 100}}
	blocked := sets.New[string]()
	previousDigest, err := clusterInventoryDigest(previousClusterMap, blocked)
	if err != nil {
		t.Fatalf("failed to calculate previous inventory digest: %v", err)
	}
	state := &clusterConfigReconciler{publishedInventoryDigest: previousDigest}

	attempted, err := state.reconcile(currentClusterMap, blocked, func(force bool) error {
		if !force {
			t.Fatal("stale published inventory did not force a dispatch")
		}
		return errors.New("publication failed")
	})
	if !attempted || err == nil {
		t.Fatalf("reconcile = attempted %t, err %v; expected failed attempt", attempted, err)
	}
	if state.publishedInventoryDigest != previousDigest {
		t.Fatal("failed publication advanced the inventory digest")
	}
}

func TestPublishedInventoryDigestLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assignments.gob.inventory-digest")
	digest, err := clusterInventoryDigest(
		dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 100}},
		sets.New[string]("build02"),
	)
	if err != nil {
		t.Fatalf("failed to calculate inventory digest: %v", err)
	}

	if got, valid, err := readPublishedInventoryDigest(path); err != nil || valid || got != "" {
		t.Fatalf("missing digest = %q, valid %t, err %v; expected empty, false, nil", got, valid, err)
	}
	if err := writePublishedInventoryDigest(path, digest); err != nil {
		t.Fatalf("failed to publish inventory digest: %v", err)
	}
	if got, valid, err := readPublishedInventoryDigest(path); err != nil || !valid || got != digest {
		t.Fatalf("published digest = %q, valid %t, err %v; expected %q, true, nil", got, valid, err, digest)
	}
	if err := os.WriteFile(path, []byte("corrupt\n"), 0o644); err != nil {
		t.Fatalf("failed to write corrupt inventory digest: %v", err)
	}
	if got, valid, err := readPublishedInventoryDigest(path); err != nil || valid || got != "" {
		t.Fatalf("corrupt digest = %q, valid %t, err %v; expected empty, false, nil", got, valid, err)
	}
}

func TestFullDispatchInvalidatesDigestBeforeGobReplacement(t *testing.T) {
	jobsStoragePath := filepath.Join(t.TempDir(), "assignments.gob")
	digestPath := publishedInventoryDigestPath(jobsStoragePath)
	digest, err := clusterInventoryDigest(
		dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 100}},
		sets.New[string](),
	)
	if err != nil {
		t.Fatalf("failed to calculate inventory digest: %v", err)
	}
	if err := writePublishedInventoryDigest(digestPath, digest); err != nil {
		t.Fatalf("failed to publish existing inventory digest: %v", err)
	}

	writerCalled := false
	directorySyncErr := &dispatcher.GobWriteCommittedError{Err: errors.New("directory sync failed")}
	err = writeFullDispatchAssignments(jobsStoragePath, map[string]dispatcher.ProwJobData{}, func(_ string, _ interface{}) error {
		writerCalled = true
		if got, valid, err := readPublishedInventoryDigest(digestPath); err != nil || valid || got != "" {
			t.Errorf("digest visible during Gob replacement = %q, valid %t, err %v; expected invalid", got, valid, err)
		}
		return directorySyncErr
	})
	if !writerCalled {
		t.Fatal("Gob writer was not called")
	}
	if !dispatcher.IsGobWriteCommitted(err) {
		t.Fatalf("write returned %v; expected committed Gob error", err)
	}
	if got, valid, err := readPublishedInventoryDigest(digestPath); err != nil || valid || got != "" {
		t.Fatalf("digest after failed Gob directory sync = %q, valid %t, err %v; expected invalid", got, valid, err)
	}
}

func TestClusterInventoryDigestIsCanonical(t *testing.T) {
	first, err := clusterInventoryDigest(dispatcher.ClusterMap{
		"build02": {Provider: "gcp", Capacity: 50, Capabilities: []string{"kvm", "amd64"}},
		"build01": {Provider: "aws", Capacity: 100, Capabilities: []string{"arm64", "amd64"}},
	}, sets.New[string]("build04", "build03"))
	if err != nil {
		t.Fatalf("failed to calculate first inventory digest: %v", err)
	}
	second, err := clusterInventoryDigest(dispatcher.ClusterMap{
		"build01": {Provider: "aws", Capacity: 100, Capabilities: []string{"amd64", "arm64"}},
		"build02": {Provider: "gcp", Capacity: 50, Capabilities: []string{"amd64", "kvm"}},
	}, sets.New[string]("build03", "build04"))
	if err != nil {
		t.Fatalf("failed to calculate second inventory digest: %v", err)
	}
	if first != second {
		t.Fatalf("semantically identical inventories produced different digests: %q != %q", first, second)
	}
}

func TestFullDispatchControllerRetriesFailuresFromAnyTrigger(t *testing.T) {
	var forceValues []bool
	attempt := 0
	controller := &fullDispatchController{dispatch: func(force bool) error {
		forceValues = append(forceValues, force)
		attempt++
		if attempt == 1 {
			return errors.New("temporary failure")
		}
		return nil
	}}

	if err := controller.reconcile(false); err == nil {
		t.Fatal("expected initial dispatch to fail")
	}
	if !controller.hasPendingRetry() {
		t.Fatal("failed dispatch did not record a pending retry")
	}
	if err := controller.reconcile(false); err != nil {
		t.Fatalf("pending retry failed: %v", err)
	}
	if controller.hasPendingRetry() {
		t.Fatal("successful retry did not clear pending state")
	}
	if err := controller.reconcile(false); err != nil {
		t.Fatalf("subsequent dispatch failed: %v", err)
	}
	if !reflect.DeepEqual(forceValues, []bool{false, true, false}) {
		t.Fatalf("unexpected force sequence: got %v, want [false true false]", forceValues)
	}
}

func TestFallbackPublicationMarkerLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assignments.gob.fallback-pending")

	pending, err := fallbackPublicationPending(path)
	if err != nil || pending {
		t.Fatalf("missing marker = pending %t, err %v; expected false, nil", pending, err)
	}
	if err := markFallbackPublicationPending(path); err != nil {
		t.Fatalf("failed to mark fallback publication pending: %v", err)
	}
	pending, err = fallbackPublicationPending(path)
	if err != nil || !pending {
		t.Fatalf("existing marker = pending %t, err %v; expected true, nil", pending, err)
	}
	if err := clearFallbackPublicationPending(path); err != nil {
		t.Fatalf("failed to clear fallback publication marker: %v", err)
	}
	if err := clearFallbackPublicationPending(path); err != nil {
		t.Fatalf("clearing missing fallback publication marker was not idempotent: %v", err)
	}
	pending, err = fallbackPublicationPending(path)
	if err != nil || pending {
		t.Fatalf("cleared marker = pending %t, err %v; expected false, nil", pending, err)
	}
}

func TestWorkingDirectoryIsRestoredBeforeRelativeMarkerCleanup(t *testing.T) {
	workingDirectory := t.TempDir()
	t.Chdir(workingDirectory)

	markerPath := "assignments.gob.fallback-pending"
	if err := markFallbackPublicationPending(markerPath); err != nil {
		t.Fatalf("failed to mark fallback publication pending: %v", err)
	}
	cloneDirectory := t.TempDir()
	if err := withRestoredWorkingDirectory(func() error {
		return os.Chdir(cloneDirectory)
	}); err != nil {
		t.Fatalf("operation with working-directory restoration failed: %v", err)
	}
	if got, err := os.Getwd(); err != nil || got != workingDirectory {
		t.Fatalf("working directory after operation = %q, err %v; expected %q", got, err, workingDirectory)
	}
	if err := os.RemoveAll(cloneDirectory); err != nil {
		t.Fatalf("failed to remove simulated PR checkout: %v", err)
	}
	if err := clearFallbackPublicationPending(markerPath); err != nil {
		t.Fatalf("failed to clear relative fallback publication marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workingDirectory, markerPath)); !os.IsNotExist(err) {
		t.Fatalf("fallback publication marker still exists after cleanup: %v", err)
	}
}

func TestClusterConfigReconcilerForcesProviderChange(t *testing.T) {
	previousClusterMap := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 100}}
	publishedDigest, err := clusterInventoryDigest(previousClusterMap, sets.New[string]())
	if err != nil {
		t.Fatalf("failed to calculate published inventory digest: %v", err)
	}
	state := &clusterConfigReconciler{
		observedClusterMap:       previousClusterMap,
		observedBlocked:          sets.New[string](),
		hasObserved:              true,
		publishedInventoryDigest: publishedDigest,
	}
	var forced bool
	attempted, err := state.reconcile(
		dispatcher.ClusterMap{"build01": {Provider: "gcp", Capacity: 100}},
		sets.New[string](),
		func(force bool) error {
			forced = force
			return nil
		},
	)
	if err != nil || !attempted || !forced {
		t.Fatalf("provider change = attempted %t, forced %t, err %v; expected forced success", attempted, forced, err)
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
					"pull-ci-openshift-cluster-api-provider-gcp-master-e2e-gcp":          {Cluster: "build02", Demand: 7, Group: "preserved-group.yaml"},
					"pull-ci-openshift-cluster-api-provider-gcp-master-govet":            {Cluster: "build02"},
					"pull-ci-openshift-cluster-api-provider-gcp-master-e2e-gcp-operator": {Cluster: "build02"},
					"pull-ci-openshift-cluster-api-provider-gcp-master-goimports":        {Cluster: "build02"},
				},
			},
			wantErr: false,
			expectedPjs: map[string]dispatcher.ProwJobData{
				"pull-ci-openshift-cluster-api-provider-gcp-master-e2e-gcp":          {Cluster: "build01", Capabilities: []string{"intranet"}, Demand: 7, Group: "preserved-group.yaml"},
				"pull-ci-openshift-cluster-api-provider-gcp-master-govet":            {Cluster: "build02"},
				"pull-ci-openshift-cluster-api-provider-gcp-master-e2e-gcp-operator": {Cluster: "build01", Capabilities: []string{"intranet"}, Demand: 5, Group: "capabilities/cluster-api-provider-gcp-presubmits.yaml"},
				"pull-ci-openshift-cluster-api-provider-gcp-master-goimports":        {Cluster: "build02"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := dispatchDeltaJobs(tt.args.prowJobConfigDir, tt.args.config, tt.args.blocked, tt.args.pjs, tt.args.cm, 5); (err != nil) != tt.wantErr {
				t.Errorf("dispatchDeltaJobs() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(tt.expectedPjs, tt.args.pjs) {
				t.Errorf("Maps are not equal. Expected: %v, Got: %v", tt.expectedPjs, tt.args.pjs)
			}
		})
	}
}

func TestAssignmentsRequireMetadataMigration(t *testing.T) {
	testCases := []struct {
		name        string
		assignments map[string]dispatcher.ProwJobData
		expected    bool
	}{
		{name: "empty", assignments: map[string]dispatcher.ProwJobData{}},
		{name: "complete", assignments: map[string]dispatcher.ProwJobData{"job": {Cluster: "build01", Demand: 2, Group: "jobs.yaml"}}},
		{name: "missing demand", assignments: map[string]dispatcher.ProwJobData{"job": {Cluster: "build01", Group: "jobs.yaml"}}, expected: true},
		{name: "missing group", assignments: map[string]dispatcher.ProwJobData{"job": {Cluster: "build01", Demand: 2}}, expected: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if actual := assignmentsRequireMetadataMigration(tc.assignments); actual != tc.expected {
				t.Fatalf("assignmentsRequireMetadataMigration() = %t, expected %t", actual, tc.expected)
			}
		})
	}
}

func TestBlockedClustersForJob(t *testing.T) {
	blocked := sets.New[string]("build01", "build02")

	testCases := []struct {
		name              string
		jobName           string
		determinedCluster string
		expectedBlocked   sets.Set[string]
	}{
		{
			name:              "keeps blocked clusters for non-matching job",
			jobName:           "periodic-something-else",
			determinedCluster: "build01",
			expectedBlocked:   sets.New[string]("build01", "build02"),
		},
		{
			name:              "removes determined cluster for matching upgrade job",
			jobName:           "periodic-build01-upgrade",
			determinedCluster: "build01",
			expectedBlocked:   sets.New[string]("build02"),
		},
		{
			name:              "keeps blocked clusters when determined cluster not blocked",
			jobName:           "periodic-build77-upgrade",
			determinedCluster: "build77",
			expectedBlocked:   sets.New[string]("build01", "build02"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := blockedClustersForJob(tc.jobName, tc.determinedCluster, blocked)
			if !actual.Equal(tc.expectedBlocked) {
				t.Fatalf("unexpected blocked set. expected=%v actual=%v", tc.expectedBlocked.UnsortedList(), actual.UnsortedList())
			}
		})
	}
}

func TestAddToVolumeSkipsBlockedRelocationForMatchingUpgradePeriodic(t *testing.T) {
	config := &dispatcher.Config{
		Default: "api.ci",
		BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
			api.CloudAWS: {
				"build01": {},
			},
			api.CloudGCP: {
				"build02": {},
			},
		},
		Groups: map[api.Cluster]dispatcher.Group{
			"build01": {
				Jobs: []string{"periodic-build01-upgrade", "periodic-something-else"},
			},
		},
	}

	cv := &clusterVolume{
		clusterVolumeMap: map[string]map[string]float64{
			"aws": {"build01": 0},
			"gcp": {"build02": 0},
		},
		cloudProviders: sets.New[string]("aws", "gcp"),
		pjs:            map[string]dispatcher.ProwJobData{},
		blocked:        sets.New[string]("build01"),
		specialClusters: map[string]float64{
			"api.ci": 0,
		},
		clusterMap: dispatcher.ClusterMap{
			"build01": {Capacity: 100},
			"build02": {Capacity: 100},
		},
	}

	jobVolumes := map[string]float64{
		"periodic-build01-upgrade": 1,
		"periodic-something-else":  1,
	}

	if err := cv.addToVolume("build02", prowconfig.JobBase{Name: "periodic-build01-upgrade"}, "foo-periodics.yaml", config, jobVolumes); err != nil {
		t.Fatalf("addToVolume returned error for matching periodic: %v", err)
	}
	if err := cv.addToVolume("build02", prowconfig.JobBase{Name: "periodic-something-else"}, "foo-periodics.yaml", config, jobVolumes); err != nil {
		t.Fatalf("addToVolume returned error for non-matching periodic: %v", err)
	}

	if got := cv.pjs["periodic-build01-upgrade"].Cluster; got != "build01" {
		t.Fatalf("expected matching periodic to stay on determined blocked cluster build01, got %s", got)
	}
	if got := cv.pjs["periodic-something-else"].Cluster; got != "build02" {
		t.Fatalf("expected non-matching periodic to be relocated to build02, got %s", got)
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
