package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/dispatcher"
)

type dispatchTestVolumes struct {
	calls int
}

func (volumes *dispatchTestVolumes) GetJobVolumes() (map[string]float64, error) {
	volumes.calls++
	return map[string]float64{"controller": 2, "aggregated-controller": 37}, nil
}

func (volumes *dispatchTestVolumes) CalculateVolumeDistribution(dispatcher.ClusterMap) map[string]float64 {
	return map[string]float64{"build01": 39}
}

type dispatchTestPublisher struct {
	manager *dispatcher.SnapshotManager
	err     error
	calls   int
}

func (publisher *dispatchTestPublisher) publish(baseline map[string]dispatcher.ProwJobData, inventory dispatcher.ClusterMap, blocked sets.Set[string]) error {
	publisher.calls++
	if publisher.err != nil {
		return publisher.err
	}
	snapshot, _, err := dispatcher.CompileSnapshot(dispatcher.CompileInput{Baseline: baseline, Inventory: inventory, Blocked: blocked, Generation: 1, Now: time.Unix(100, 0)})
	if err != nil {
		return err
	}
	return publisher.manager.Publish(snapshot)
}

func TestFullDispatchRunnerDispatch(t *testing.T) {
	const valid = `{"verify":{"aggregated":{"aggregatedProwJob":{"analysisJobCount":7,"prowJob":{"name":"controller"}}}}}`
	const conflicting = `{"verify":{"x":{"aggregatedProwJob":{"analysisJobCount":1,"prowJob":{"name":"a-b"}}},"x-a":{"aggregatedProwJob":{"analysisJobCount":1,"prowJob":{"name":"b"}}}}}`
	publicationError := errors.New("snapshot storage unavailable")
	for _, tc := range []struct {
		name             string
		release          string
		missingAlias     bool
		force            bool
		empty            bool
		legacyMetadata   bool
		inventoryChange  bool
		existingSnapshot bool
		publicationError bool
		wantError        string
		wantGeneration   bool
		wantPublication  bool
	}{
		{name: "complete aggregate assignments publish on startup", release: valid, wantPublication: true},
		{name: "missing aggregate forces ordinary unforced generation", release: valid, missingAlias: true, wantGeneration: true, wantPublication: true},
		{name: "ordinary assignments remain no-op", release: `{"verify":{}}`, missingAlias: true, wantPublication: true},
		{name: "malformed config still publishes startup baseline", release: `{`, wantError: "unexpected end of JSON input", wantPublication: true},
		{name: "conflicting config still publishes startup baseline", release: conflicting, wantError: "references multiple templates", wantPublication: true},
		{name: "malformed config retains existing snapshot", release: `{`, existingSnapshot: true, wantError: "unexpected end of JSON input", wantPublication: true},
		{name: "load and publication errors both remain visible", release: `{`, publicationError: true, wantError: "unexpected end of JSON input", wantPublication: true},
		{name: "publication failure retains existing snapshot", release: conflicting, existingSnapshot: true, publicationError: true, wantError: "references multiple templates", wantPublication: true},
		{name: "forced generation fails closed", release: `{`, force: true, wantError: "unexpected end of JSON input"},
		{name: "empty assignments fail closed", release: conflicting, empty: true, wantError: "references multiple templates"},
		{name: "metadata migration fails closed", release: `{`, legacyMetadata: true, wantError: "unexpected end of JSON input"},
		{name: "inventory change fails closed", release: `{`, inventoryChange: true, wantError: "unexpected end of JSON input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			releaseDir := filepath.Join(directory, "releases")
			jobDir := filepath.Join(directory, "jobs")
			for _, path := range []string{releaseDir, jobDir} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			configPath := filepath.Join(directory, "dispatcher.yaml")
			clusterPath := filepath.Join(directory, "clusters.yaml")
			clusterConfig := "aws:\n- name: build01\n  capacity: 100\n"
			if tc.inventoryChange {
				clusterConfig += "- name: build02\n  capacity: 100\n"
			}
			for path, content := range map[string]string{
				configPath:  `{"default":"build01","buildFarm":{"aws":{"build01":{}}}}`,
				clusterPath: clusterConfig,
				filepath.Join(releaseDir, "release.json"):     tc.release,
				filepath.Join(jobDir, "infra-periodics.yaml"): "periodics:\n- name: controller\n  cluster: build01\n",
			} {
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			baseline := map[string]dispatcher.ProwJobData{
				"controller":            {Cluster: "build01", Demand: 2, Group: "infra-periodics.yaml"},
				"aggregated-controller": {Cluster: "build01", Demand: 37, Group: "infra-periodics.yaml"},
			}
			if tc.missingAlias {
				delete(baseline, "aggregated-controller")
			}
			if tc.empty {
				baseline = map[string]dispatcher.ProwJobData{}
			}
			if tc.legacyMetadata {
				baseline["controller"] = dispatcher.ProwJobData{Cluster: "build01"}
			}
			gobPath := filepath.Join(directory, "assignments.gob")
			if err := dispatcher.WriteGob(gobPath, baseline); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(gobPath)
			if err != nil {
				t.Fatal(err)
			}
			jobs := dispatcher.NewProwjobs(gobPath)
			manager := dispatcher.NewSnapshotManager(filepath.Join(directory, "snapshot.json"))
			publisher := &dispatchTestPublisher{manager: manager}
			if tc.existingSnapshot {
				if err := publisher.publish(baseline, dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 100}}, sets.New[string]()); err != nil {
					t.Fatal(err)
				}
				publisher.calls = 0
			}
			previousSnapshot := manager.Current()
			if tc.publicationError {
				publisher.err = publicationError
			}
			volumes := &dispatchTestVolumes{}
			ephemeral := dispatcher.NewEphemeralClusterDispatcher(nil)
			runner := fullDispatchRunner{
				options: options{configPath: configPath, clusterConfigPath: clusterPath, releaseConfigDir: releaseDir, prowJobConfigDir: jobDir, jobsStoragePath: gobPath, PrometheusOptions: dispatcher.PrometheusOptions{MinimumJobDemand: 1}},
				jobs:    jobs, volumes: volumes, ephemeral: ephemeral, publishSnapshot: publisher.publish,
			}
			err = runner.dispatch(tc.force)
			if (err != nil) != (tc.wantError != "") || err != nil && !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("dispatch() error = %v, want %q", err, tc.wantError)
			}
			if tc.publicationError && !errors.Is(err, publicationError) {
				t.Errorf("publication error lost: %v", err)
			}
			if (volumes.calls == 1) != tc.wantGeneration || volumes.calls > 1 {
				t.Errorf("demand queries = %d, want generation %t", volumes.calls, tc.wantGeneration)
			}
			if (publisher.calls == 1) != tc.wantPublication {
				t.Errorf("snapshot publications = %d, want publication %t", publisher.calls, tc.wantPublication)
			}
			if manager.Ready() != (tc.existingSnapshot || tc.wantPublication && !tc.publicationError) {
				t.Errorf("snapshot readiness = %t", manager.Ready())
			}
			cluster, ephemeralErr := ephemeral.Dispatch("ephemeral-test")
			if tc.wantPublication && (ephemeralErr != nil || cluster != "build01") || !tc.wantPublication && ephemeralErr == nil {
				t.Errorf("ephemeral initialization: cluster %q, error %v", cluster, ephemeralErr)
			}
			if tc.wantGeneration {
				baseline["aggregated-controller"] = dispatcher.ProwJobData{Cluster: "build01", Demand: 37, Group: "infra-periodics.yaml"}
			} else {
				after, err := os.ReadFile(gobPath)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(before, after); diff != "" {
					t.Errorf("no-op or failed dispatch changed durable assignments (-want +got):\n%s", diff)
				}
			}
			if diff := cmp.Diff(baseline, jobs.GetDataCopy()); diff != "" {
				t.Errorf("assignments changed (-want +got):\n%s", diff)
			}
			if tc.existingSnapshot && tc.publicationError {
				if diff := cmp.Diff(previousSnapshot, manager.Current()); diff != "" {
					t.Errorf("failed publication lost previous snapshot (-want +got):\n%s", diff)
				}
			}
			if tc.wantPublication && !tc.publicationError {
				for name := range baseline {
					if decision, found := manager.Lookup(name, time.Unix(101, 0)); !found || decision.Cluster != "build01" {
						t.Errorf("snapshot lookup %q = %#v, found %t", name, decision, found)
					}
				}
			}
		})
	}
}
