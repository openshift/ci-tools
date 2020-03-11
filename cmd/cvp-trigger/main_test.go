package main

import "testing"

func TestValidateOptions(t *testing.T) {
	testcases := []struct {
		description string
		opts        options
		expectError bool
	}{
		{
			description: "valid options",
			opts: options{
				prowConfigPath: "not/empty",
				jobConfigPath:  "also/not/empty",
				periodic:       "some-job",
			},
		},
		{
			description: "missing prow configuration",
			opts: options{
				jobConfigPath: "not/empty",
				periodic:      "some-job",
			},
			expectError: true,
		},
		{
			description: "missing prow job configuration",
			opts: options{
				prowConfigPath: "not/empty",
				periodic:       "some-job",
			},
			expectError: true,
		},
		{
			description: "missing job",
			opts: options{
				prowConfigPath: "not/empty",
				jobConfigPath:  "also/not/empty",
			},
			expectError: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.description, func(t *testing.T) {
			err := tc.opts.validate()
			if err == nil && tc.expectError {
				t.Errorf("%s: expected error, got nil", tc.description)
			}
			if err != nil && !tc.expectError {
				t.Errorf("%s: unexpected validation error: %v", tc.description, err)
			}
		})
	}
}
