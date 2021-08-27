package main

import (
	"fmt"
	"github.com/google/go-cmp/cmp"
	"github.com/openshift/ci-tools/pkg/testhelper"
	"os"
	"path/filepath"
	"testing"
)

func TestInitClusterBuildFarmDir(t *testing.T) {
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("couldn't obtain working directory")
	}
	testdata := filepath.Join(workingDir, "testdata")
	testCases := []struct {
		name string
		options
		expectedError error
	}{
		{
			name: "basic",
			options: options{
				clusterName: "newcluster",
				releaseRepo: testdata,
			},
		},
		{
			name: "symlink exists",
			options: options{
				clusterName: "existingCluster",
				releaseRepo: testdata,
			},
			expectedError: fmt.Errorf("failed to symlink common to ../common"),
		},
	}
	for _, tc := range testCases {
		buildDir := filepath.Join(testdata, "clusters", "build-clusters", tc.clusterName)
		t.Run(tc.name, func(t *testing.T) {
			err := initClusterBuildFarmDir(tc.options)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error does not match expectedError, diff: %s", diff)
			}

			if tc.expectedError == nil {
				if _, err := os.Stat(buildDir); os.IsNotExist(err) {
					t.Fatalf("build farm directory: %s was not created", buildDir)
				}
				for _, item := range []string{"common", "common_except_app.ci"} {
					expectedDest := filepath.Join("..", item)
					dest, err := os.Readlink(filepath.Join(buildDir, item))
					if err != nil || dest != expectedDest {
						t.Fatalf("item: %s was not symlinked to: %s", item, expectedDest)
					}
				}
			}
		})

		t.Cleanup(func() {
			existingClusterDir := filepath.Join(testdata, "clusters", "build-clusters", "existingCluster")
			// We should NEVER remove the existingCluster
			if existingClusterDir != buildDir {
				if err := os.RemoveAll(buildDir); err != nil {
					t.Fatalf("error removing output config file: %v", err)
				}
			}
		})
	}
}

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
			name: "missing cluster name",
			options: options{
				clusterName: "",
				releaseRepo: testdata,
			},
			expectedErrors: []error{fmt.Errorf("--cluster-name must be provided")},
		},
		{
			name: "invalid cluster name",
			options: options{
				clusterName: "new cluster",
				releaseRepo: testdata,
			},
			expectedErrors: []error{fmt.Errorf("--cluster-name must not contain whitespace")},
		},
		{
			name: "missing release repo",
			options: options{
				clusterName: "newcluster",
				releaseRepo: "",
			},
			expectedErrors: []error{fmt.Errorf("--release-repo must be provided")},
		},
		{
			name: "build farm dir exists",
			options: options{
				clusterName: "existingCluster",
				releaseRepo: testdata,
			},
			expectedErrors: []error{fmt.Errorf("build farm directory: existingCluster already exists")},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			errors := validateOptions(tc.options)
			if diff := cmp.Diff(tc.expectedErrors, errors, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("errors do not match expectedErrors, diff: %s", diff)
			}

		})
	}
}
