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

func TestValidateCollectionName(t *testing.T) {
	testCases := []struct {
		name          string
		collection    string
		expectedValid bool
	}{
		{
			name:          "valid collection name: lowercase letters",
			collection:    "test-collection",
			expectedValid: true,
		},
		{
			name:          "valid collection name: numbers",
			collection:    "test123",
			expectedValid: true,
		},
		{
			name:          "valid collection name: hyphens",
			collection:    "test-collection-123",
			expectedValid: true,
		},
		{
			name:          "valid collection name: multiphe hyphens",
			collection:    "test--collection",
			expectedValid: true,
		},
		{
			name:          "valid collection name: single character",
			collection:    "a",
			expectedValid: true,
		},
		{
			name:          "invalid collection name: uppercase letters",
			collection:    "Test-Collection",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: special characters",
			collection:    "test_collection",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: symbols",
			collection:    "abc!4@#$%^&*()+",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: spaces",
			collection:    "test collection",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: empty string",
			collection:    "",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: dots",
			collection:    "test.collection",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: double underscores",
			collection:    "test__collection",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: double underscores at the end",
			collection:    "testcollection__",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: double underscores at the beginning",
			collection:    "__testcollection",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: hyphen at the beginning",
			collection:    "-testcollection",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: hyphen at the end",
			collection:    "test-",
			expectedValid: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualValid := ValidateCollectionName(tc.collection)
			if actualValid != tc.expectedValid {
				t.Errorf("Expected %t, got %t for collection %q", tc.expectedValid, actualValid, tc.collection)
			}
		})
	}
}
