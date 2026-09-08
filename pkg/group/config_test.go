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
		{
			name:        "a secret collection cannot be listed twice for the same group",
			file:        filepath.Join("testdata", "TestLoadConfig", "duplicate_secret_collection.yaml"),
			expectedErr: fmt.Errorf("failed to validate config file: secret collection 'wildfly-charts-secrets' is listed more than once for group 'test-platform-gsm-secrets-owners' in the configuration file"),
		},
		{
			name:        "an updater service account can only be requested for a collection the group owns",
			file:        filepath.Join("testdata", "TestLoadConfig", "updater_sa_not_a_collection.yaml"),
			expectedErr: fmt.Errorf("failed to validate config file: group 'test-platform-gsm-secrets-owners' requests an updater service account for 'not-mine', which is not one of its secret collections"),
		},
		{
			name:        "a collection cannot be listed twice under updater_service_accounts",
			file:        filepath.Join("testdata", "TestLoadConfig", "duplicate_updater_sa.yaml"),
			expectedErr: fmt.Errorf("failed to validate config file: secret collection 'test-platform-infra' is listed more than once under updater_service_accounts for group 'test-platform-gsm-secrets-owners' in the configuration file"),
		},
		{
			name:        "an unclaimed group cannot request updater service accounts",
			file:        filepath.Join("testdata", "TestLoadConfig", "unclaimed_with_updater_sa.yaml"),
			expectedErr: fmt.Errorf("failed to validate config file: unclaimed group 'test-platform-gsm-unclaimed-secrets' cannot request updater service accounts"),
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
