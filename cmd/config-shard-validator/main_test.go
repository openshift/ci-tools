package main

import (
	"errors"
	"testing"

	"k8s.io/test-infra/prow/plugins"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestValidatePath(t *testing.T) {
	testCases := []struct {
		name          string
		pathsToCheck  []pathWithConfig
		configUpdater *plugins.ConfigUpdater
		expectedError []error
	}{
		{
			name:         "default cluster alias",
			pathsToCheck: []pathWithConfig{{path: "ci-operator/other/path"}},
			configUpdater: &plugins.ConfigUpdater{
				Maps: map[string]plugins.ConfigMapSpec{
					"ci-operator/other/*": {Clusters: map[string][]string{"default": {"my-ns"}}},
				},
			},
			expectedError: []error{
				errors.New("config_updater.maps.ci-operator/other/*.clusters: Invalid value: \"default\": `default` cluster name is not allowed, a clustername must be explicitly specified"),
			},
		},
		{
			name:         "gzip not enabled for job config",
			pathsToCheck: []pathWithConfig{{path: "ci-operator/path"}},
			configUpdater: &plugins.ConfigUpdater{
				Maps: map[string]plugins.ConfigMapSpec{
					"ci-operator/*": {Clusters: map[string][]string{"non-default": {"my-ns"}}},
				},
			},
			expectedError: []error{
				errors.New("config_updater.maps.ci-operator/*.gzip: Invalid value: \"null\": field must be set to `true` for jobconfigs"),
			},
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
			err := validatePaths(tc.pathsToCheck, tc.configUpdater)
			testhelper.Diff(t, "error", err, tc.expectedError, testhelper.EquateErrorMessage)
		})
	}
}
