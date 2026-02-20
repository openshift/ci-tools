package multi_stage

import (
	"fmt"
	"testing"

	"github.com/GoogleCloudPlatform/secrets-store-csi-driver-provider-gcp/config"
	"github.com/google/go-cmp/cmp"

	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
)

func TestGroupCredentialsByCollectionGroupAndMountPath(t *testing.T) {
	testCases := []struct {
		name        string
		credentials []api.CredentialReference
		expected    map[string][]api.CredentialReference
	}{
		{
			name:        "empty credentials",
			credentials: []api.CredentialReference{},
			expected:    map[string][]api.CredentialReference{},
		},
		{
			name: "single credential",
			credentials: []api.CredentialReference{
				{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/cred1"},
			},
			expected: map[string][]api.CredentialReference{
				"collection1:group1:/tmp/cred1": {
					{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/cred1"},
				},
			},
		},
		{
			name: "usual scenario: credentials with different collections, groups and paths",
			credentials: []api.CredentialReference{
				{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/cred1"},
				{Collection: "collection2", Group: "group2", Field: "cred2", MountPath: "/tmp/cred2"},
			},
			expected: map[string][]api.CredentialReference{
				"collection1:group1:/tmp/cred1": {
					{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/cred1"},
				},
				"collection2:group2:/tmp/cred2": {
					{Collection: "collection2", Group: "group2", Field: "cred2", MountPath: "/tmp/cred2"},
				},
			},
		},
		{
			name: "usual scenario: credentials with same collection but different group and mount paths",
			credentials: []api.CredentialReference{
				{Collection: "my-creds", Group: "aws", Field: "key1", MountPath: "/tmp/aws"},
				{Collection: "my-creds", Group: "aws", Field: "different-key", MountPath: "/tmp/aws"},
				{Collection: "my-creds", Group: "gcp", Field: "key2", MountPath: "/tmp/gcp"},
			},
			expected: map[string][]api.CredentialReference{
				"my-creds:aws:/tmp/aws": {
					{Collection: "my-creds", Group: "aws", Field: "key1", MountPath: "/tmp/aws"},
					{Collection: "my-creds", Group: "aws", Field: "different-key", MountPath: "/tmp/aws"},
				},
				"my-creds:gcp:/tmp/gcp": {
					{Collection: "my-creds", Group: "gcp", Field: "key2", MountPath: "/tmp/gcp"},
				},
			},
		},
		{
			name: "usual scenario: credentials with same collection, group and path",
			credentials: []api.CredentialReference{
				{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/shared"},
				{Collection: "collection1", Group: "group1", Field: "cred2", MountPath: "/tmp/shared"},
			},
			expected: map[string][]api.CredentialReference{
				"collection1:group1:/tmp/shared": {
					{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/shared"},
					{Collection: "collection1", Group: "group1", Field: "cred2", MountPath: "/tmp/shared"},
				},
			},
		},
		{
			name: "usual scenario: mixed grouping - some grouped together, some separate",
			credentials: []api.CredentialReference{
				{Collection: "colours", Group: "primary", Field: "red", MountPath: "/tmp/path"},
				{Collection: "colours", Group: "primary", Field: "blue", MountPath: "/tmp/path"},
				{Collection: "shapes", Group: "round", Field: "circle", MountPath: "/tmp/path"},
				{Collection: "shapes", Group: "angular", Field: "square", MountPath: "/tmp/other"},
			},
			expected: map[string][]api.CredentialReference{
				"colours:primary:/tmp/path": {
					{Collection: "colours", Group: "primary", Field: "red", MountPath: "/tmp/path"},
					{Collection: "colours", Group: "primary", Field: "blue", MountPath: "/tmp/path"},
				},
				"shapes:round:/tmp/path": {
					{Collection: "shapes", Group: "round", Field: "circle", MountPath: "/tmp/path"},
				},
				"shapes:angular:/tmp/other": {
					{Collection: "shapes", Group: "angular", Field: "square", MountPath: "/tmp/other"},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := groupCredentialsByCollectionGroupAndMountPath(tc.credentials)
			if diff := cmp.Diff(tc.expected, result); diff != "" {
				t.Errorf("groupCredentialsByCollectionGroupAndMountPath() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildGCPSecretsParameter(t *testing.T) {
	testCases := []struct {
		name        string
		credentials []api.CredentialReference
		expected    []config.Secret
	}{
		{
			name:        "empty credentials",
			credentials: []api.CredentialReference{},
			expected:    nil,
		},
		{
			name: "single credential",
			credentials: []api.CredentialReference{
				{Collection: "collection1", Group: "group1", Field: "cred1"},
			},
			expected: []config.Secret{
				{
					ResourceName: fmt.Sprintf("projects/%s/secrets/collection1__group1__cred1/versions/latest", GSMproject),
					FileName:     "cred1",
				},
			},
		},
		{
			name: "multiple credentials",
			credentials: []api.CredentialReference{
				{Collection: "collection1", Group: "group1", Field: "cred1"},
				{Collection: "collection2", Group: "group2", Field: "cred2"},
			},
			expected: []config.Secret{
				{
					ResourceName: fmt.Sprintf("projects/%s/secrets/collection1__group1__cred1/versions/latest", GSMproject),
					FileName:     "cred1",
				},
				{
					ResourceName: fmt.Sprintf("projects/%s/secrets/collection2__group2__cred2/versions/latest", GSMproject),
					FileName:     "cred2",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			yamlString, err := buildGCPSecretsParameter(tc.credentials)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var actual []config.Secret
			err = yaml.Unmarshal([]byte(yamlString), &actual)
			if err != nil {
				t.Fatalf("Failed to unmarshal YAML output: %v", err)
			}

			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("buildGCPSecretsParameter() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetSPCName(t *testing.T) {
	testCases := []struct {
		name        string
		namespace   string
		credentials []api.CredentialReference
		expected    string
	}{
		{
			name:      "simple case",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/cred1"},
			},
			expected: "test-ns-37fbca68ecf3629da44421f3-spc",
		},
		{
			name:      "typical ci-operator namespace",
			namespace: "ci-op-abc123def456",
			credentials: []api.CredentialReference{
				{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/cred1"},
			},
			expected: "ci-op-abc123def456-37fbca68ecf3629da44421f3-spc",
		},
		{
			name:      "multiple credentials same collection group and mount path",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/shared"},
				{Collection: "collection1", Group: "group1", Field: "cred2", MountPath: "/tmp/shared"},
			},
			expected: "test-ns-0dac9a0bb0cfa2cd7454405a-spc",
		},
		{
			name:      "different fields produce different hash",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "colours", Group: "primary", Field: "red", MountPath: "/tmp/path"},
			},
			expected: "test-ns-57f5da70d2e3196fac95df08-spc",
		},
		{
			name:      "different groups produce different hash (aws)",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "my-creds", Group: "aws", Field: "access-key", MountPath: "/tmp/aws"},
			},
			expected: "test-ns-1347c8e8837d7e97560e7150-spc",
		},
		{
			name:      "different groups produce different hash (gcp)",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "my-creds", Group: "gcp", Field: "access-key", MountPath: "/tmp/gcp"},
			},
			expected: "test-ns-33eddd4dbdaacb84bff886db-spc",
		},
		{
			name:      "fields are sorted for deterministic hashing",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "colours", Group: "primary", Field: "blue", MountPath: "/tmp/path"},
				{Collection: "colours", Group: "primary", Field: "red", MountPath: "/tmp/path"},
			},
			expected: "test-ns-9f663213b58aa5ec3db559e4-spc",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := getSPCName(tc.namespace, tc.credentials)
			if result != tc.expected {
				t.Errorf("getSPCName() = %v, want %v", result, tc.expected)
			}
		})
	}
}

func TestCSIVolumeName(t *testing.T) {
	testCases := []struct {
		name        string
		namespace   string
		credentials []api.CredentialReference
		expected    string
	}{
		{
			name:      "simple case",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "coll1", Group: "default", Field: "field1", MountPath: "/tmp/cred1"},
			},
			expected: "test-ns-3194a3eba8e37c2a",
		},
		{
			name:      "mount path with dots",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "coll1", Group: "default", Field: "field1", MountPath: "/tmp/cred.with.dots"},
			},
			expected: "test-ns-314371647aefa581",
		},
		{
			name:      "mount path with underscores",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "coll1", Group: "default", Field: "field1", MountPath: "/tmp/cred_with_underscores"},
			},
			expected: "test-ns-4c64a633af543812",
		},
		{
			name:      "long names stay within 63 char limit",
			namespace: "long-namespace-name-within-limits",
			credentials: []api.CredentialReference{
				{Collection: "some-long-collection-name", Group: "default", Field: "field1", MountPath: "/long/mount/path/that/exceeds/kubernetes/limits"},
			},
			expected: "long-namespace-name-within-limits-057ce95edd368edd",
		},
		{
			name:      "long namespace triggers hash-only mode",
			namespace: "namespace-that-is-just-long-enough-to-trigger-truncation",
			credentials: []api.CredentialReference{
				{Collection: "collection", Group: "default", Field: "field1", MountPath: "/tmp"},
			},
			expected: "644c2cb4c1712501c5cab0651185bac2",
		},
		{
			name:      "different groups produce different volume names (aws)",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "my-creds", Group: "aws", Field: "key", MountPath: "/tmp/secrets"},
			},
			expected: "test-ns-1cb1b8a131a84b72",
		},
		{
			name:      "different groups produce different volume names (gcp)",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "my-creds", Group: "gcp", Field: "key", MountPath: "/tmp/secrets"},
			},
			expected: "test-ns-122de8ec25d37ef5",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := getCSIVolumeName(tc.namespace, tc.credentials)
			if result != tc.expected {
				t.Errorf("getCSIVolumeName() = %v, want %v", result, tc.expected)
			}
			if len(result) > KubernetesDNSLabelLimit {
				t.Errorf("getCSIVolumeName() result exceeds Kubernetes label char limit (%d chars): %v", len(result), result)
			}
		})
	}
}

