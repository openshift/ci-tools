package group

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestLoadConfig(t *testing.T) {
	testCases := []struct {
		name        string
		file        string
		expected    *Config
		expectedErr error
	}{
		{
			name: "base case: resolved",
			file: filepath.Join("testdata", "TestLoadConfig", "base_case.yaml"),
			expected: &Config{
				ClusterGroups: map[string][]string{"dp-managed": {"build01", "build02"}},
				Groups: map[string]Target{
					"old-group-name": {RenameTo: "new-group-name", Clusters: []string{"arm01"}, ClusterGroups: []string{"dp-managed"}},
					"some-group":     {},
				},
			},
		},
		{
			name:        "cannot use the group name openshift-priv-admins",
			file:        filepath.Join("testdata", "TestLoadConfig", "openshift_priv_admins.yaml"),
			expectedErr: fmt.Errorf("failed to validate config file: cannot use the group name openshift-priv-admins in the configuration file"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualErr := LoadConfig(tc.file)
			if diff := cmp.Diff(tc.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
			if actualErr == nil {
				if diff := cmp.Diff(tc.expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					t.Errorf("%s differs from expected:\n%s", tc.name, diff)
				}
			}
		})
	}
}
