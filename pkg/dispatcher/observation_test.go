package dispatcher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
)

// observationRecords supplies just the durable read operation used by the observer.
type observationRecords struct {
	records []dispatcherv1.DispatchOverride
	err     error
}

func (r observationRecords) Overrides(context.Context) ([]dispatcherv1.DispatchOverride, error) {
	return r.records, r.err
}

// observationNow keeps expiry and JSON timestamps deterministic.
func observationNow() time.Time { return time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC) }

// observationToken is deliberately independent of the control server token.
func observationToken() []byte { return []byte("read-token") }

// observationFixture exercises scoped runtime placement and unused inventory.
func observationFixture(t *testing.T, expired bool) (*SnapshotManager, dispatcherv1.DispatchOverride) {
	t.Helper()
	now := observationNow()
	override := activeOverride("gpu-drain", dispatcherv1.OverrideKindDrain, "build01", nil, "gpu", now.Add(-time.Hour))
	override.Spec.ExpiresAt = metav1.NewTime(now.Add(time.Hour))
	if expired {
		override.Spec.ExpiresAt = metav1.NewTime(now)
	}
	// Observation consumes an installed policy; compiler eligibility is tested separately.
	snapshot := &PolicySnapshot{
		FormatVersion: SnapshotFormatVersion, CompilerVersion: SnapshotCompilerVersion,
		Generation: 7, GeneratedAt: now.Add(-time.Hour), InputDigest: "fixture-policy",
		Baseline: map[string]ProwJobData{
			"a-gpu": {Cluster: "build01", Group: "group-a", Capabilities: []string{"gpu"}, Demand: 2},
			"b-cpu": {Cluster: "build01", Group: "group-c", Demand: 3},
			"c-gpu": {Cluster: "build02", Group: "group-b", Capabilities: []string{"gpu"}, Demand: 4},
		},
		Inventory: ClusterMap{
			"build01": {Provider: "aws", Capacity: 100, Capabilities: []string{"gpu"}},
			"build02": {Provider: "aws", Capacity: 100, Capabilities: []string{"gpu"}},
			"unused":  {Provider: "gcp", Capacity: 20},
		},
		Assignments: map[string]SnapshotAssignment{
			"a-gpu": {Cluster: "build02", BaselineCluster: "build01", Capabilities: []string{"gpu"}, Demand: 2, OverrideID: override.Spec.ID, ValidUntil: override.Spec.ExpiresAt.Time},
			"b-cpu": {Cluster: "build01", BaselineCluster: "build01", Demand: 3},
			"c-gpu": {Cluster: "build02", BaselineCluster: "build02", Capabilities: []string{"gpu"}, Demand: 4},
		},
		Blocked: []string{"unused"}, OverrideIDs: []string{override.Spec.ID},
	}
	checksum, err := snapshotChecksum(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Checksum = checksum
	manager := NewSnapshotManager("")
	if err := manager.Publish(snapshot); err != nil {
		t.Fatal(err)
	}
	return manager, override
}

// observationRequest keeps HTTP assertions focused on observable behavior.
func observationRequest(server *ObservationServer, method, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", token)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func TestObservationHTTPRejections(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, token string
		source                    OverrideReader
		want                      int
		message                   string
	}{
		{name: "missing token", method: "GET", path: "/observe/v1/state", want: 401, message: "unauthorized"},
		{name: "wrong token", method: "GET", path: "/observe/v1/state", token: "Bearer control-token", want: 401, message: "unauthorized"},
		{name: "write method", method: "POST", path: "/observe/v1/overrides", token: "Bearer read-token", want: 405, message: "only GET is supported"},
		{name: "head method", method: "HEAD", path: "/observe/v1/state", token: "Bearer read-token", want: 405, message: "only GET is supported"},
		{name: "unknown route", method: "GET", path: "/other/state", token: "Bearer read-token", want: 404, message: "unknown observation route"},
		{name: "unready jobs", method: "GET", path: "/observe/v1/jobs", token: "Bearer read-token", want: 503, message: "serving policy is not ready"},
		{name: "no runtime controls", method: "GET", path: "/observe/v1/overrides", token: "Bearer read-token", want: 503, message: "runtime override observation is unavailable"},
		{name: "list failure", method: "GET", path: "/observe/v1/overrides", token: "Bearer read-token", source: observationRecords{err: errors.New("store unavailable")}, want: 503, message: "could not list runtime overrides"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := observationRequest(NewObservationServer(nil, tc.source, observationToken), tc.method, tc.path, tc.token)
			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != tc.want || body["error"] != tc.message || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d body=%s headers=%v", response.Code, response.Body.String(), response.Header())
			}
			if tc.want == http.StatusMethodNotAllowed && response.Header().Get("Allow") != "GET" {
				t.Fatal("missing Allow: GET")
			}
		})
	}
}

