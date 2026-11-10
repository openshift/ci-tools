package dispatcher

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowjobv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/scheduler/strategy"

	"github.com/openshift/ci-tools/pkg/controller/ephemeralcluster"
)

func TestServerRequestHandler(t *testing.T) {
	const template = "release-openshift-release-analysis-aggregator"
	const generated = "aggregated-aws-ovn-upgrade-5.1-micro-fips-" + template
	for _, tc := range []struct {
		name       string
		job        string
		legacyOnly bool
		missing    bool
		exact      string
		override   bool
		expired    bool
		want       string
	}{
		{name: "real generated identity", job: generated, want: "build01"},
		{name: "other verification prefix", job: "custom-verification-" + template, want: "build01"},
		{name: "canonical unchanged", job: template, want: "build01"},
		{name: "ordinary unchanged", job: "ordinary", want: "build03"},
		{name: "snapshot exact wins", job: generated, exact: "snapshot", override: true, want: "build03"},
		{name: "original Gob before canonical snapshot", job: generated, exact: "gob", override: true, want: "build03"},
		{name: "legacy canonical lookup", job: generated, legacyOnly: true, want: "build01"},
		{name: "legacy exact wins", job: generated, legacyOnly: true, exact: "gob", want: "build03"},
		{name: "canonical Gob after snapshot miss", job: generated, missing: true, exact: "canonical-gob", want: "build01"},
		{name: "unknown", job: "unknown"},
		{name: "wrong suffix", job: generated + "-custom"},
		{name: "empty prefix", job: "-" + template},
		{name: "missing canonical", job: generated, missing: true},
		{name: "canonical override metadata", job: generated, override: true, want: "build02"},
		{name: "expired canonical override", job: generated, override: true, expired: true, want: "build01"},
		{name: "ephemeral path first", job: ephemeralcluster.ProwJobNamePrefix + generated, want: "ephemeral"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseline := map[string]ProwJobData{"ordinary": {Cluster: "build03"}}
			legacy := map[string]ProwJobData{}
			if !tc.missing {
				baseline[template] = ProwJobData{Cluster: "build01"}
			}
			if tc.exact == "snapshot" {
				baseline[tc.job] = ProwJobData{Cluster: "build03"}
			}
			if tc.exact == "gob" {
				legacy[tc.job] = ProwJobData{Cluster: "build03"}
			}
			if tc.exact == "canonical-gob" || tc.legacyOnly {
				legacy[template] = ProwJobData{Cluster: "build01"}
			}
			manager := NewSnapshotManager("")
			snapshot, _, err := CompileSnapshot(CompileInput{Baseline: baseline, Inventory: ClusterMap{
				"build01": {Provider: "aws", Capacity: 100}, "build02": {Provider: "aws", Capacity: 100}, "build03": {Provider: "aws", Capacity: 100},
			}, Generation: 7, Now: time.Unix(100, 0)})
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
			if tc.expired {
				deadline = time.Unix(1, 0)
			}
			if tc.override {
				snapshot.Assignments[template] = SnapshotAssignment{Cluster: "build02", BaselineCluster: "build01", OverrideID: "canonical-override", ValidUntil: deadline, Explanation: "canonical policy"}
				snapshot.Checksum, err = snapshotChecksum(snapshot)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := manager.Publish(snapshot); err != nil {
				t.Fatal(err)
			}
			if tc.legacyOnly {
				manager = nil
			}
			server := NewSnapshotServer(&Prowjobs{data: legacy}, NewEphemeralClusterDispatcher([]string{"ephemeral"}), nil, manager)
			body, err := json.Marshal(SchedulingRequest{Job: tc.job})
			if err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			server.RequestHandler(recorder, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
			if tc.want == "" {
				if recorder.Code != http.StatusNotFound {
					t.Fatalf("want 404, got %d: %s", recorder.Code, recorder.Body.String())
				}
				return
			}
			var response SchedulingResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("status %d: %v", recorder.Code, err)
			}
			if recorder.Code != http.StatusOK || response.Cluster != tc.want {
				t.Fatalf("status %d response %+v, want %s", recorder.Code, response, tc.want)
			}
			if tc.override && tc.exact == "" {
				if response.PolicyGeneration != 7 || response.PolicyDigest != snapshot.InputDigest || recorder.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("lost policy metadata: %+v headers %v", response, recorder.Header())
				}
				if tc.expired {
					if response.Source != "expired-override-fallback" || response.OverrideID != "" || response.ValidUntil != nil {
						t.Fatalf("lost expiry semantics: %+v", response)
					}
				} else if response.Source != "runtime-override" || response.OverrideID != "canonical-override" || response.ValidUntil == nil || !response.ValidUntil.Equal(deadline) || response.Explanation != "canonical policy" {
					t.Fatalf("lost override metadata: %+v", response)
				}
			}
		})
	}
}

func TestExternalSchedulerDefaultTemplate(t *testing.T) {
	for _, tc := range []struct{ name, job, want string }{
		{name: "generated Spec.Job resolves", job: "aggregated-aws-ovn-upgrade-5.1-micro-fips-release-openshift-release-analysis-aggregator", want: "build01"},
		{name: "unknown Spec.Job uses Prow fallback", job: "verification-custom-aggregator", want: "build02"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dispatcher := NewServer(&Prowjobs{data: map[string]ProwJobData{"release-openshift-release-analysis-aggregator": {Cluster: "build01"}}}, NewEphemeralClusterDispatcher(nil), nil)
			server := httptest.NewServer(http.HandlerFunc(dispatcher.RequestHandler))
			defer server.Close()
			scheduler := strategy.NewExternal(prowconfig.ExternalScheduling{URL: server.URL, Cache: prowconfig.ExternalSchedulingCache{CleanupInterval: &metav1.Duration{Duration: time.Minute}, EntryTimeoutInterval: &metav1.Duration{Duration: time.Minute}}}, logrus.NewEntry(logrus.New()))
			result, err := scheduler.Schedule(t.Context(), &prowjobv1.ProwJob{Spec: prowjobv1.ProwJobSpec{Job: tc.job, Cluster: "build02"}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Cluster != tc.want {
				t.Fatalf("got %q, want %q", result.Cluster, tc.want)
			}
		})
	}
}

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
