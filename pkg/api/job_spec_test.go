package api

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/test-infra/prow/pod-utils/downwardapi"
)

func TestJobNameHash(t *testing.T) {
	testCases := []struct {
		name     string
		jobSpec  JobSpec
		expected string
	}{
		{
			name: "basic",
			jobSpec: JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job: "some-job",
				},
			},
			expected: "21d89",
		},
		{
			name: "target additional suffix supplied",
			jobSpec: JobSpec{
				JobSpec: downwardapi.JobSpec{
					Job: "some-job",
				},
				TargetAdditionalSuffix: "1",
			},
			expected: "02c64",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jobNameHash := tc.jobSpec.JobNameHash()
			if diff := cmp.Diff(jobNameHash, tc.expected); diff != "" {
				t.Fatalf("jobNameHash doesn't match expected, diff: %s", diff)
			}
		})
	}
}
