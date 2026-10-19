package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestRecordDemandCoverage(t *testing.T) {
	testCases := []struct {
		name         string
		bound        bool
		sample       float64
		wantSampled  int
		wantFallback int
	}{
		{name: "observed movable", sample: 7, wantSampled: 1},
		{name: "observed affinity bound", bound: true, sample: 7, wantSampled: 1},
		{name: "no positive history", wantFallback: 1},
		{name: "invalid history", sample: math.Inf(1), wantFallback: 1},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			job := dispatcher.ProwJobData{Cluster: "build01", Demand: 7, PlacementKnown: true, Relocatable: true}
			if tc.bound {
				job.Affinity = dispatcher.PlacementAffinity{ID: "unit", Cluster: "build01"}
			}
			var report planPrediction
			recordDemandCoverage(&report, map[string]dispatcher.ProwJobData{"job": job}, map[string]float64{"job": tc.sample})
			if report.SampledJobs != tc.wantSampled || report.FallbackDemandJobs != tc.wantFallback {
				t.Fatalf("sample coverage: %#v", report.planEligibility)
			}
			if tc.bound {
				if report.AffinityBoundJobs != 1 || report.AffinityBoundDemand != 7 || report.UnboundRelocatableJobs != 0 {
					t.Fatalf("bound coverage: %#v", report.planEligibility)
				}
				return
			}
			if report.AffinityBoundJobs != 0 || report.UnboundRelocatableJobs != 1 || report.UnboundRelocatableDemand != 7 {
				t.Fatalf("movable coverage: %#v", report.planEligibility)
			}
		})
	}
}

func TestEvaluateDispatchPlan(t *testing.T) {
	cases := []struct{ name, shadow string }{{name: "demand shadow", shadow: "demand"}, {name: "disabled shadow", shadow: "disabled"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, data := range map[string]string{"a.yaml": "periodics:\n- name: a\n#" + strings.Repeat("padding", 30), "b.yaml": "periodics:\n- name: b\n#" + strings.Repeat("padding", 15), "c.yaml": "periodics:\n- name: c\n"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			config := orderingConfig()
			before := cloneDispatchConfig(config)
			inventory := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 1}, "build02": {Provider: "aws", Capacity: 1}}
			previous := map[string]dispatcher.ProwJobData{"a": {Cluster: "build01", Demand: 7}}
			registry := prometheus.NewRegistry()
			metrics, err := newPlanMetrics(registry)
			if err != nil {
				t.Fatal(err)
			}
			o := options{prowJobConfigDir: root, groupOrder: "file-size", shadowGroupOrder: tc.shadow, movementBudgetPercent: 100, planMetrics: metrics}
			got, err := evaluateDispatchPlan(o, config, map[string]float64{"a": 1, "b": 2, "c": 10}, sets.New[string](), nil, inventory, previous)
			if err != nil {
				t.Fatal(err)
			}
			expectedConfig := cloneDispatchConfig(before)
			expected, err := dispatchJobsWithPolicy(root, expectedConfig, map[string]float64{"a": 1, "b": 2, "c": 10}, sets.New[string](), nil, inventory, previous, 0, 100, 0)
			if err != nil {
				t.Fatal(err)
			}
			if tc.shadow == "demand" {
				shadowFiles, loadErr := loadDispatchFiles(root)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				shadowAssignments, shadowErr := dispatchJobsFromFiles(root, cloneDispatchConfig(before), map[string]float64{"a": 1, "b": 2, "c": 10}, sets.New[string](), nil, inventory, previous, 0, 100, 0, shadowFiles, "demand")
				if shadowErr != nil {
					t.Fatal(shadowErr)
				}
				if cmp.Equal(got, shadowAssignments) {
					t.Fatal("test requires a shadow plan that differs from selected placement")
				}
			}
			if diff := cmp.Diff(expected, got); diff != "" {
				t.Errorf("shadow changed selected assignments (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(expectedConfig.BuildFarm, config.BuildFarm); diff != "" {
				t.Errorf("shadow changed selected config (-want +got):\n%s", diff)
			}
			if previous["a"].Demand != 7 {
				t.Error("evaluation mutated prior assignments")
			}
			clone := cloneDispatchConfig(before)
			clone.BuildFarm[api.CloudAWS]["build01"].FilenamesRaw = []string{"changed"}
			clone.BuildFarm[api.CloudAWS]["build01"].Filenames.Insert("changed")
			if len(before.BuildFarm[api.CloudAWS]["build01"].FilenamesRaw) != 0 || before.BuildFarm[api.CloudAWS]["build01"].Filenames.Has("changed") {
				t.Error("configuration clone aliases mutable assignments")
			}
			families, err := registry.Gather()
			if err != nil || len(families) != 1 {
				t.Fatalf("gathered metrics = %v, %v", families, err)
			}
		})
	}
}

func TestPredictPlan(t *testing.T) {
	cases := []struct {
		name            string
		capacity        int
		mean, max, skew float64
	}{{name: "equal capacity", capacity: 1, mean: 5, max: 10, skew: 2}, {name: "capacity normalized", capacity: 2, mean: 2.5, max: 5, skew: 2}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jobs := map[string]dispatcher.ProwJobData{"a": {Cluster: "build01", Demand: 10, Group: "group", PlacementKnown: true, Relocatable: true}, "b": {Cluster: "build02", Group: "group", PlacementKnown: true, Relocatable: true}, "unknown": {Cluster: "manual", Demand: 2}}
			report := predictPlan("demand", jobs, map[string]dispatcher.ProwJobData{"a": {Cluster: "build02"}}, dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: tc.capacity}, "build02": {Provider: "aws", Capacity: tc.capacity}}, sets.New[string]())
			if len(report.Providers) != 1 {
				t.Fatalf("providers: %#v", report.Providers)
			}
			provider := report.Providers[0]
			if provider.NormalizedMean != tc.mean || provider.NormalizedMax != tc.max || provider.MaxMeanSkew != tc.skew || report.MovedJobs != 1 || report.MovedGroups != 1 || report.MovedDemand != 10 || report.SplitGroups != 1 || report.UnknownJobs != 1 || report.UnclassifiedJobs != 1 {
				t.Errorf("unexpected report: %#v", report)
			}
		})
	}
}
