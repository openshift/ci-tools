package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
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
		{name: "equal demand uses path", order: "demand", files: []fileSizeInfo{orderingFile("b.yaml", "b", 100, nil), orderingFile("a.yaml", "a", 1, nil)}, want: []string{"a.yaml", "b.yaml"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files, err := orderDispatchFiles(tc.files, tc.order, orderingConfig(), tc.volumes, nil, 1, nil)
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
		{name: "cloud restriction refreshed", labels: map[string]string{api.CloudLabel: "gcp"}, group: "jobs.yaml", movable: true, cluster: "build01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := orderingConfig()
			original := prowconfig.JobBase{Name: "job"}
			digest := placementDigest(original, "build01", true, "jobs.yaml", config)
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

func TestValidateGroupOrders(t *testing.T) {
	cases := []struct {
		name, selected, shadow string
		wantError              bool
	}{
		{name: "safe defaults", selected: "file-size", shadow: "demand"},
		{name: "reverse comparison", selected: "demand", shadow: "file-size"},
		{name: "shadow disabled", selected: "demand", shadow: "disabled"},
		{name: "invalid selected", selected: "random", shadow: "disabled", wantError: true},
		{name: "invalid shadow", selected: "file-size", shadow: "random", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateGroupOrders(tc.selected, tc.shadow); (err != nil) != tc.wantError {
				t.Errorf("validation error = %v, want error %t", err, tc.wantError)
			}
		})
	}
}

func TestOrderDispatchFilesExcludesAffinity(t *testing.T) {
	cases := []struct {
		name   string
		pinned bool
		want   string
	}{{name: "bound demand excluded", pinned: true, want: "light.yaml"}, {name: "unbound demand included", want: "heavy.yaml"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			previous := map[string]dispatcher.ProwJobData{}
			if tc.pinned {
				previous["heavy"] = dispatcher.ProwJobData{Affinity: dispatcher.PlacementAffinity{ID: "unit", Cluster: "build01"}}
			}
			files, err := orderDispatchFiles([]fileSizeInfo{orderingFile("heavy.yaml", "heavy", 1, nil), orderingFile("light.yaml", "light", 1, nil)}, "demand", orderingConfig(), map[string]float64{"heavy": 10, "light": 1}, nil, 1, previous)
			if err != nil {
				t.Fatal(err)
			}
			if files[0].path != tc.want {
				t.Errorf("first group = %s, want %s", files[0].path, tc.want)
			}
		})
	}
}

func TestPrechargeFixedDemand(t *testing.T) {
	cases := []struct {
		name     string
		affinity bool
	}{{name: "explicit fixed host"}, {name: "durable affinity host", affinity: true}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := orderingConfig()
			fixed := orderingFile("fixed.yaml", "fixed", 1, map[string]string{api.ClusterLabel: "build01"})
			previous := map[string]dispatcher.ProwJobData{}
			if tc.affinity {
				fixed = orderingFile("fixed.yaml", "fixed", 1, nil)
				previous["fixed"] = dispatcher.ProwJobData{Affinity: dispatcher.PlacementAffinity{ID: "unit", Cluster: "build01"}}
			}
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

func TestBoundClusterForConfig(t *testing.T) {
	cases := []struct {
		name, first, second string
		blocked             bool
		want                string
		wantError           bool
	}{
		{name: "one binding", first: "build01", want: "build01"},
		{name: "matching bindings", first: "build01", second: "build01", want: "build01"},
		{name: "conflicting bindings", first: "build01", second: "build02", wantError: true},
		{name: "missing destination", first: "missing", wantError: true},
		{name: "blocked destination", first: "build01", blocked: true, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := orderingFile("jobs.yaml", "first", 1, nil)
			file.jobConfig.Periodics = append(file.jobConfig.Periodics, prowconfig.Periodic{JobBase: prowconfig.JobBase{Name: "second"}})
			prior := map[string]dispatcher.ProwJobData{"first": {Affinity: dispatcher.PlacementAffinity{ID: "unit", Cluster: tc.first}}, "second": {Affinity: dispatcher.PlacementAffinity{ID: "unit", Cluster: tc.second}}}
			blocked := sets.New[string]()
			if tc.blocked {
				blocked.Insert("build01")
			}
			got, err := boundClusterForConfig(file.jobConfig, file.path, orderingConfig(), prior, dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 1}, "build02": {Provider: "aws", Capacity: 1}}, blocked)
			if (err != nil) != tc.wantError || got != tc.want {
				t.Errorf("bound cluster = %q, %v; want %q, error %t", got, err, tc.want, tc.wantError)
			}
		})
	}
}

func TestBoundClusterForConfigCloudFallback(t *testing.T) {
	cases := []struct {
		name, cloud             string
		gcpPool, emptyAzurePool bool
		mapping                 map[api.Cloud]api.Cloud
		want                    string
		wantError               bool
	}{
		{name: "unavailable cloud uses farm fallback", cloud: "azure4", want: "build01"},
		{name: "empty cloud pool uses farm fallback", cloud: "azure4", emptyAzurePool: true, want: "build01"},
		{name: "matching available cloud", cloud: "aws", gcpPool: true, want: "build01"},
		{name: "conflicting available cloud", cloud: "gcp", gcpPool: true, wantError: true},
		{name: "mapping does not alter fallback when e2e pinning disabled", cloud: "azure4", mapping: map[api.Cloud]api.Cloud{"azure4": "gcp"}, gcpPool: true, want: "build01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := orderingConfig()
			config.CloudMapping = tc.mapping
			inventory := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 1}, "build02": {Provider: "aws", Capacity: 1}}
			if tc.gcpPool {
				config.BuildFarm[api.CloudGCP] = map[api.Cluster]*dispatcher.BuildFarmConfig{"build03": {Filenames: sets.New[string]()}}
				inventory["build03"] = dispatcher.ClusterInfo{Provider: "gcp", Capacity: 1}
			}
			if tc.emptyAzurePool {
				config.BuildFarm["azure4"] = map[api.Cluster]*dispatcher.BuildFarmConfig{}
			}
			file := orderingFile("jobs.yaml", "first", 1, map[string]string{api.CloudLabel: tc.cloud})
			prior := map[string]dispatcher.ProwJobData{"first": {Affinity: dispatcher.PlacementAffinity{ID: "unit", Cluster: "build01"}}}
			got, err := boundClusterForConfig(file.jobConfig, file.path, config, prior, inventory, sets.New[string]())
			if (err != nil) != tc.wantError || got != tc.want {
				t.Errorf("bound cluster = %q, %v; want %q, error %t", got, err, tc.want, tc.wantError)
			}
		})
	}
}
