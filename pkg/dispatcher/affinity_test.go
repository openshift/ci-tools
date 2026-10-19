package dispatcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
)

func TestAffinityStorePreservesBindings(t *testing.T) {
	inventory := ClusterMap{"source": {Provider: "aws", Capacity: 100}, "target": {Provider: "aws", Capacity: 100}, "gcp": {Provider: "gcp", Capacity: 100}}
	tests := []struct {
		name        string
		steps       []map[string]ProwJobData
		wantAliases []string
		sameIDs     []string
		wantError   string
	}{
		{name: "restart preserves baseline binding", steps: []map[string]ProwJobData{{"a": runtimePlacementJob("family", "source", 1)}}, wantAliases: []string{"a"}},
		{name: "renamed paths and new siblings retain aliases", steps: []map[string]ProwJobData{{"a": runtimePlacementJob("old", "source", 1)}, {"a": runtimePlacementJob("new", "source", 1), "b": runtimePlacementJob("new", "source", 1)}}, wantAliases: []string{"a", "b"}, sameIDs: []string{"a", "b"}},
		{name: "delete and readd retains unit tombstone", steps: []map[string]ProwJobData{{"a": runtimePlacementJob("family", "source", 1)}, {}, {"b": runtimePlacementJob("family", "source", 1)}}, wantAliases: []string{"a", "b"}, sameIDs: []string{"a", "b"}},
		{name: "removed job alias prevents changed host", steps: []map[string]ProwJobData{{"a": runtimePlacementJob("family", "source", 1)}, {}, {"a": runtimePlacementJob("renamed", "target", 1)}}, wantError: "conflicts with affinity host"},
		{name: "ambiguous initial group hosts rejected", steps: []map[string]ProwJobData{{"a": runtimePlacementJob("family", "source", 1), "b": runtimePlacementJob("family", "target", 1)}}, wantError: "conflicts with affinity host"},
		{name: "merging previously separate groups rejected", steps: []map[string]ProwJobData{{"a": runtimePlacementJob("one", "source", 1), "b": runtimePlacementJob("two", "target", 1)}, {"a": runtimePlacementJob("merged", "source", 1), "b": runtimePlacementJob("merged", "target", 1)}}, wantError: "collides with another affinity group"},
		{name: "explicit same provider exceptions stay separate", steps: []map[string]ProwJobData{{"a": {Group: "family", Cluster: "source", PlacementKnown: true}, "b": {Group: "family", Cluster: "target", PlacementKnown: true}}}, wantAliases: []string{"a", "b"}},
		{name: "manual fixed cluster absent from inventory", steps: []map[string]ProwJobData{{"a": {Group: "family", Cluster: "app.ci", PlacementKnown: true}}}, wantAliases: []string{"a"}},
		{name: "non Kubernetes empty cluster is not bound", steps: []map[string]ProwJobData{{"external": {PlacementKnown: true}}}, wantAliases: []string{}},
		{name: "unknown cluster fails closed", steps: []map[string]ProwJobData{{"a": runtimePlacementJob("family", "unknown", 1)}}, wantError: "no provider inventory"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "affinity.json")
			store, err := OpenAffinityStore(path)
			if err != nil {
				t.Fatal(err)
			}
			var firstID string
			for i, step := range tc.steps {
				before := copyBaseline(step)
				enriched, err := store.Commit(step, inventory)
				if diff := cmp.Diff(before, step); diff != "" {
					t.Fatalf("Commit mutated input (-want +got):\n%s", diff)
				}
				if tc.wantError != "" && i == len(tc.steps)-1 {
					if err == nil || !strings.Contains(err.Error(), tc.wantError) {
						t.Fatalf("wanted error containing %q, got %v", tc.wantError, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if job, exists := enriched["a"]; exists {
					if firstID == "" {
						firstID = job.Affinity.ID
					}
					if job.Affinity.ID != firstID {
						t.Fatal("job alias changed affinity identity")
					}
				}
				store, err = OpenAffinityStore(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			tombstones, err := store.Enrich(nil)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.wantAliases, sortedProwJobNames(tombstones)); diff != "" {
				t.Errorf("unexpected aliases (-want +got):\n%s", diff)
			}
			for _, name := range tc.sameIDs {
				if tombstones[name].Affinity.ID != firstID {
					t.Errorf("job %q lost original affinity", name)
				}
			}
		})
	}
}

func TestAffinityStoreResolve(t *testing.T) {
	inventory := ClusterMap{"source": {Provider: "aws", Capacity: 100}, "target": {Provider: "aws", Capacity: 100}, "gcp": {Provider: "gcp", Capacity: 100}}
	tests := []struct {
		name      string
		jobName   string
		job       ProwJobData
		wantBound bool
		wantError string
	}{
		{name: "existing alias survives file rename", jobName: "old", job: runtimePlacementJob("renamed", "target", 1), wantBound: true},
		{name: "new sibling inherits old group", jobName: "new", job: runtimePlacementJob("family", "target", 1), wantBound: true},
		{name: "unrelated group stays unbound", jobName: "new", job: runtimePlacementJob("other", "target", 1)},
		{name: "new capability incompatible with host rejected", jobName: "old", job: runtimePlacementJob("family", "source", 1, "gpu"), wantError: "lacks required capabilities"},
		{name: "provider change rejected", jobName: "old", job: runtimePlacementJob("family", "gcp", 1), wantError: "incompatible with provider"},
		{name: "explicit changed placement rejected", jobName: "old", job: ProwJobData{Group: "family", Cluster: "target", PlacementKnown: true}, wantError: "explicit placement"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, err := OpenAffinityStore(filepath.Join(t.TempDir(), "affinity.json"))
			if err != nil {
				t.Fatal(err)
			}
			committed, err := store.Commit(map[string]ProwJobData{"old": runtimePlacementJob("family", "source", 1)}, inventory)
			if err != nil {
				t.Fatal(err)
			}
			affinity, bound, err := store.Resolve(tc.jobName, tc.job, inventory)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil || bound != tc.wantBound {
				t.Fatalf("bound=%v err=%v", bound, err)
			}
			if bound && affinity != committed["old"].Affinity {
				t.Errorf("unexpected affinity: %#v", affinity)
			}
			aliases, err := store.Enrich(nil)
			if err != nil || len(aliases) != 1 {
				t.Fatalf("Resolve mutated aliases: %#v %v", aliases, err)
			}
		})
	}
}

func TestAffinityStoreRejectsMalformedState(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "invalid JSON", data: "{"},
		{name: "missing maps", data: `{"version":1}`},
		{name: "unsupported version", data: `{"version":2,"bindings":{},"groups":{},"jobs":{}}`},
		{name: "unknown fields", data: `{"version":1,"bindings":{},"groups":{},"jobs":{},"unexpected":true}`},
		{name: "dangling group", data: `{"version":1,"bindings":{},"groups":{"g":"missing"},"jobs":{}}`},
		{name: "trailing JSON", data: `{"version":1,"bindings":{},"groups":{},"jobs":{}} {}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "affinity.json")
			if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenAffinityStore(path); err == nil {
				t.Fatal("malformed store accepted")
			}
		})
	}
}

func TestAffinityCommitFailurePreservesState(t *testing.T) {
	tests := []struct {
		name         string
		incompatible bool
	}{
		{name: "unwritable destination"},
		{name: "conflicting plan", incompatible: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "affinity.json")
			store, err := OpenAffinityStore(path)
			if err != nil {
				t.Fatal(err)
			}
			inventory := ClusterMap{"source": {Provider: "aws", Capacity: 100}, "target": {Provider: "aws", Capacity: 100}}
			initial, err := store.Commit(map[string]ProwJobData{"old": runtimePlacementJob("family", "source", 1)}, inventory)
			if err != nil {
				t.Fatal(err)
			}
			candidate := map[string]ProwJobData{"new": runtimePlacementJob("other", "target", 1)}
			if tc.incompatible {
				candidate["old"] = runtimePlacementJob("family", "target", 1)
			}
			if !tc.incompatible {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			before := copyBaseline(candidate)
			if _, err := store.Commit(candidate, inventory); err == nil {
				t.Fatal("invalid commit succeeded")
			}
			if diff := cmp.Diff(before, candidate); diff != "" {
				t.Errorf("input changed (-want +got):\n%s", diff)
			}
			after, err := store.Enrich(nil)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(initial, after); diff != "" {
				t.Errorf("bindings changed (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAffinityBoundUnitsCannotMoveAtRuntime(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	capacity := int32(25)
	tests := []struct {
		name      string
		kind      dispatcherv1.OverrideKind
		wantError string
	}{
		{name: "capacity reports whole unit immovable", kind: dispatcherv1.OverrideKindCapacity},
		{name: "drain rejects bound unit", kind: dispatcherv1.OverrideKindDrain, wantError: "2 affected workloads"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job := runtimePlacementJob("family", "source", 10)
			job.Affinity = PlacementAffinity{ID: "existing", Cluster: "source"}
			var requestedCapacity *int32
			if tc.kind == dispatcherv1.OverrideKindCapacity {
				requestedCapacity = &capacity
			}
			_, summary, err := CompileSnapshot(CompileInput{Baseline: map[string]ProwJobData{"a": job, "b": runtimePlacementJob("family", "source", 10)}, Inventory: ClusterMap{"source": {Provider: "aws", Capacity: 100}, "target": {Provider: "aws", Capacity: 100}}, Now: now, Overrides: []dispatcherv1.DispatchOverride{activeOverride("override", tc.kind, "source", requestedCapacity, "", now)}})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if summary.MovableJobs != 0 || summary.MovedJobs != 0 || len(summary.ImmovableJobs) != 2 {
				t.Fatalf("bound unit moved: %#v", summary)
			}
		})
	}
}

func TestAffinityResolveAllHandlesRenamesBeforeNewSiblings(t *testing.T) {
	inventory := ClusterMap{"source": {Provider: "aws", Capacity: 100}, "target": {Provider: "aws", Capacity: 100}}
	tests := []struct {
		name      string
		current   map[string]ProwJobData
		wantBound []string
		wantError string
	}{
		{name: "new sibling sorts before retained renamed alias", current: map[string]ProwJobData{"a-new": runtimePlacementJob("new", "target", 1), "z-old": runtimePlacementJob("new", "target", 1)}, wantBound: []string{"a-new", "z-old"}},
		{name: "unrelated new units remain unbound", current: map[string]ProwJobData{"new": runtimePlacementJob("other", "target", 1), "z-old": runtimePlacementJob("family", "target", 1)}, wantBound: []string{"z-old"}},
		{name: "empty cluster non kubernetes jobs skipped", current: map[string]ProwJobData{"external": {PlacementKnown: true}}, wantBound: []string{}},
		{name: "incompatible alias fails the whole batch", current: map[string]ProwJobData{"a-new": runtimePlacementJob("new", "target", 1), "z-old": runtimePlacementJob("new", "target", 1, "gpu")}, wantError: "lacks required capabilities"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "affinity.json")
			store, err := OpenAffinityStore(path)
			if err != nil {
				t.Fatal(err)
			}
			initial, err := store.Commit(map[string]ProwJobData{"z-old": runtimePlacementJob("family", "source", 1)}, inventory)
			if err != nil {
				t.Fatal(err)
			}
			bound, err := store.ResolveAll(tc.current, inventory)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			names := make([]string, 0, len(bound))
			for _, name := range sortedProwJobNames(tc.current) {
				affinity, exists := bound[name]
				if !exists {
					continue
				}
				names = append(names, name)
				if affinity != initial["z-old"].Affinity {
					t.Fatalf("job %q did not inherit durable binding", name)
				}
			}
			if diff := cmp.Diff(tc.wantBound, names); diff != "" {
				t.Errorf("unexpected bound aliases (-want +got):\n%s", diff)
			}
			after, err := store.Enrich(nil)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(initial, after); diff != "" {
				t.Errorf("lookup mutated store (-want +got):\n%s", diff)
			}
		})
	}
}
