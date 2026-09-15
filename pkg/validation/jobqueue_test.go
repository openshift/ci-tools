package validation

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestContextFreeValidationDefersJobQueueReferences(t *testing.T) {
	config := &api.ReleaseBuildConfiguration{
		Resources: api.ResourceConfiguration{"*": {Requests: api.ResourceList{"cpu": "1"}}},
		Tests: []api.TestStepConfiguration{{
			As:                         "unit",
			Commands:                   "commands",
			ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
			JobQueueName:               "validated-by-repository-loader",
		}},
	}
	if err := IsValidConfiguration(config, "org", "repo"); err != nil {
		t.Fatalf("context-free validation must defer job queue references: %v", err)
	}
}

func TestValidatorUsesJobQueueDefinitions(t *testing.T) {
	queues := api.JobQueueConfig{JobQueues: map[string]api.JobQueue{
		"known": {Capacity: 1, Description: "Known queue"},
	}}
	validator := NewValidator(nil, nil, &queues)
	config := &api.ReleaseBuildConfiguration{Tests: []api.TestStepConfiguration{{
		As:                         "unit",
		Commands:                   "commands",
		ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "ignored"},
		JobQueueName:               "missing",
	}}}

	actual := validator.ValidateTestStepConfiguration(NewConfigContext(), config, false)
	expected := []error{errString(`tests[0].job_queue_name: job queue "missing" is not defined`)}
	if diff := cmp.Diff(expected, actual, testhelper.EquateErrorMessage); diff != "" {
		t.Errorf("errors differ (-want +got):\n%s", diff)
	}
}

func TestValidateJobQueueReferences(t *testing.T) {
	queues := api.JobQueueConfig{JobQueues: map[string]api.JobQueue{
		"known": {Capacity: 1, Description: "Known queue"},
	}}
	testCases := []struct {
		name     string
		tests    []api.TestStepConfiguration
		queues   api.JobQueueConfig
		expected []error
	}{
		{name: "no tests"},
		{name: "queue unset", tests: []api.TestStepConfiguration{{As: "e2e"}}},
		{name: "known queue", tests: []api.TestStepConfiguration{{As: "e2e", JobQueueName: "known"}}, queues: queues},
		{
			name:     "unknown queue",
			tests:    []api.TestStepConfiguration{{As: "e2e", JobQueueName: "missing"}},
			queues:   queues,
			expected: []error{errString(`tests[0].job_queue_name: job queue "missing" is not defined`)},
		},
		{
			name:     "empty registry",
			tests:    []api.TestStepConfiguration{{As: "e2e", JobQueueName: "known"}},
			expected: []error{errString(`tests[0].job_queue_name: job queue "known" is not defined`)},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := ValidateJobQueueReferences(tc.tests, tc.queues)
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("errors differ (-want +got):\n%s", diff)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
