package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
)

// orderingConfig provides two eligible destinations with independently mutable assignments.
func orderingConfig() *dispatcher.Config {
	return &dispatcher.Config{Default: "build01", BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{api.CloudAWS: {"build01": {Filenames: sets.New[string]()}, "build02": {Filenames: sets.New[string]()}}}}
}

// orderingFile supplies one job without involving filesystem byte counts.
func orderingFile(path, name string, size int64, labels map[string]string) fileSizeInfo {
	return fileSizeInfo{path: path, size: size, jobConfig: &prowconfig.JobConfig{Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: name, Labels: labels}}}}}
}

func TestOrderDispatchFiles(t *testing.T) {
	cases := []struct {
		name, order string
		files       []fileSizeInfo
		volumes     map[string]float64
		want        []string
	}{
		{name: "bytes order opposite demand", order: "file-size", files: []fileSizeInfo{orderingFile("small.yaml", "heavy", 1, nil), orderingFile("large.yaml", "light", 100, nil)}, volumes: map[string]float64{"heavy": 100, "light": 1}, want: []string{"large.yaml", "small.yaml"}},
		{name: "demand overrides bytes", order: "demand", files: []fileSizeInfo{orderingFile("small.yaml", "heavy", 1, nil), orderingFile("large.yaml", "light", 100, nil)}, volumes: map[string]float64{"heavy": 100, "light": 1}, want: []string{"small.yaml", "large.yaml"}},
		{name: "pinned demand excluded", order: "demand", files: []fileSizeInfo{orderingFile("pinned.yaml", "heavy", 100, map[string]string{api.ClusterLabel: "build01"}), orderingFile("movable.yaml", "light", 1, nil)}, volumes: map[string]float64{"heavy": 100, "light": 1}, want: []string{"movable.yaml", "pinned.yaml"}},
		{name: "aggregate demand across group", order: "demand", files: []fileSizeInfo{
			{path: "aggregate.yaml", jobConfig: &prowconfig.JobConfig{Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "a"}}, {JobBase: prowconfig.JobBase{Name: "b"}}}}},
			orderingFile("single.yaml", "single", 100, nil),
		}, volumes: map[string]float64{"a": 6, "b": 6, "single": 10}, want: []string{"aggregate.yaml", "single.yaml"}},
		{name: "missing demand uses minimum", order: "demand", files: []fileSizeInfo{orderingFile("missing.yaml", "missing", 1, nil), orderingFile("sampled.yaml", "sampled", 100, nil)}, volumes: map[string]float64{"sampled": 0.5}, want: []string{"missing.yaml", "sampled.yaml"}},
		{name: "equal demand uses path", order: "demand", files: []fileSizeInfo{orderingFile("b.yaml", "b", 100, nil), orderingFile("a.yaml", "a", 1, nil)}, want: []string{"a.yaml", "b.yaml"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files, err := orderDispatchFiles(tc.files, tc.order, orderingConfig(), tc.volumes, nil, 1)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, file := range files {
				got = append(got, file.path)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("unexpected order (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRefreshJobAssignment(t *testing.T) {
	cases := []struct {
		name    string
		labels  map[string]string
		group   string
		keep    bool
		movable bool
		cluster string
	}{
		{name: "unchanged constraints preserve placement", group: "jobs.yaml", keep: true, movable: true, cluster: "build02"},
		{name: "new explicit pin overrides prior placement", labels: map[string]string{api.ClusterLabel: "build01"}, group: "jobs.yaml", cluster: "build01"},
		{name: "path group change refreshed", group: "other.yaml", movable: true, cluster: "build01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := orderingConfig()
			original := prowconfig.JobBase{Name: "job"}
			digest := placementDigest(original, dispatcher.JobPlacement{Cluster: "build01", BaselineRelocatable: true, RuntimeRelocatable: true}, "jobs.yaml", config)
			assignments := map[string]dispatcher.ProwJobData{"job": {Cluster: "build02", Demand: 7, Group: "jobs.yaml", PlacementKnown: true, Relocatable: true, PlacementDigest: digest}}
			job := prowconfig.JobBase{Name: "job", Labels: tc.labels}
			if err := refreshJobAssignment(job, "build01", tc.group, "jobs.yaml", config, assignments, sets.New[string](), nil, 1); err != nil {
				t.Fatal(err)
			}
			got := assignments["job"]
			if got.Cluster != tc.cluster || got.Group != tc.group || got.Relocatable != tc.movable || got.Demand != 7 || !got.PlacementKnown {
				t.Errorf("unexpected refreshed assignment: %#v", got)
			}
			if (got.PlacementDigest == digest) != tc.keep {
				t.Errorf("placement digest unchanged = %t, want %t", got.PlacementDigest == digest, tc.keep)
			}
		})
	}
}

func TestPrechargeFixedDemand(t *testing.T) {
	cases := []struct {
		name string
	}{{name: "fixed load influences greedy placement without double counting"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := orderingConfig()
			fixed := orderingFile("fixed.yaml", "fixed", 1, map[string]string{api.ClusterLabel: "build01"})
			previous := map[string]dispatcher.ProwJobData{}

			movable := orderingFile("movable.yaml", "movable", 1, nil)
			inventory := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 1}, "build02": {Provider: "aws", Capacity: 1}}
			cv := &clusterVolume{clusterVolumeMap: map[string]map[string]float64{"aws": {"build01": 0, "build02": 0}}, cloudProviders: sets.New[string]("aws"), pjs: map[string]dispatcher.ProwJobData{}, previousAssignments: previous, specialClusters: map[string]float64{}, clusterMap: inventory, movementBudget: -1}
			volumes := map[string]float64{"fixed": 100, "movable": 1}
			if err := cv.prechargeFixedDemand([]fileSizeInfo{movable, fixed}, config, volumes); err != nil {
				t.Fatal(err)
			}
			cluster, err := cv.findClusterForJobConfig("", movable.jobConfig, movable.path, config, volumes)
			if err != nil {
				t.Fatal(err)
			}
			if cluster != "build02" {
				t.Errorf("movable group assigned to %s despite fixed load on build01", cluster)
			}
			if _, err := cv.findClusterForJobConfig("", fixed.jobConfig, fixed.path, config, volumes); err != nil {
				t.Fatal(err)
			}
			want := map[string]float64{"build01": 100, "build02": 1}
			if diff := cmp.Diff(want, cv.clusterVolumeMap["aws"]); diff != "" {
				t.Errorf("fixed demand counted incorrectly (-want +got):\n%s", diff)
			}
			if cv.pjs["fixed"].Cluster != "build01" {
				t.Errorf("fixed placement lost: %#v", cv.pjs["fixed"])
			}
		})
	}
}

