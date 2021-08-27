package main

import (
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
)

func TestUpdateSanitizeProwJobs(t *testing.T) {
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
	}
	for _, tc := range testCases {
		var tempConfigFile string
		t.Run(tc.name, func(t *testing.T) {
			sanitizeProwJobsDir := filepath.Join(testdata, "core-services", "sanitize-prow-jobs")
			src := filepath.Join(sanitizeProwJobsDir, "config.yaml")
			srcFD, err := os.Open(src)
			if err != nil {
				t.Fatalf("couldn't open config file")
			}
			tempConfigFile = filepath.Join(sanitizeProwJobsDir, "_config.yaml")
			destFD, err := os.Create(tempConfigFile)
			if err != nil {
				t.Fatalf("couldn't create temp config file")
			}
			_, err = io.Copy(destFD, srcFD)
			if err != nil {
				t.Fatalf("couldn't copy to temp config file")
			}
			if err = updateSanitizeProwJobs(tc.options); err != tc.expectedError {
				t.Fatalf("expected error: %v received error: %v", tc.expectedError, err)
			}

			configOut, _ := ioutil.ReadFile(tempConfigFile)
			expectedOut, _ := ioutil.ReadFile(filepath.Join(sanitizeProwJobsDir, "config_expected.yaml"))
			if diff := cmp.Diff(expectedOut, configOut); diff != "" {
				t.Fatalf("expected config does not match generated config: %s", diff)
			}
		})

		t.Cleanup(func() {
			if err := os.Remove(tempConfigFile); err != nil {
				t.Fatalf("error removing output config file: %v", err)
			}
		})
	}
}

func TestUpdateConfig(t *testing.T) {
	testCases := []struct {
		name        string
		clusterName string
		input       dispatcher.Config
		expected    dispatcher.Config
	}{
		{
			name:        "update config",
			clusterName: "newcluster",
			input: dispatcher.Config{
				Groups: dispatcher.JobGroups{
					api.ClusterAPPCI: dispatcher.Group{
						Jobs: []string{
							"pull-ci-openshift-release-master-build01-dry",
							"branch-ci-openshift-release-master-build01-apply",
							"periodic-openshift-release-master-build01-apply"}}},
			},
			expected: dispatcher.Config{
				Groups: dispatcher.JobGroups{
					api.ClusterAPPCI: dispatcher.Group{
						Jobs: []string{
							"pull-ci-openshift-release-master-build01-dry",
							"branch-ci-openshift-release-master-build01-apply",
							"periodic-openshift-release-master-build01-apply",
							"pull-ci-openshift-release-master-newcluster-dry",
							"branch-ci-openshift-release-master-newcluster-apply",
							"periodic-openshift-release-master-newcluster-apply"}}},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			updateConfig(&tc.input, tc.clusterName)
			if diff := cmp.Diff(tc.expected, tc.input); diff != "" {
				t.Fatalf("expected jobs were different than results: %s", diff)
			}
		})
	}
}
