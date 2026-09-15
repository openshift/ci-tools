package prowconfigutils

import (
	"fmt"

	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/load"
)

// ApplyJobQueueConfig replaces Plank's job queue capacities with the
// authoritative job queue configuration.
func ApplyJobQueueConfig(config *prowconfig.Config, jobQueuePath string) error {
	if jobQueuePath == "" {
		return nil
	}

	queues, err := load.JobQueues(jobQueuePath)
	if err != nil {
		return fmt.Errorf("load job queue configuration: %w", err)
	}

	capacities := make(map[string]int, len(queues.JobQueues))
	for name, queue := range queues.JobQueues {
		capacities[name] = queue.Capacity
	}
	config.Plank.JobQueueCapacities = capacities

	return nil
}
