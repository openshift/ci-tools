package main

import (
	"strings"
	"testing"
)

func TestOptionsValidate(t *testing.T) {
	testCases := []struct {
		name        string
		options     options
		errorSubstr string
	}{
		{
			name:        "missing jobs directory",
			options:     options{configPath: "config.yaml"},
			errorSubstr: "--prow-jobs-dir",
		},
		{
			name:        "missing assignment source",
			options:     options{prowJobConfigDir: "jobs"},
			errorSubstr: "--config-path",
		},
		{
			name:    "config mode",
			options: options{prowJobConfigDir: "jobs", configPath: "config.yaml"},
		},
		{
			name:    "cluster-only mode",
			options: options{prowJobConfigDir: "jobs", configPath: "config.yaml", clusterOnly: true},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.options.validate()
			if testCase.errorSubstr == "" {
				if err != nil {
					t.Fatalf("validate() returned an unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.errorSubstr) {
				t.Fatalf("validate() error = %v, want an error containing %q", err, testCase.errorSubstr)
			}
		})
	}
}