func TestReplaceForbiddenSymbolsInCredentialName(t *testing.T) {
	testCases := []struct {
		name        string
		secretName  string
		expected    string
		expectError bool
	}{
		// Valid cases - letters, numbers, dashes, dots, and underscores are allowed
		{
			name:        "valid secret name with letters only",
			secretName:  "credential",
			expected:    "credential",
			expectError: false,
		},
		{
			name:        "valid secret with dashes",
			secretName:  "credential-name",
			expected:    "credential-name",
			expectError: false,
		},
		{
			name:        "valid secret with numbers",
			secretName:  "secret123",
			expected:    "secret123",
			expectError: false,
		},
		{
			name:        "valid secret with mixed case",
			secretName:  "MySecret-123",
			expected:    "MySecret-123",
			expectError: false,
		},
		// Valid cases - replacement strings should be converted to dots and underscores
		{
			name:        "secret with dot replacement should work",
			secretName:  fmt.Sprintf("%scredential", gsmvalidation.DotReplacementString),
			expected:    ".credential",
			expectError: false,
		},
		{
			name:        "secret with dot in middle should work",
			secretName:  fmt.Sprintf("name%sjson", gsmvalidation.DotReplacementString),
			expected:    "name.json",
			expectError: false,
		},
		{
			name:        "secret ending with dot should work",
			secretName:  fmt.Sprintf("credential%s", gsmvalidation.DotReplacementString),
			expected:    "credential.",
			expectError: false,
		},
		{
			name:        "secret with multiple dots should work",
			secretName:  fmt.Sprintf("%scredential%stxt%s", gsmvalidation.DotReplacementString, gsmvalidation.DotReplacementString, gsmvalidation.DotReplacementString),
			expected:    ".credential.txt.",
			expectError: false,
		},
		{
			name:        "secret with underscore replacement should work",
			secretName:  "some_credential",
			expected:    "some_credential",
			expectError: false,
		},
		{
			name:        "allcaps secret with underscores should work",
			secretName:  "AWS_ACCESS_KEY_ID",
			expected:    "AWS_ACCESS_KEY_ID",
			expectError: false,
		},
		{
			name:        "secret ending with underscore should work",
			secretName:  "credential_",
			expected:    "credential_",
			expectError: false,
		},
		{
			name:        "secret with multiple underscores should work",
			secretName:  "credential_file_",
			expected:    "credential_file_",
			expectError: false,
		},
		{
			name:        "secret with mixed dot and underscore should work",
			secretName:  fmt.Sprintf("name_file%stxt", gsmvalidation.DotReplacementString),
			expected:    "name_file.txt",
			expectError: false,
		},
		{
			name:        "secret starting with dot and ending with underscore should work",
			secretName:  fmt.Sprintf("%scredential_", gsmvalidation.DotReplacementString),
			expected:    ".credential_",
			expectError: false,
		},
		{
			name:        "secret ending with dot should work",
			secretName:  fmt.Sprintf("credential%s", gsmvalidation.DotReplacementString),
			expected:    "credential.",
			expectError: false,
		},
		{
			name:        "secret with multiple dots and underscores mixed should work",
			secretName:  fmt.Sprintf("%sconfig_file%sbackup_txt%s", gsmvalidation.DotReplacementString, gsmvalidation.DotReplacementString, gsmvalidation.DotReplacementString),
			expected:    ".config_file.backup_txt.",
			expectError: false,
		},
		{
			name:        "secret with adjacent dot and underscore should work",
			secretName:  fmt.Sprintf("name%s_file", gsmvalidation.DotReplacementString),
			expected:    "name._file",
			expectError: false,
		},
		{
			name:        "secret with underscore and dash should stay the same",
			secretName:  "some_key-secret",
			expected:    "some_key-secret",
			expectError: false,
		},
		{
			name:        "secret with slashes should work",
			secretName:  fmt.Sprintf("path%sto%ssecret", gsmvalidation.SlashReplacementString, gsmvalidation.SlashReplacementString),
			expected:    "path/to/secret",
			expectError: false,
		},
		// Invalid cases - forbidden characters that are not allowed
		{
			name:        "secret with special characters should fail validation",
			secretName:  "secret@domain.com",
			expected:    "",
			expectError: true,
		},
		{
			name:        "secret with spaces should fail validation",
			secretName:  "secret name",
			expected:    "",
			expectError: true,
		},
		{
			name:        "secret with parentheses should fail validation",
			secretName:  "secret(test)",
			expected:    "",
			expectError: true,
		},
		{
			name:        "secret with brackets should fail validation",
			secretName:  "secret[0]",
			expected:    "",
			expectError: true,
		},
		{
			name:        "secret with plus sign should fail validation",
			secretName:  "secret+test",
			expected:    "",
			expectError: true,
		},
		{
			name:        "secret with multiple invalid characters should fail validation",
			secretName:  fmt.Sprintf("some%sweird@secret%sname!", gsmvalidation.DotReplacementString, gsmvalidation.SlashReplacementString),
			expected:    "",
			expectError: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := restoreForbiddenSymbolsInSecretName(tc.secretName)

			if tc.expectError {
				if err == nil {
					t.Errorf("expected error for secret name '%s', but got none", tc.secretName)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error for secret name '%s': %v", tc.secretName, err)
				return
			}

			if result != tc.expected {
				t.Errorf("secret name is '%v', want '%v'", result, tc.expected)
			}
		})
	}
}
