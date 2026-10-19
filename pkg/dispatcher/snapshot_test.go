package dispatcher

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
)

func activeOverride(id string, kind dispatcherv1.OverrideKind, cluster string, capacity *int32, capability string, now time.Time) dispatcherv1.DispatchOverride {
	return NewOverride(dispatcherv1.DispatchOverrideSpec{
		ID: id, PlanID: "plan-" + id, SourceGeneration: 1, PolicyInputDigest: "digest", Kind: kind, Cluster: cluster,
		Scope: dispatcherv1.DispatchOverrideScope{Capability: capability}, Capacity: capacity,
		StartsAt: metav1.NewTime(now.Add(-time.Minute)), ExpiresAt: metav1.NewTime(now.Add(time.Hour)),
		CreatedBy: "U1", SourceChannelID: "C1", Reason: "incident", RequiredApprovals: 1,
		Approvals: []dispatcherv1.DispatchApproval{{UserID: "U1", At: metav1.NewTime(now)}}, IdempotencyKey: id,
	})
}

func TestCompileSnapshotCapacityMovesOnlyNeededDemand(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	capacity := int32(25)
	baseline := map[string]ProwJobData{
		"job-a": {Cluster: "build01", Demand: 10},
		"job-b": {Cluster: "build01", Demand: 10},
		"job-c": {Cluster: "build01", Demand: 10},
		"job-d": {Cluster: "build02", Demand: 10},
	}
	inventory := ClusterMap{
		"build01": {Provider: "aws", Capacity: 100},
		"build02": {Provider: "aws", Capacity: 100},
	}
	snapshot, summary, err := CompileSnapshot(CompileInput{
		Baseline: baseline, Inventory: inventory, Blocked: sets.New[string](), Generation: 2, Now: now,
		Overrides: []dispatcherv1.DispatchOverride{activeOverride("capacity", dispatcherv1.OverrideKindCapacity, "build01", &capacity, "", now)},
	})
	if err != nil {
		t.Fatalf("CompileSnapshot returned error: %v", err)
	}
	if summary.MovedJobs != 2 {
		t.Fatalf("expected two moves, got %#v", summary)
	}
	moved := 0
	for _, assignment := range snapshot.Assignments {
		if assignment.OverrideID != "" {
			moved++
			if assignment.Cluster != "build02" || assignment.BaselineCluster != "build01" {
				t.Fatalf("unexpected runtime assignment: %#v", assignment)
			}
		}
	}
	if moved != 2 {
		t.Fatalf("expected two override assignments, got %d", moved)
	}
}

