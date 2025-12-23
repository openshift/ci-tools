package sanitizer

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
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

	config := &dispatcher.Config{Default: "api.ci"}
	if err := defaultJobConfig(jc, "", config, nil, make(sets.Set[string]), dispatcher.ClusterMap{}); err != nil {
		t.Errorf("failed default job config: %v", err)
	}

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

func TestDetermineClusterPreservesValidBuildFarmCluster(t *testing.T) {
	testCases := []struct {
		name            string
		jobCluster      string
		blocked         sets.Set[string]
		buildFarm       map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig
		groups          dispatcher.JobGroups
		expectedCluster string
	}{
		{
			name:       "preserves existing valid build farm cluster",
			jobCluster: "build01",
			blocked:    sets.New[string](),
			buildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
				api.CloudAWS: {
					"build01": {Filenames: sets.New[string]()},
					"build02": {Filenames: sets.New[string]()},
				},
			},
			expectedCluster: "build01",
		},
		{
			name:       "uses algorithm when cluster is blocked",
			jobCluster: "build01",
			blocked:    sets.New[string]("build01"),
			buildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
				api.CloudAWS: {
					"build01": {Filenames: sets.New[string]()},
					"build02": {Filenames: sets.New[string]()},
				},
			},
			groups: dispatcher.JobGroups{
				"special-cluster": {Jobs: []string{"test-job"}},
			},
			expectedCluster: "special-cluster", // algorithm matches job name in Groups
		},
		{
			name:       "uses algorithm when cluster is not in build farm",
			jobCluster: "some-other-cluster",
			blocked:    sets.New[string](),
			buildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{
				api.CloudAWS: {
					"build01": {Filenames: sets.New[string]()},
					"build02": {Filenames: sets.New[string]()},
				},
			},
			groups: dispatcher.JobGroups{
				"special-cluster": {Jobs: []string{"test-job"}},
			},
			expectedCluster: "special-cluster", // algorithm matches job name in Groups
		},
		{
			name:       "uses algorithm when cluster is empty",
			jobCluster: "",
			blocked:    sets.New[string](),
			buildFarm:  map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{},
			groups: dispatcher.JobGroups{
				"special-cluster": {Jobs: []string{"test-job"}},
			},
			expectedCluster: "special-cluster", // algorithm matches job name in Groups
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jc := &config.JobConfig{
				PresubmitsStatic: map[string][]config.Presubmit{
					"org/repo": {{JobBase: config.JobBase{Agent: "kubernetes", Cluster: tc.jobCluster, Name: "test-job"}}},
				},
			}

			cfg := &dispatcher.Config{
				Default:   "api.ci",
				BuildFarm: tc.buildFarm,
				Groups:    tc.groups,
			}

			if err := defaultJobConfig(jc, "org/repo/test.yaml", cfg, nil, tc.blocked, dispatcher.ClusterMap{}); err != nil {
				t.Fatalf("failed to default job config: %v", err)
			}

			got := jc.PresubmitsStatic["org/repo"][0].Cluster
			if got != tc.expectedCluster {
				t.Errorf("expected cluster %q, got %q", tc.expectedCluster, got)
			}
		})
	}
}

func TestIsCIOperatorLatest(t *testing.T) {
	testCases := []struct {
		name     string
		image    string
		expected bool
	}{
		{name: "standard image", image: "ci-operator:latest", expected: true},
		{name: "qci image", image: "quay-proxy.ci.openshift.org/openshift/ci:ci_ci-operator_latest", expected: true},
		{name: "full registry path", image: "registry/namespace/ci-operator:latest", expected: true},
		{name: "different tag", image: "registry/namespace/ci-operator:other", expected: false},
		{name: "fifferent image", image: "other-image:latest", expected: false},
	}
	for _, tc := range testCases {
		t.Run(tc.image, func(t *testing.T) {
			got := isCIOperatorLatest(tc.image)
			if got != tc.expected {
				t.Errorf("For image %s, expected %v but got %v", tc.image, tc.expected, got)
			}
		})
	}
}
