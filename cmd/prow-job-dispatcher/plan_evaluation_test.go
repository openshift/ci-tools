package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/dispatcher"
)

func TestEvaluateDispatchFiles(t *testing.T) {
	cases := []struct{ name, shadow string }{{name: "isolated demand shadow", shadow: "demand"}, {name: "disabled shadow", shadow: "disabled"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, data := range map[string]string{"a.yaml": "periodics:\n- name: a\n#" + strings.Repeat("padding", 30), "b.yaml": "periodics:\n- name: b\n#" + strings.Repeat("padding", 15), "c.yaml": "periodics:\n- name: c\n"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			files, err := loadDispatchFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			config := orderingConfig()
			originalFarm := config.BuildFarm
			expectedConfig, shadowConfig := *config, *config
			inventory := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 1}, "build02": {Provider: "aws", Capacity: 1}}
			previous := map[string]dispatcher.ProwJobData{"a": {Cluster: "build01", Demand: 7}}
			volumes := map[string]float64{"a": 1, "b": 2, "c": 10}
			expected, err := dispatchJobsFromFiles(root, &expectedConfig, volumes, sets.New[string](), nil, inventory, previous, 0, 100, 0, files, "file-size")
			if err != nil {
				t.Fatal(err)
			}
			shadow, err := dispatchJobsFromFiles(root, &shadowConfig, volumes, sets.New[string](), nil, inventory, previous, 0, 100, 0, files, "demand")
			if err != nil {
				t.Fatal(err)
			}
			if cmp.Equal(expected, shadow) {
				t.Fatal("test requires distinct selected and shadow placements")
			}
			// Both policies must use the loaded revision, even if git-sync replaces a file.
			if err := os.WriteFile(filepath.Join(root, "a.yaml"), []byte("invalid: ["), 0600); err != nil {
				t.Fatal(err)
			}
			o := options{prowJobConfigDir: root, groupOrder: "file-size", shadowGroupOrder: tc.shadow, movementBudgetPercent: 100}
			got, err := evaluateDispatchFiles(o, config, volumes, sets.New[string](), nil, inventory, previous, files)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(expected, got); diff != "" {
				t.Errorf("unexpected selected assignments (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(expectedConfig.BuildFarm, config.BuildFarm); diff != "" {
				t.Errorf("unexpected selected config (-want +got):\n%s", diff)
			}
			if previous["a"].Demand != 7 || len(originalFarm["aws"]["build01"].FilenamesRaw) != 0 {
				t.Error("evaluation mutated shared inputs")
			}
		})
	}
}

func TestPlanMetrics(t *testing.T) {
	cases := []struct {
		name     string
		capacity int
		wantLoad float64
	}{{name: "unit capacity", capacity: 1, wantLoad: 10}, {name: "normalized capacity", capacity: 2, wantLoad: 5}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jobs := map[string]dispatcher.ProwJobData{"a": {Cluster: "build01", Demand: 10}}
			report := predictPlan("demand", jobs, map[string]dispatcher.ProwJobData{"a": {Cluster: "build02"}}, dispatcher.ClusterMap{"build01": {Capacity: tc.capacity}, "build02": {Capacity: 1}, "blocked": {Capacity: 1}, "disabled": {}}, sets.New[string]("blocked"))
			want := planPrediction{Order: "demand", NormalizedLoad: map[string]float64{"build01": tc.wantLoad, "build02": 0}, MovedDemand: 10}
			if diff := cmp.Diff(want, report); diff != "" {
				t.Errorf("unexpected prediction (-want +got):\n%s", diff)
			}
			metrics, err := newPlanMetrics(prometheus.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			metrics.record("selected", report)
			var load, moved dto.Metric
			if err := metrics.normalizedLoad.WithLabelValues("selected", "build01").Write(&load); err != nil {
				t.Fatal(err)
			}
			if err := metrics.movedDemand.WithLabelValues("selected").Write(&moved); err != nil {
				t.Fatal(err)
			}
			if load.GetGauge().GetValue() != tc.wantLoad || moved.GetGauge().GetValue() != 10 {
				t.Errorf("unexpected metrics: load=%v, moved=%v", &load, &moved)
			}
		})
	}
}
