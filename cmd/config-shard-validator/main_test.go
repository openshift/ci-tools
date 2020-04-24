package main

import (
	"testing"

	"k8s.io/test-infra/prow/plugins"
)

func TestValidatePath(t *testing.T) {
	testCases := []struct {
		name          string
		pathsToCheck  []pathWithConfig
		configUpdater *plugins.ConfigUpdater
		expectedError string
	}{
		{
			name:         "default cluster alias",
			pathsToCheck: []pathWithConfig{{path: "ci-operator/other/path"}},
			configUpdater: &plugins.ConfigUpdater{
				Maps: map[string]plugins.ConfigMapSpec{
					"ci-operator/other/*": {Clusters: map[string][]string{"default": {"my-ns"}}},
				},
			},
			expectedError: "ci-operator/other/path.config_updater.maps.ci-operator/other/*.clusters: Invalid value: \"default\": `default` cluster name is not allowed, a clustername must be explicitly specified",
		},
		{
			name:         "gzip not enabled for job config",
			pathsToCheck: []pathWithConfig{{path: "ci-operator/path"}},
			configUpdater: &plugins.ConfigUpdater{
				Maps: map[string]plugins.ConfigMapSpec{
					"ci-operator/*": {Clusters: map[string][]string{"non-default": {"my-ns"}}},
				},
			},
			expectedError: "ci-operator/path.config_updater.maps.ci-operator/*.gzip: Invalid value: \"null\": field must be set to `true` for jobconfigs",
		},
		{
			name:         "happy path",
			pathsToCheck: []pathWithConfig{{path: "ci-operator/other/path"}},
			configUpdater: &plugins.ConfigUpdater{
				Maps: map[string]plugins.ConfigMapSpec{
					"ci-operator/other/*": {Clusters: map[string][]string{"non-default": {"my-ns"}}},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			errMsg := ""
			err := validatePaths(tc.pathsToCheck, tc.configUpdater)
			if err != nil {
				errMsg = err.Error()
			}
			if errMsg != tc.expectedError {
				t.Errorf("expected error %s got error %s", tc.expectedError, err)
			}
		})
	}
}