func TestCapabilityAssignmentMetadata(t *testing.T) {
	cases := []struct {
		name   string
		legacy bool
	}{
		{name: "new baseline assignment records runtime eligibility"},
		{name: "old known assignment is migrated", legacy: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := orderingConfig()
			job := prowconfig.JobBase{Name: "gpu-job", Labels: map[string]string{"capability/gpu": "gpu"}}
			inventory := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 1, Capabilities: []string{"gpu"}}}
			assignments := map[string]dispatcher.ProwJobData{}
			if tc.legacy {
				assignments[job.Name] = dispatcher.ProwJobData{Cluster: "build01", Demand: 7, Group: "jobs.yaml", PlacementKnown: true, PlacementDigest: "old-digest"}
				if !assignmentsRequireMetadataMigration(assignments) {
					t.Fatal("old known metadata did not require migration")
				}
				if err := refreshJobAssignment(job, "build02", "jobs.yaml", "jobs.yaml", config, assignments, sets.New[string](), inventory, 1); err != nil {
					t.Fatal(err)
				}
			} else {
				cv := &clusterVolume{pjs: assignments, clusterMap: inventory, clusterVolumeMap: map[string]map[string]float64{"aws": {"build01": 0, "build02": 0}}, specialClusters: map[string]float64{}, prowJobConfigDir: ".", minimumJobDemand: 1}
				if err := cv.addToVolume("build02", job, "jobs.yaml", config, map[string]float64{job.Name: 7}); err != nil {
					t.Fatal(err)
				}
			}
			got := assignments[job.Name]
			if got.Cluster != "build01" || !got.Relocatable || !got.PlacementKnown || got.Demand != 7 {
				t.Errorf("capability assignment lost baseline placement or runtime eligibility: %#v", got)
			}
			if assignmentsRequireMetadataMigration(assignments) {
				t.Error("current classification still requires migration")
			}
		})
	}
}

func TestPlacementDigestRestrictions(t *testing.T) {
	cases := []struct {
		name          string
		first, second dispatcher.JobPlacement
		equal         bool
	}{
		{name: "runtime eligibility is part of restrictions", first: dispatcher.JobPlacement{Cluster: "build01"}, second: dispatcher.JobPlacement{Cluster: "build01", RuntimeRelocatable: true}},
		{name: "hard pin destination is part of restrictions", first: dispatcher.JobPlacement{Cluster: "build01"}, second: dispatcher.JobPlacement{Cluster: "build02"}},
		{name: "group pin retains destination despite legacy baseline flag", first: dispatcher.JobPlacement{Cluster: "build01", BaselineRelocatable: true}, second: dispatcher.JobPlacement{Cluster: "build02", BaselineRelocatable: true}},
		{name: "baseline movable destination is independent", first: dispatcher.JobPlacement{Cluster: "build01", BaselineRelocatable: true, RuntimeRelocatable: true}, second: dispatcher.JobPlacement{Cluster: "build02", BaselineRelocatable: true, RuntimeRelocatable: true}, equal: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := orderingConfig()
			first := placementDigest(prowconfig.JobBase{}, tc.first, "jobs.yaml", config)
			second := placementDigest(prowconfig.JobBase{}, tc.second, "jobs.yaml", config)
			if (first == second) != tc.equal {
				t.Errorf("digest equal = %t, want %t", first == second, tc.equal)
			}
		})
	}
}