func TestCompileSnapshotCapabilityCapacityUsesScopedLoad(t *testing.T) {
	now := time.Now().UTC()
	capacity := int32(25)
	snapshot, summary, err := CompileSnapshot(CompileInput{
		Baseline: map[string]ProwJobData{
			"source-unscoped": {Cluster: "build01", Demand: 100},
			"source-cap-a":    {Cluster: "build01", Demand: 10, Capabilities: []string{"gpu"}},
			"source-cap-b":    {Cluster: "build01", Demand: 10, Capabilities: []string{"gpu"}},
			"target-cap":      {Cluster: "build02", Demand: 10, Capabilities: []string{"gpu"}},
		},
		Inventory: ClusterMap{
			"build01": {Provider: "aws", Capacity: 100, Capabilities: []string{"gpu"}},
			"build02": {Provider: "aws", Capacity: 100, Capabilities: []string{"gpu"}},
		},
		Overrides:  []dispatcherv1.DispatchOverride{activeOverride("cap", dispatcherv1.OverrideKindCapacity, "build01", &capacity, "gpu", now)},
		Generation: 1, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.MovedJobs != 1 || snapshot.Assignments["source-unscoped"].OverrideID != "" {
		t.Fatalf("capability capacity did not use scoped load: summary=%#v unscoped=%#v", summary, snapshot.Assignments["source-unscoped"])
	}
}

func TestCompileSnapshotIsDeterministic(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	capacity := int32(25)
	first, _, err := CompileSnapshot(CompileInput{
		Baseline:   map[string]ProwJobData{"b": {Cluster: "build01", Demand: .1}, "a": {Cluster: "build01", Demand: .2}, "c": {Cluster: "build02", Demand: .3}},
		Inventory:  ClusterMap{"build02": {Provider: "aws", Capacity: 100}, "build01": {Provider: "aws", Capacity: 100}},
		Overrides:  []dispatcherv1.DispatchOverride{activeOverride("capacity", dispatcherv1.OverrideKindCapacity, "build01", &capacity, "", now)},
		Generation: 4, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := CompileSnapshot(CompileInput{
		Baseline:   map[string]ProwJobData{"c": {Cluster: "build02", Demand: .3}, "a": {Cluster: "build01", Demand: .2}, "b": {Cluster: "build01", Demand: .1}},
		Inventory:  ClusterMap{"build01": {Provider: "aws", Capacity: 100}, "build02": {Provider: "aws", Capacity: 100}},
		Overrides:  []dispatcherv1.DispatchOverride{activeOverride("capacity", dispatcherv1.OverrideKindCapacity, "build01", &capacity, "", now)},
		Generation: 4, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Checksum != second.Checksum || !reflect.DeepEqual(first.Assignments, second.Assignments) {
		t.Fatalf("same policy inputs were not deterministic: %s != %s", first.Checksum, second.Checksum)
	}
}

func TestCompileSnapshotRejectsUnrepresentedCapability(t *testing.T) {
	now := time.Now().UTC()
	override := activeOverride("cap", dispatcherv1.OverrideKindDrain, "build01", nil, "gpu", now)
	_, _, err := CompileSnapshot(CompileInput{
		Baseline:  map[string]ProwJobData{"job": {Cluster: "build01"}},
		Inventory: ClusterMap{"build01": {Provider: "aws", Capacity: 100}, "build02": {Provider: "aws", Capacity: 100}},
		Overrides: []dispatcherv1.DispatchOverride{override}, Generation: 1, Now: now,
	})
	if err == nil {
		t.Fatal("expected unrepresented capability to be rejected")
	}
}

func TestValidateOverrideRejectsInvalidCapacityBeforeBlockedClusterShortcut(t *testing.T) {
	now := time.Now().UTC()
	override := activeOverride("invalid-capacity", dispatcherv1.OverrideKindCapacity, "blocked", nil, "", now)
	if err := validateOverride(&override, map[string]ProwJobData{}, ClusterMap{}, sets.New[string]("blocked")); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("blocked-cluster capacity override without capacity was accepted: %v", err)
	}
}

func TestValidateOverrideSetTreatsDifferentCapabilitiesAsPotentiallyOverlapping(t *testing.T) {
	now := time.Now().UTC()
	first := activeOverride("first", dispatcherv1.OverrideKindDrain, "build01", nil, "gpu", now)
	second := activeOverride("second", dispatcherv1.OverrideKindDrain, "build01", nil, "intranet", now)
	if err := ValidateOverrideSet([]dispatcherv1.DispatchOverride{first, second}, now); err == nil {
		t.Fatal("different capability scopes on one cluster were treated as provably disjoint")
	}
}

func TestCompileSnapshotRejectsConcurrentOverridesInOneProviderPool(t *testing.T) {
	now := time.Now().UTC()
	first := activeOverride("first", dispatcherv1.OverrideKindDrain, "build01", nil, "", now)
	second := activeOverride("second", dispatcherv1.OverrideKindDrain, "build02", nil, "", now)
	_, _, err := CompileSnapshot(CompileInput{
		Baseline: map[string]ProwJobData{
			"job-a": {Cluster: "build01", Demand: 1},
			"job-b": {Cluster: "build02", Demand: 1},
		},
		Inventory: ClusterMap{
			"build01": {Provider: "aws", Capacity: 100},
			"build02": {Provider: "aws", Capacity: 100},
			"build03": {Provider: "aws", Capacity: 100},
		},
		Overrides: []dispatcherv1.DispatchOverride{first, second}, Generation: 1, Now: now,
	})
	if err == nil || !strings.Contains(err.Error(), "provider placement pool") {
		t.Fatalf("concurrent overrides in one placement pool were accepted: %v", err)
	}
}

func TestCompileSnapshotReportsImpactPerOverride(t *testing.T) {
	now := time.Now().UTC()
	capacity := int32(25)
	first := activeOverride("first", dispatcherv1.OverrideKindCapacity, "build01", &capacity, "", now)
	second := activeOverride("second", dispatcherv1.OverrideKindCapacity, "build03", &capacity, "", now)
	_, summary, err := CompileSnapshot(CompileInput{
		Baseline: map[string]ProwJobData{
			"aws-job": {Cluster: "build01", Demand: 10},
			"gcp-job": {Cluster: "build03", Demand: 10},
		},
		Inventory: ClusterMap{
			"build01": {Provider: "aws", Capacity: 100},
			"build02": {Provider: "aws", Capacity: 100},
			"build03": {Provider: "gcp", Capacity: 100},
			"build04": {Provider: "gcp", Capacity: 100},
		},
		Overrides: []dispatcherv1.DispatchOverride{first, second}, Generation: 1, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.AffectedJobs != 2 || summary.ByOverride["first"].AffectedJobs != 1 || summary.ByOverride["second"].AffectedJobs != 1 {
		t.Fatalf("override impacts were not separated: %#v", summary)
	}
}

func TestCompileSnapshotDrainRejectsPartialRelocation(t *testing.T) {
	now := time.Now().UTC()
	override := activeOverride("drain", dispatcherv1.OverrideKindDrain, "build01", nil, "", now)
	_, summary, err := CompileSnapshot(CompileInput{
		Baseline: map[string]ProwJobData{
			"movable":   {Cluster: "build01", Demand: 1},
			"immovable": {Cluster: "build01", Demand: 1, Capabilities: []string{"gpu"}},
		},
		Inventory: ClusterMap{
			"build01": {Provider: "aws", Capacity: 100, Capabilities: []string{"gpu"}},
			"build02": {Provider: "aws", Capacity: 100},
		},
		Overrides: []dispatcherv1.DispatchOverride{override}, Generation: 1, Now: now,
	})
	if err == nil || !strings.Contains(err.Error(), "1 affected workloads") {
		t.Fatalf("partial drain was accepted: summary=%#v err=%v", summary, err)
	}
}

func TestCompileSnapshotDigestTracksEffectiveOverrideSet(t *testing.T) {
	now := time.Now().UTC()
	override := activeOverride("drain", dispatcherv1.OverrideKindDrain, "build01", nil, "", now)
	input := CompileInput{
		Baseline:  map[string]ProwJobData{"job": {Cluster: "build01", Demand: 1}},
		Inventory: ClusterMap{"build01": {Provider: "aws", Capacity: 100}, "build02": {Provider: "aws", Capacity: 100}},
		Overrides: []dispatcherv1.DispatchOverride{override}, Generation: 1, Now: now,
	}
	active, _, err := CompileSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Generation++
	input.Now = override.Spec.ExpiresAt.Time
	expired, _, err := CompileSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	if active.InputDigest == expired.InputDigest || len(expired.OverrideIDs) != 0 {
		t.Fatalf("expiry did not change effective policy digest: active=%s expired=%s ids=%v", active.InputDigest, expired.InputDigest, expired.OverrideIDs)
	}
}

func TestSnapshotLookupEvaluatesExpiryOnEveryDecision(t *testing.T) {
	now := time.Now().UTC()
	override := activeOverride("drain", dispatcherv1.OverrideKindDrain, "build01", nil, "", now)
	snapshot, _, err := CompileSnapshot(CompileInput{
		Baseline:  map[string]ProwJobData{"job": {Cluster: "build01", Demand: 1}},
		Inventory: ClusterMap{"build01": {Provider: "aws", Capacity: 100}, "build02": {Provider: "aws", Capacity: 100}},
		Overrides: []dispatcherv1.DispatchOverride{override}, Generation: 1, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewSnapshotManager("")
	if err := manager.Publish(snapshot); err != nil {
		t.Fatal(err)
	}
	active, found := manager.Lookup("job", now)
	if !found || active.Cluster != "build02" || active.Source != "runtime-override" {
		t.Fatalf("unexpected active decision: %#v", active)
	}
	expired, found := manager.Lookup("job", override.Spec.ExpiresAt.Time)
	if !found || expired.Cluster != "build01" || expired.Source != "expired-override-fallback" {
		t.Fatalf("unexpected expired decision: %#v", expired)
	}
}

func TestSnapshotCacheRoundTripAndCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	snapshot, _, err := CompileSnapshot(CompileInput{
		Baseline:  map[string]ProwJobData{"job": {Cluster: "build01"}},
		Inventory: ClusterMap{"build01": {Provider: "aws", Capacity: 100}}, Generation: 1, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewSnapshotManager(path)
	if err := manager.Publish(snapshot); err != nil {
		t.Fatal(err)
	}
	restored := NewSnapshotManager(path)
	if err := restored.Load(); err != nil {
		t.Fatalf("failed to restore snapshot: %v", err)
	}
	if restored.Ready() {
		t.Fatal("restart cache became ready before durable inputs were verified")
	}
	if err := restored.Publish(restored.Current()); err != nil || !restored.Ready() {
		t.Fatalf("failed to verify restored snapshot: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"checksum":"wrong"}`), 0644); err != nil {
		t.Fatal(err)
	}
	corrupt := NewSnapshotManager(path)
	if err := corrupt.Load(); err == nil || corrupt.Ready() {
		t.Fatal("corrupt snapshot became ready")
	}
}

func TestSnapshotManagerCurrentReturnsDeepCopy(t *testing.T) {
	snapshot, _, err := CompileSnapshot(CompileInput{
		Baseline: map[string]ProwJobData{
			"job": {Cluster: "build01", Capabilities: []string{"gpu"}},
		},
		Inventory: ClusterMap{
			"build01": {Provider: "aws", Capacity: 100, Capabilities: []string{"gpu"}},
		},
		Blocked: sets.New[string]("blocked"), Generation: 9, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewSnapshotManager("")
	if err := manager.Publish(snapshot); err != nil {
		t.Fatal(err)
	}
	first := manager.Current()
	assignment := first.Assignments["job"]
	assignment.Cluster = "changed"
	assignment.Capabilities[0] = "changed"
	first.Assignments["job"] = assignment
	job := first.Baseline["job"]
	job.Capabilities[0] = "changed"
	first.Baseline["job"] = job
	cluster := first.Inventory["build01"]
	cluster.Capabilities[0] = "changed"
	first.Inventory["build01"] = cluster
	first.Blocked[0] = "changed"

	second := manager.Current()
	if second.Generation != 9 || second.Assignments["job"].Cluster != "build01" || second.Assignments["job"].Capabilities[0] != "gpu" ||
		second.Baseline["job"].Capabilities[0] != "gpu" || second.Inventory["build01"].Capabilities[0] != "gpu" || second.Blocked[0] != "blocked" {
		t.Fatalf("Current exposed mutable snapshot state: %#v", second)
	}
}
