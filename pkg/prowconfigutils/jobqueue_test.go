package prowconfigutils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	prowconfig "sigs.k8s.io/prow/pkg/config"
)

func TestApplyJobQueueConfig(t *testing.T) {
	testCases := []struct {
		name              string
		jobQueueConfigDir func(t *testing.T) string
		existing          map[string]int
		expected          map[string]int
	}{
		{
			name:              "empty directory leaves capacities unchanged",
			jobQueueConfigDir: func(t *testing.T) string { return "" },
			existing:          map[string]int{"existing": 5},
			expected:          map[string]int{"existing": 5},
		},
		{
			name: "definitions replace capacities",
			jobQueueConfigDir: func(t *testing.T) string {
				root := t.TempDir()
				path := filepath.Join(root, "team", "job-queues.yaml")
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatalf("create queue directory: %v", err)
				}
				queueConfig := "job_queues:\n  shiftstack:\n    capacity: 10\n    description: Shiftstack leases\n  paused:\n    capacity: 0\n    description: Paused queue\n"
				if err := os.WriteFile(path, []byte(queueConfig), 0644); err != nil {
					t.Fatalf("write queue configuration: %v", err)
				}
				return root
			},
			existing: map[string]int{"stale": 5},
			expected: map[string]int{"shiftstack": 10, "paused": 0},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &prowconfig.Config{ProwConfig: prowconfig.ProwConfig{Plank: prowconfig.Plank{
				JobQueueCapacities: tc.existing,
			}}}
			if err := ApplyJobQueueConfig(config, tc.jobQueueConfigDir(t)); err != nil {
				t.Fatalf("apply job queue configuration: %v", err)
			}
			if diff := cmp.Diff(tc.expected, config.Plank.JobQueueCapacities); diff != "" {
				t.Errorf("capacities differ (-want +got):\n%s", diff)
			}
		})
	}
}
