package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openshift/ci-tools/pkg/dispatcher"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestAffinityPrevious(t *testing.T) {
	testCases := []struct {
		name     string
		cluster  string
		prior    string
		snapshot string
		wantErr  string
	}{
		{name: "bootstrap current fallback", cluster: "build01", prior: "build01"},
		{name: "new job keeps existing fallback", cluster: "build01"},
		{name: "unresolved fallback rejected", wantErr: "explicit configured fallback"},
		{name: "divergent old baseline rejected", cluster: "build01", prior: "build02", wantErr: "differs from preserved baseline"},
		{name: "divergent override cache rejected", cluster: "build01", snapshot: `{"assignments":{"job":{"cluster":"build02"}}}`, wantErr: "cached runtime host"},
		{name: "invalid bootstrap evidence rejected", cluster: "build01", snapshot: `{}`, wantErr: "no assignments"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			jobs := filepath.Join(root, "jobs")
			if err := os.Mkdir(jobs, 0755); err != nil {
				t.Fatal(err)
			}
			data := "periodics:\n- name: job\n  cluster: " + tc.cluster + "\n"
			if err := os.WriteFile(filepath.Join(jobs, "jobs.yaml"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "affinity.json")
			store, err := dispatcher.OpenAffinityStore(path)
			if err != nil {
				t.Fatal(err)
			}
			previous := map[string]dispatcher.ProwJobData{}
			if tc.prior != "" {
				previous["job"] = dispatcher.ProwJobData{Cluster: tc.prior, Demand: 9}
			}
			snapshot := filepath.Join(root, "snapshot.json")
			if tc.snapshot != "" {
				if err := os.WriteFile(snapshot, []byte(tc.snapshot), 0600); err != nil {
					t.Fatal(err)
				}
			}
			inventory := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 100}, "build02": {Provider: "aws", Capacity: 100}}
			got, err := affinityPrevious(store, jobs, orderingConfig(), inventory, previous, 1, snapshot)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("failed bootstrap persisted affinity: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got["job"].Cluster != tc.cluster || got["job"].Affinity.Cluster != tc.cluster || !got["job"].PlacementKnown {
				t.Fatalf("invalid frozen assignment: %#v", got["job"])
			}
			if tc.prior != "" && got["job"].Demand != 9 {
				t.Fatalf("lost historical demand: %#v", got["job"])
			}
			reopened, err := dispatcher.OpenAffinityStore(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(jobs, "jobs.yaml"), filepath.Join(jobs, "renamed.yaml")); err != nil {
				t.Fatal(err)
			}
			data += "- name: sibling\n  cluster: " + tc.cluster + "\n"
			if err := os.WriteFile(filepath.Join(jobs, "renamed.yaml"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			renamed, err := affinityPrevious(reopened, jobs, orderingConfig(), inventory, got, 1, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if renamed["job"].Affinity != got["job"].Affinity || renamed["sibling"].Affinity != got["job"].Affinity {
				t.Fatal("rename or new sibling lost durable group affinity")
			}
			if err := dispatchDeltaJobs(jobs, orderingConfig(), sets.New[string](), renamed, inventory, 1); err != nil {
				t.Fatal(err)
			}
			if renamed["sibling"].Affinity != got["job"].Affinity {
				t.Fatal("delta refresh lost affinity")
			}
		})
	}
}

func TestValidateAffinityOptions(t *testing.T) {
	testCases := []struct {
		name    string
		options options
		wantErr bool
	}{
		{name: "existing weekly mode"},
		{name: "durable refresh", options: options{affinityStoragePath: "affinity.json", rebalanceInterval: time.Hour}},
		{name: "negative interval", options: options{rebalanceInterval: -time.Second}, wantErr: true},
		{name: "too frequent", options: options{affinityStoragePath: "affinity.json", rebalanceInterval: time.Minute}, wantErr: true},
		{name: "unguarded refresh", options: options{rebalanceInterval: time.Hour}, wantErr: true},
		{name: "state path collision", options: options{affinityStoragePath: "jobs.gob", jobsStoragePath: "jobs.gob"}, wantErr: true},
		{name: "implicit snapshot collision", options: options{affinityStoragePath: "jobs.gob.snapshot.json", jobsStoragePath: "jobs.gob"}, wantErr: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateAffinityOptions(tc.options); (err != nil) != tc.wantErr {
				t.Errorf("error = %v, want error %t", err, tc.wantErr)
			}
		})
	}
}
