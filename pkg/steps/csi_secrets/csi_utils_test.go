package csi_secrets

import (
	"fmt"
	"testing"

	"github.com/GoogleCloudPlatform/secrets-store-csi-driver-provider-gcp/config"
	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	csiapi "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestGroupCredentialsByMountPath(t *testing.T) {
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
				"/tmp/cred1": {
					{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/cred1"},
				},
			},
		},
		{
			name: "usual scenario: credentials with different mount paths stay separate",
			credentials: []api.CredentialReference{
				{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/cred1"},
				{Collection: "collection2", Group: "group2", Field: "cred2", MountPath: "/tmp/cred2"},
			},
			expected: map[string][]api.CredentialReference{
				"/tmp/cred1": {
					{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/cred1"},
				},
				"/tmp/cred2": {
					{Collection: "collection2", Group: "group2", Field: "cred2", MountPath: "/tmp/cred2"},
				},
			},
		},
		{
			name: "usual scenario: same mount path groups together regardless of collection or group",
			credentials: []api.CredentialReference{
				{Collection: "my-creds", Group: "aws", Field: "key1", MountPath: "/tmp/aws"},
				{Collection: "my-creds", Group: "aws", Field: "different-key", MountPath: "/tmp/aws"},
				{Collection: "my-creds", Group: "gcp", Field: "key2", MountPath: "/tmp/gcp"},
			},
			expected: map[string][]api.CredentialReference{
				"/tmp/aws": {
					{Collection: "my-creds", Group: "aws", Field: "key1", MountPath: "/tmp/aws"},
					{Collection: "my-creds", Group: "aws", Field: "different-key", MountPath: "/tmp/aws"},
				},
				"/tmp/gcp": {
					{Collection: "my-creds", Group: "gcp", Field: "key2", MountPath: "/tmp/gcp"},
				},
			},
		},
		{
			name: "usual scenario: same collection, group and path",
			credentials: []api.CredentialReference{
				{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/shared"},
				{Collection: "collection1", Group: "group1", Field: "cred2", MountPath: "/tmp/shared"},
			},
			expected: map[string][]api.CredentialReference{
				"/tmp/shared": {
					{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/shared"},
					{Collection: "collection1", Group: "group1", Field: "cred2", MountPath: "/tmp/shared"},
				},
			},
		},
		{
			name: "usual scenario: multi-collection bundle: different collections at same path merge into one group",
			credentials: []api.CredentialReference{
				{Collection: "colours", Group: "primary", Field: "red", MountPath: "/tmp/path"},
				{Collection: "colours", Group: "primary", Field: "blue", MountPath: "/tmp/path"},
				{Collection: "shapes", Group: "round", Field: "circle", MountPath: "/tmp/path"},
				{Collection: "shapes", Group: "angular", Field: "square", MountPath: "/tmp/other"},
			},
			expected: map[string][]api.CredentialReference{
				"/tmp/path": {
					{Collection: "colours", Group: "primary", Field: "red", MountPath: "/tmp/path"},
					{Collection: "colours", Group: "primary", Field: "blue", MountPath: "/tmp/path"},
					{Collection: "shapes", Group: "round", Field: "circle", MountPath: "/tmp/path"},
				},
				"/tmp/other": {
					{Collection: "shapes", Group: "angular", Field: "square", MountPath: "/tmp/other"},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := GroupCredentialsByMountPath(tc.credentials)
			if diff := cmp.Diff(tc.expected, result); diff != "" {
				t.Errorf("GroupCredentialsByMountPath() mismatch (-want +got):\n%s", diff)
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
					ResourceName: fmt.Sprintf("projects/%s/secrets/collection1__group1__cred1/versions/latest", GSMProject),
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
					ResourceName: fmt.Sprintf("projects/%s/secrets/collection1__group1__cred1/versions/latest", GSMProject),
					FileName:     "cred1",
				},
				{
					ResourceName: fmt.Sprintf("projects/%s/secrets/collection2__group2__cred2/versions/latest", GSMProject),
					FileName:     "cred2",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			yamlString, err := BuildGCPSecretsParameter(tc.credentials)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var actual []config.Secret
			err = yaml.Unmarshal([]byte(yamlString), &actual)
			if err != nil {
				t.Fatalf("Failed to unmarshal YAML output: %v", err)
			}

			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("BuildGCPSecretsParameter() mismatch (-want +got):\n%s", diff)
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
			expected: "test-ns-413eed400e7af60b8833c3f8-spc",
		},
		{
			name:      "typical ci-operator namespace",
			namespace: "ci-op-abc123def456",
			credentials: []api.CredentialReference{
				{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/cred1"},
			},
			expected: "ci-op-abc123def456-413eed400e7af60b8833c3f8-spc",
		},
		{
			name:      "multiple credentials same mount path",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "collection1", Group: "group1", Field: "cred1", MountPath: "/tmp/shared"},
				{Collection: "collection1", Group: "group1", Field: "cred2", MountPath: "/tmp/shared"},
			},
			expected: "test-ns-3282fd6f77af324290aa5447-spc",
		},
		{
			name:      "different fields produce different hash",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "colours", Group: "primary", Field: "red", MountPath: "/tmp/path"},
			},
			expected: "test-ns-260b2418a28eeaa5a9e3a995-spc",
		},
		{
			name:      "different groups produce different hash (aws)",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "my-creds", Group: "aws", Field: "access-key", MountPath: "/tmp/aws"},
			},
			expected: "test-ns-c133d70a71fa6d3410fc8889-spc",
		},
		{
			name:      "different groups produce different hash (gcp)",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "my-creds", Group: "gcp", Field: "access-key", MountPath: "/tmp/gcp"},
			},
			expected: "test-ns-baddf8f68cf0aafe2acd532e-spc",
		},
		{
			name:      "credential keys are sorted for deterministic hashing",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "colours", Group: "primary", Field: "blue", MountPath: "/tmp/path"},
				{Collection: "colours", Group: "primary", Field: "red", MountPath: "/tmp/path"},
			},
			expected: "test-ns-cfe2f25c28f9af63ee0c82cc-spc",
		},
		{
			name:      "multi-collection credentials at same mount path produce single hash",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "col-a", Group: "grp-x", Field: "login", MountPath: "/var/bundle"},
				{Collection: "col-a", Group: "grp-x", Field: "pswd", MountPath: "/var/bundle"},
				{Collection: "col-b", Group: "grp-y", Field: "config", MountPath: "/var/bundle"},
			},
			expected: "test-ns-6f9da35a0c07b54ee330ab7b-spc",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := GetSPCName(tc.namespace, tc.credentials)
			if result != tc.expected {
				t.Errorf("GetSPCName() = %v, want %v", result, tc.expected)
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
			expected: "test-ns-a6ea5e284a092d64",
		},
		{
			name:      "mount path with dots",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "coll1", Group: "default", Field: "field1", MountPath: "/tmp/cred.with.dots"},
			},
			expected: "test-ns-b8aa56363fadb89b",
		},
		{
			name:      "mount path with underscores",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "coll1", Group: "default", Field: "field1", MountPath: "/tmp/cred_with_underscores"},
			},
			expected: "test-ns-09b39d03e50b0a50",
		},
		{
			name:      "long names stay within 63 char limit",
			namespace: "long-namespace-name-within-limits",
			credentials: []api.CredentialReference{
				{Collection: "some-long-collection-name", Group: "default", Field: "field1", MountPath: "/long/mount/path/that/exceeds/kubernetes/limits"},
			},
			expected: "long-namespace-name-within-limits-758f3a0a3e8edb80",
		},
		{
			name:      "long namespace triggers hash-only mode",
			namespace: "namespace-that-is-just-long-enough-to-trigger-truncation",
			credentials: []api.CredentialReference{
				{Collection: "collection", Group: "default", Field: "field1", MountPath: "/tmp"},
			},
			expected: "e9671acd244849c57167c658fa2f9697",
		},
		{
			name:      "same mount path produces same volume name regardless of collection/group",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "my-creds", Group: "aws", Field: "key", MountPath: "/tmp/secrets"},
			},
			expected: "test-ns-203f6fcff3e34ef1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := GetCSIVolumeName(tc.namespace, tc.credentials)
			if result != tc.expected {
				t.Errorf("GetCSIVolumeName() = %v, want %v", result, tc.expected)
			}
			if len(result) > KubernetesDNSLabelLimit {
				t.Errorf("GetCSIVolumeName() result exceeds Kubernetes label char limit (%d chars): %v", len(result), result)
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
			secretName:  fmt.Sprintf("some%sweird@secretname!", gsmvalidation.DotReplacementString),
			expected:    "",
			expectError: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := RestoreForbiddenSymbolsInSecretName(tc.secretName)

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

func TestBuildCSIVolume(t *testing.T) {
	readOnly := true
	testCases := []struct {
		name           string
		volumeName     string
		spcName        string
		expectedVolume coreapi.Volume
	}{
		{
			name:       "simple case",
			volumeName: "volume-name",
			spcName:    "spc-name",
			expectedVolume: coreapi.Volume{
				Name: "volume-name",
				VolumeSource: coreapi.VolumeSource{
					CSI: &coreapi.CSIVolumeSource{
						Driver:   "secrets-store.csi.k8s.io",
						ReadOnly: &readOnly,
						VolumeAttributes: map[string]string{
							"secretProviderClass": "spc-name",
						},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := BuildCSIVolume(tc.volumeName, tc.spcName)
			testhelper.Diff(t, tc.name, actual, tc.expectedVolume)
		})
	}
}

func TestBuildSPCsForCredentials(t *testing.T) {
	testCases := []struct {
		name        string
		namespace   string
		credentials []api.CredentialReference
		expectErr   bool
		checkSPCs   func(t *testing.T, spcs map[string]*csiapi.SecretProviderClass)
	}{
		{
			name:        "empty credentials",
			namespace:   "test-ns",
			credentials: []api.CredentialReference{},
			checkSPCs: func(t *testing.T, spcs map[string]*csiapi.SecretProviderClass) {
				if len(spcs) != 0 {
					t.Errorf("expected 0 SPCs, got %d", len(spcs))
				}
			},
		},
		{
			name:      "single credential produces volume SPC and censoring SPC",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "col", Group: "grp", Field: "key", MountPath: "/secrets"},
			},
			checkSPCs: func(t *testing.T, spcs map[string]*csiapi.SecretProviderClass) {
				if len(spcs) != 2 {
					t.Errorf("expected 2 SPCs (1 volume + 1 censor), got %d", len(spcs))
				}
				for _, spc := range spcs {
					if spc.Namespace != "test-ns" {
						t.Errorf("expected namespace test-ns, got %s", spc.Namespace)
					}
					if spc.Spec.Provider != "gcp" {
						t.Errorf("expected provider gcp, got %s", spc.Spec.Provider)
					}
				}
			},
		},
		{
			name:      "duplicate credentials are deduped in censoring SPCs",
			namespace: "test-ns",
			credentials: []api.CredentialReference{
				{Collection: "col", Group: "grp", Field: "key", MountPath: "/path1"},
				{Collection: "col", Group: "grp", Field: "key", MountPath: "/path2"},
			},
			checkSPCs: func(t *testing.T, spcs map[string]*csiapi.SecretProviderClass) {
				// 2 volume SPCs (different mount paths) + 1 censoring SPC (deduped)
				if len(spcs) != 3 {
					t.Errorf("expected 3 SPCs (2 volume + 1 censor deduped), got %d", len(spcs))
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			spcs, err := BuildSPCsForCredentials(tc.namespace, tc.credentials)
			if tc.expectErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.checkSPCs(t, spcs)
		})
	}
}
