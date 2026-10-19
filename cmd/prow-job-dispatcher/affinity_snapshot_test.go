package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/util/sets"
)

// TestAffinityDispatchSnapshot prevents unbound jobs or changed restrictions from
// entering a generation after its configuration has been loaded.
func TestAffinityDispatchSnapshot(t *testing.T) {
	testCases := []struct {
		name   string
		order  string
		shadow string
		delta  bool
	}{
		{name: "file size with demand shadow", order: "file-size", shadow: "demand"},
		{name: "demand with file size shadow", order: "demand", shadow: "file-size"},
		{name: "delta refresh", delta: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			jobs := filepath.Join(root, "jobs")
			if err := os.Mkdir(jobs, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(jobs, "jobs.yaml")
			original := "periodics:\n- name: movable\n  cluster: build01\n- name: pinned\n  cluster: build02\n  labels:\n    ci-operator.openshift.io/cluster: build02\n"
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			files, err := loadDispatchFiles(jobs)
			if err != nil {
				t.Fatal(err)
			}
			// Simulate a repository update between loading and freezing/planning.
			changed := "periodics:\n- name: movable\n  cluster: build02\n- name: pinned\n  cluster: build01\n  labels:\n    ci-operator.openshift.io/cluster: build01\n- name: unseen-sibling\n  cluster: build02\n"
			if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(jobs, "new.yaml"), []byte("periodics:\n- name: unseen-file\n  cluster: build02\n"), 0600); err != nil {
				t.Fatal(err)
			}
			storePath := filepath.Join(root, "affinity.json")
			store, err := dispatcher.OpenAffinityStore(storePath)
			if err != nil {
				t.Fatal(err)
			}
			config := orderingConfig()
			inventory := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 100}, "build02": {Provider: "aws", Capacity: 100}}
			previous := map[string]dispatcher.ProwJobData{"movable": {Cluster: "build01", Demand: 9}, "pinned": {Cluster: "build02", Demand: 7}}
			frozen, err := affinityPreviousFromFiles(store, jobs, files, config, inventory, previous, 1, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(frozen) != 2 || frozen["movable"].Cluster != "build01" || !frozen["movable"].Relocatable || frozen["pinned"].Cluster != "build02" || frozen["pinned"].Relocatable {
				t.Fatalf("affinity used changed disk configuration: %#v", frozen)
			}
			got := frozen
			if tc.delta {
				if err := dispatchDeltaFiles(files, jobs, config, sets.New[string](), got, inventory, 1); err != nil {
					t.Fatal(err)
				}
			} else {
				registry := prometheus.NewRegistry()
				metrics, err := newPlanMetrics(registry)
				if err != nil {
					t.Fatal(err)
				}
				o := options{prowJobConfigDir: jobs, groupOrder: tc.order, shadowGroupOrder: tc.shadow, movementBudgetPercent: 100, planMetrics: metrics}
				o.MinimumJobDemand = 1
				got, err = evaluateDispatchFiles(o, config, map[string]float64{"movable": 9, "pinned": 7}, sets.New[string](), nil, inventory, frozen, files)
				if err != nil {
					t.Fatal(err)
				}
				families, err := registry.Gather()
				if err != nil {
					t.Fatal(err)
				}
				loads := make(map[string]float64)
				for _, family := range families {
					for _, metric := range family.Metric {
						labels := make(map[string]string)
						for _, label := range metric.Label {
							labels[label.GetName()] = label.GetValue()
						}
						if labels["measure"] == "demand" {
							loads[labels["role"]+"/"+labels["cluster"]] = metric.GetGauge().GetValue()
						}
					}
				}
				wantLoads := map[string]float64{"selected/build01": 9, "selected/build02": 7, "shadow/build01": 9, "shadow/build02": 7}
				if diff := cmp.Diff(wantLoads, loads); diff != "" {
					t.Errorf("selected or shadow used changed disk configuration (-want +got):\n%s", diff)
				}
			}
			if diff := cmp.Diff(frozen, got); diff != "" {
				t.Errorf("dispatch changed frozen assignments (-want +got):\n%s", diff)
			}
			reopened, err := dispatcher.OpenAffinityStore(storePath)
			if err != nil {
				t.Fatal(err)
			}
			persisted, err := reopened.Enrich(nil)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(persisted, got); diff != "" {
				t.Errorf("dispatch diverged from durable snapshot (-want +got):\n%s", diff)
			}
		})
	}
}
