package dispatcher

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSnapshotServerReadinessAndGenerationResponse(t *testing.T) {
	manager := NewSnapshotManager("")
	server := NewSnapshotServer(&Prowjobs{data: map[string]ProwJobData{
		"before-publish":        {Cluster: "build02"},
		"missing-from-snapshot": {Cluster: "build03"},
	}}, NewEphemeralClusterDispatcher(nil), func(bool) {}, manager)
	notReady := httptest.NewRecorder()
	server.ReadyHandler(notReady, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if notReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty server reported ready: %d", notReady.Code)
	}
	assertSchedulingResponse(t, server, "before-publish", "build02", "legacy-gob-fallback")
	snapshot, _, err := CompileSnapshot(CompileInput{
		Baseline:  map[string]ProwJobData{"job": {Cluster: "build01"}},
		Inventory: ClusterMap{"build01": {Provider: "aws", Capacity: 100}}, Generation: 7, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Publish(snapshot); err != nil {
		t.Fatal(err)
	}
	ready := httptest.NewRecorder()
	server.ReadyHandler(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("published server did not report ready: %d", ready.Code)
	}
	body, _ := json.Marshal(SchedulingRequest{Job: "job"})
	response := httptest.NewRecorder()
	server.RequestHandler(response, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
	if response.Code != http.StatusOK || response.Header().Get("X-Dispatcher-Policy-Generation") != "7" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected scheduling response %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var scheduling SchedulingResponse
	if err := json.Unmarshal(response.Body.Bytes(), &scheduling); err != nil {
		t.Fatal(err)
	}
	if scheduling.Cluster != "build01" || scheduling.PolicyGeneration != 7 || scheduling.Source != "baseline" {
		t.Fatalf("unexpected scheduling decision: %#v", scheduling)
	}
	if scheduling.ValidUntil != nil || bytes.Contains(response.Body.Bytes(), []byte(`"validUntil"`)) {
		t.Fatalf("baseline response contains an override deadline: %s", response.Body.String())
	}
	assertSchedulingResponse(t, server, "missing-from-snapshot", "build03", "legacy-gob-fallback")
}

func assertSchedulingResponse(t *testing.T, server *Server, job, cluster, source string) {
	t.Helper()
	body, err := json.Marshal(SchedulingRequest{Job: job})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.RequestHandler(recorder, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("job %q returned %d: %s", job, recorder.Code, recorder.Body.String())
	}
	var response SchedulingResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Cluster != cluster || response.Source != source {
		t.Fatalf("job %q returned %#v, want cluster %q source %q", job, response, cluster, source)
	}
}
