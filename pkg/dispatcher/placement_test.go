package dispatcher

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
)

// runtimePlacementJob gives tests explicit classification rather than legacy metadata.
func runtimePlacementJob(group, cluster string, demand float64, capabilities ...string) ProwJobData {
	return ProwJobData{Group: group, Cluster: cluster, Demand: demand, Capabilities: capabilities, PlacementKnown: true, Relocatable: true}
}

func TestCompileSnapshotColocationUnits(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	capacity := int32(25)
	tests := []struct {
		name          string
		baseline      map[string]ProwJobData
		inventory     ClusterMap
		kind          dispatcherv1.OverrideKind
		wantClusters  map[string]string
		wantMoved     int
		wantGroups    int
		wantImmovable []string
		wantError     string
	}{
		{
			name: "drain keeps siblings together with multiple destinations",
			baseline: map[string]ProwJobData{
				"a": runtimePlacementJob("family", "source", 5),
				"b": runtimePlacementJob("family", "source", 5, "gpu"),
				"c": runtimePlacementJob("other", "source", 10),
				"d": runtimePlacementJob("family", "gcp", 1),
			},
			inventory: ClusterMap{
				"source": {Provider: "aws", Capacity: 100, Capabilities: []string{"gpu"}},
				"plain":  {Provider: "aws", Capacity: 100},
				"gpu":    {Provider: "aws", Capacity: 100, Capabilities: []string{"gpu"}},
				"gcp":    {Provider: "gcp", Capacity: 100},
			},
			kind:         dispatcherv1.OverrideKindDrain,
			wantClusters: map[string]string{"a": "gpu", "b": "gpu", "c": "plain", "d": "gcp"}, wantMoved: 3, wantGroups: 2,
		},
		{
			name: "capacity uses summed unit demand and moves siblings atomically",
			baseline: map[string]ProwJobData{
				"a": runtimePlacementJob("family", "source", 6),
				"b": runtimePlacementJob("family", "source", 6),
				"c": runtimePlacementJob("other", "source", 10),
				"d": runtimePlacementJob("target", "target", 10),
			},
			inventory:    ClusterMap{"source": {Provider: "aws", Capacity: 100}, "target": {Provider: "aws", Capacity: 100}},
			kind:         dispatcherv1.OverrideKindCapacity,
			wantClusters: map[string]string{"a": "target", "b": "target", "c": "source", "d": "target"}, wantMoved: 2, wantGroups: 2,
		},
		{
			name:         "pinned member immobilizes siblings",
			baseline:     map[string]ProwJobData{"a": runtimePlacementJob("family", "source", 1), "b": {Group: "family", Cluster: "source", PlacementKnown: true, Demand: 1}},
			inventory:    ClusterMap{"source": {Provider: "aws", Capacity: 100}, "target": {Provider: "aws", Capacity: 100}},
			kind:         dispatcherv1.OverrideKindCapacity,
			wantClusters: map[string]string{"a": "source", "b": "source"}, wantGroups: 1, wantImmovable: []string{"a", "b"},
		},
		{
			name:         "legacy member immobilizes siblings",
			baseline:     map[string]ProwJobData{"a": runtimePlacementJob("family", "source", 1), "b": {Group: "family", Cluster: "source", Demand: 1}},
			inventory:    ClusterMap{"source": {Provider: "aws", Capacity: 100}, "target": {Provider: "aws", Capacity: 100}},
			kind:         dispatcherv1.OverrideKindCapacity,
			wantClusters: map[string]string{"a": "source", "b": "source"}, wantGroups: 1, wantImmovable: []string{"a", "b"},
		},
		{
			name:      "drain rejects without whole unit capability intersection",
			baseline:  map[string]ProwJobData{"a": runtimePlacementJob("family", "source", 1, "gpu"), "b": runtimePlacementJob("family", "source", 1, "arm")},
			inventory: ClusterMap{"source": {Provider: "aws", Capacity: 100, Capabilities: []string{"gpu", "arm"}}, "gpu": {Provider: "aws", Capacity: 100, Capabilities: []string{"gpu"}}, "arm": {Provider: "aws", Capacity: 100, Capabilities: []string{"arm"}}},
			kind:      dispatcherv1.OverrideKindDrain, wantError: "2 affected workloads",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var requestedCapacity *int32
			if tc.kind == dispatcherv1.OverrideKindCapacity {
				requestedCapacity = &capacity
			}
			snapshot, summary, err := CompileSnapshot(CompileInput{Baseline: tc.baseline, Inventory: tc.inventory, Now: now, Overrides: []dispatcherv1.DispatchOverride{activeOverride("override", tc.kind, "source", requestedCapacity, "", now)}})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected error containing %q, got %v", tc.wantError, err)
				}
				if snapshot != nil {
					t.Fatal("rejected override returned a snapshot")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			clusters := make(map[string]string, len(snapshot.Assignments))
			for name, assignment := range snapshot.Assignments {
				clusters[name] = assignment.Cluster
			}
			if diff := cmp.Diff(tc.wantClusters, clusters); diff != "" {
				t.Errorf("unexpected clusters (-want +got):\n%s", diff)
			}
			if summary.MovedJobs != tc.wantMoved || summary.AffectedGroups != tc.wantGroups {
				t.Errorf("unexpected summary: %#v", summary)
			}
			if diff := cmp.Diff(tc.wantImmovable, summary.ImmovableJobs); diff != "" {
				t.Errorf("unexpected immovable jobs (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCompileSnapshotRejectsPartialGroupScope(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	capacity := int32(25)
	for _, tc := range []struct {
		name     string
		kind     dispatcherv1.OverrideKind
		capacity *int32
	}{
		{name: "drain", kind: dispatcherv1.OverrideKindDrain},
		{name: "capacity", kind: dispatcherv1.OverrideKindCapacity, capacity: &capacity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, _, err := CompileSnapshot(CompileInput{
				Baseline:  map[string]ProwJobData{"a": runtimePlacementJob("family", "source", 1, "gpu"), "b": runtimePlacementJob("family", "source", 1)},
				Inventory: ClusterMap{"source": {Provider: "aws", Capacity: 100, Capabilities: []string{"gpu"}}, "target": {Provider: "aws", Capacity: 100, Capabilities: []string{"gpu"}}},
				Now:       now,
				Overrides: []dispatcherv1.DispatchOverride{activeOverride("override", tc.kind, "source", tc.capacity, "gpu", now)},
			})
			if err == nil || !strings.Contains(err.Error(), "splits colocation group") {
				t.Fatalf("expected split group error, got %v", err)
			}
			if snapshot != nil {
				t.Fatal("rejected override returned a snapshot")
			}
		})
	}
}
