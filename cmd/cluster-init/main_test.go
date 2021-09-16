package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestValidateOptions(t *testing.T) {
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("couldn't obtain working directory")
	}
	testdata := filepath.Join(workingDir, "testdata")
	testCases := []struct {
		name string
		options
		expectedErrors []error
	}{
		{
			name: "valid",
			options: options{
				clusterName: "newcluster",
				releaseRepo: testdata,
			},
		},
		{
			name: "allow for missing cluster name",
			options: options{
				clusterName: "",
				releaseRepo: testdata,
			},
		},
		{
			name: "invalid cluster name",
			options: options{
				clusterName: "new cluster",
				releaseRepo: testdata,
			},
			expectedErrors: []error{errors.New("--cluster-name must not contain whitespace")},
		},
		{
			name: "missing release repo",
			options: options{
				clusterName: "newcluster",
				releaseRepo: "",
			},
			expectedErrors: []error{errors.New("--release-repo must be provided")},
		},
		{
			name: "cluster exists",
			options: options{
				clusterName: "existingCluster",
				releaseRepo: testdata,
			},
			expectedErrors: []error{
				errors.New("build farm directory: existingCluster already exists"),
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateOptions(tc.options)
			if diff := cmp.Diff(tc.expectedErrors, errs, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("errs do not match expectedErrors, diff: %s", diff)
			}

		})
	}
}