func TestObservationJobs(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		expired     bool
		want        []string
		next        string
		status      int
	}{
		{name: "stable first page", query: "?limit=1", want: []string{"a-gpu"}, next: "a-gpu", status: 200},
		{name: "continuation", query: "?limit=1&after=a-gpu&generation=7", want: []string{"b-cpu"}, next: "b-cpu", status: 200},
		{name: "last page", query: "?after=b-cpu&generation=7", want: []string{"c-gpu"}, status: 200},
		{name: "combined filters", query: "?name=gpu&group=group-a&cluster=build02", want: []string{"a-gpu"}, status: 200},
		{name: "expiry filters effective cluster", query: "?cluster=build01", expired: true, want: []string{"a-gpu", "b-cpu"}, status: 200},
		{name: "no matches", query: "?name=missing", want: []string{}, status: 200},
		{name: "missing generation", query: "?after=a-gpu", status: 400},
		{name: "stale generation", query: "?after=a-gpu&generation=6", status: 409},
		{name: "invalid generation", query: "?generation=no", status: 400},
		{name: "unbounded limit", query: "?limit=501", status: 400},
		{name: "negative limit", query: "?limit=-1", status: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, _ := observationFixture(t, tc.expired)
			before := manager.Current()
			server := NewObservationServer(manager, nil, observationToken)
			server.now = observationNow
			response := observationRequest(server, "GET", "/observe/v1/jobs"+tc.query, "Bearer read-token")
			if response.Code != tc.status {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			if tc.status != 200 {
				return
			}
			var got observationJobs
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			names := make([]string, 0, len(got.Jobs))
			for _, row := range got.Jobs {
				names = append(names, row.Name)
				decision, _ := manager.Lookup(row.Name, observationNow())
				if diff := cmp.Diff(decision, row.Decision); diff != "" {
					t.Errorf("decision (-want +got):\n%s", diff)
				}
				baseline := before.Baseline[row.Name]
				if row.Group != baseline.Group || row.Demand != baseline.Demand || row.BaselineCluster != baseline.Cluster || !cmp.Equal(row.Capabilities, baseline.Capabilities) {
					t.Errorf("incorrect baseline data: %#v", row)
				}
			}
			if diff := cmp.Diff(tc.want, names); diff != "" {
				t.Errorf("names (-want +got):\n%s", diff)
			}
			if got.NextAfter != tc.next || got.Policy.Generation != 7 || got.ObservedAt != observationNow() {
				t.Errorf("incorrect metadata: %#v", got)
			}
			if diff := cmp.Diff(before, manager.Current()); diff != "" {
				t.Errorf("observation mutated policy:\n%s", diff)
			}
		})
	}
}

func TestObservationStateAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name           string
		ready, expired bool
		wantLoads      []observationLoad
		serving        int
	}{
		{name: "unready state", wantLoads: []observationLoad{}},
		{name: "scoped drain still serving after requested revocation", ready: true, wantLoads: []observationLoad{{Jobs: 1, Demand: 3}, {Jobs: 2, Demand: 6}, {}}, serving: 1},
		{name: "expiry fallback while published", ready: true, expired: true, wantLoads: []observationLoad{{Jobs: 2, Demand: 5}, {Jobs: 1, Demand: 4}, {}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var manager *SnapshotManager
			source := observationRecords{}
			if tc.ready {
				var override dispatcherv1.DispatchOverride
				manager, override = observationFixture(t, tc.expired)
				revoked := metav1.NewTime(observationNow())
				override.Spec.RevokedAt, override.Spec.RevokedBy = &revoked, "operator"
				override.Status.State = dispatcherv1.OverrideStateActive
				override.Status.History = []dispatcherv1.DispatchAuditEvent{{Actor: "operator", Message: "approved"}}
				source.records = []dispatcherv1.DispatchOverride{override}
			}
			server := NewObservationServer(manager, source, observationToken)
			server.now = observationNow
			response := observationRequest(server, "GET", "/observe/v1/state", "Bearer read-token")
			var state observationState
			if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			loads := make([]observationLoad, 0, len(state.Clusters))
			for _, cluster := range state.Clusters {
				loads = append(loads, cluster.observationLoad)
			}
			if response.Code != 200 || state.Ready != tc.ready || !cmp.Equal(tc.wantLoads, loads) {
				t.Fatalf("incorrect state: %s", response.Body.String())
			}
			if tc.ready && (state.Jobs != 3 || state.Demand != 9 || state.Clusters[0].ConfiguredCapacity != 100 || !state.Clusters[2].Blocked || state.Clusters[2].Provider != "gcp") {
				t.Errorf("incorrect inventory/totals: %#v", state)
			}
			response = observationRequest(server, "GET", "/observe/v1/overrides", "Bearer read-token")
			var overrides observationOverrides
			if err := json.Unmarshal(response.Body.Bytes(), &overrides); err != nil {
				t.Fatal(err)
			}
			if response.Code != 200 || overrides.Ready != tc.ready {
				t.Fatalf("incorrect override response: %s", response.Body.String())
			}
			if tc.ready {
				row := overrides.Overrides[0]
				if !row.Published || row.ServingJobs != tc.serving {
					t.Errorf("incorrect publication: %#v", row)
				}
				if diff := cmp.Diff(source.records[0], row.Record); diff != "" {
					t.Errorf("durable record changed:\n%s", diff)
				}
			}
		})
	}
}

func TestObservationLoadOutsideInventory(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inventory ClusterMap
		known     bool
	}{
		{name: "manual cluster absent from inventory"},
		{name: "configured manual cluster", inventory: ClusterMap{"manual": {Capacity: 10}}, known: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := &PolicySnapshot{Inventory: tc.inventory, Assignments: map[string]SnapshotAssignment{"manual-job": {Cluster: "manual", Demand: 2}}}
			got := observeState(snapshot, observationNow())
			if got.Jobs != 1 || got.Demand != 2 || len(got.Clusters) != 1 || got.Clusters[0].Name != "manual" || got.Clusters[0].InventoryKnown != tc.known || got.Clusters[0].observationLoad != got.observationLoad {
				t.Fatalf("placement totals omitted manual cluster: %#v", got)
			}
		})
	}
}
