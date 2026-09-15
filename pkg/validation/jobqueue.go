package validation

import (
	"fmt"

	"github.com/openshift/ci-tools/pkg/api"
)

// ValidateJobQueueReferences verifies that every configured queue is defined.
func ValidateJobQueueReferences(tests []api.TestStepConfiguration, queues api.JobQueueConfig) []error {
	var errors []error
	for index, test := range tests {
		if test.JobQueueName == "" {
			continue
		}
		if _, exists := queues.JobQueues[test.JobQueueName]; !exists {
			errors = append(errors, fmt.Errorf("tests[%d].job_queue_name: job queue %q is not defined", index, test.JobQueueName))
		}
	}
	return errors
}
