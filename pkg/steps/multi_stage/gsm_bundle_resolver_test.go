package multi_stage

import (
	"context"
	"errors"
	"testing"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/google/go-cmp/cmp"
	"github.com/googleapis/gax-go/v2"

	"github.com/openshift/ci-tools/pkg/api"
	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

// fakeGSMClient is a test double that should never be called
// (discoveredFields cache should prevent actual API calls)
type fakeGSMClient struct{}

func (f *fakeGSMClient) ListSecrets(ctx context.Context, req *secretmanagerpb.ListSecretsRequest,
	opts ...gax.CallOption) *secretmanager.SecretIterator {
	panic("ListSecrets should not be called when cache is populated")
}

func (f *fakeGSMClient) AccessSecretVersion(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	panic("AccessSecretVersion not implemented in test")
}

func (f *fakeGSMClient) GetSecret(ctx context.Context, req *secretmanagerpb.GetSecretRequest, opts ...gax.CallOption) (*secretmanagerpb.Secret, error) {
	panic("GetSecret not implemented in test")
}

func (f *fakeGSMClient) CreateSecret(ctx context.Context, req *secretmanagerpb.CreateSecretRequest, opts ...gax.CallOption) (*secretmanagerpb.Secret, error) {
	panic("CreateSecret not implemented in test")
}

func (f *fakeGSMClient) DeleteSecret(ctx context.Context, req *secretmanagerpb.DeleteSecretRequest, opts ...gax.CallOption) error {
	panic("DeleteSecret not implemented in test")
}

func (f *fakeGSMClient) AddSecretVersion(ctx context.Context, req *secretmanagerpb.AddSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.SecretVersion,
	error) {
	panic("AddSecretVersion not implemented in test")
}

func TestResolveCredentialReferences(t *testing.T) {
	client := &fakeGSMClient{}

	testCases := []struct {
		name             string
		credentials      []api.CredentialReference
		gsmConfig        *api.GSMConfig
		discoveredFields map[collectionGroupKey][]string
		expected         []api.CredentialReference
		expectedError    error
	}{
		{
			name: "explicit field - pass through unchanged",
			credentials: []api.CredentialReference{
				{
					Collection: "my-creds",
					Group:      "aws",
					Field:      "access-key",
					MountPath:  "/tmp/aws",
				},
			},
			gsmConfig: &api.GSMConfig{},
			expected: []api.CredentialReference{
				{
					Collection: "my-creds",
					Group:      "aws",
					Field:      "access-key",
					MountPath:  "/tmp/aws",
				},
			},
		},
		{
			name: "explicit field with 'as' - pass through unchanged",
			credentials: []api.CredentialReference{
				{
					Collection: "my-creds",
					Group:      "aws",
					Field:      "access-key",
					As:         "renamed-key",
					MountPath:  "/tmp/aws",
				},
			},
			gsmConfig: &api.GSMConfig{},
			expected: []api.CredentialReference{
				{
					Collection: "my-creds",
					Group:      "aws",
					Field:      "access-key",
					As:         "renamed-key",
					MountPath:  "/tmp/aws",
				},
			},
		},
		{
			name: "auto-discovery with cache",
			credentials: []api.CredentialReference{
				{
					Collection: "my-creds",
					Group:      "aws",
					MountPath:  "/tmp/aws",
				},
			},
			gsmConfig: &api.GSMConfig{},
			discoveredFields: map[collectionGroupKey][]string{
				{collection: "my-creds", group: "aws"}: {"token", "password"},
			},
			expected: []api.CredentialReference{
				{Collection: "my-creds", Group: "aws", Field: "token", MountPath: "/tmp/aws"},
				{Collection: "my-creds", Group: "aws", Field: "password", MountPath: "/tmp/aws"},
			},
		},
		{
			name: "bundle reference - expands to fields",
			credentials: []api.CredentialReference{
				{
					Bundle:    "aws-bundle",
					MountPath: "/tmp/aws",
				},
			},
			gsmConfig: &api.GSMConfig{
				Bundles: []api.GSMBundle{
					{
						Name: "aws-bundle",
						GSMSecrets: []api.GSMSecretRef{
							{
								Collection: "my-creds",
								Group:      "aws",
								Fields: []api.FieldEntry{
									{Name: "access-key"},
									{Name: "secret-key", As: "secret"},
								},
							},
						},
					},
				},
			},
			expected: []api.CredentialReference{
				{Collection: "my-creds", Group: "aws", Field: "access-key", MountPath: "/tmp/aws"},
				{Collection: "my-creds", Group: "aws", Field: "secret-key", As: "secret", MountPath: "/tmp/aws"},
			},
		},
		{
			name: "bundle with auto-discovery",
			credentials: []api.CredentialReference{
				{
					Bundle:    "aws-bundle",
					MountPath: "/tmp/aws",
				},
			},
			gsmConfig: &api.GSMConfig{
				Bundles: []api.GSMBundle{
					{
						Name: "aws-bundle",
						GSMSecrets: []api.GSMSecretRef{
							{
								Collection: "my-creds",
								Group:      "aws",
							},
						},
					},
				},
			},
			discoveredFields: map[collectionGroupKey][]string{
				{collection: "my-creds", group: "aws"}: {"discovered-key-1", "discovered-key-2"},
			},
			expected: []api.CredentialReference{
				{Collection: "my-creds", Group: "aws", Field: "discovered-key-1", MountPath: "/tmp/aws"},
				{Collection: "my-creds", Group: "aws", Field: "discovered-key-2", MountPath: "/tmp/aws"},
			},
		},
		{
			name: "bundle with multiple gsm secrets with explicit fields",
			credentials: []api.CredentialReference{
				{
					Bundle:    "multi-bundle",
					MountPath: "/tmp/creds",
				},
			},
			gsmConfig: &api.GSMConfig{
				Bundles: []api.GSMBundle{
					{
						Name: "multi-bundle",
						GSMSecrets: []api.GSMSecretRef{
							{
								Collection: "creds",
								Group:      "aws",
								Fields:     []api.FieldEntry{{Name: "aws-key"}},
							},
							{
								Collection: "creds",
								Group:      "gcp",
								Fields:     []api.FieldEntry{{Name: "gcp-key"}},
							},
						},
					},
				},
			},
			expected: []api.CredentialReference{
				{Collection: "creds", Group: "aws", Field: "aws-key", MountPath: "/tmp/creds"},
				{Collection: "creds", Group: "gcp", Field: "gcp-key", MountPath: "/tmp/creds"},
			},
		},
		{
			name: "complex case - bundle, explicit, auto-discovery",
			credentials: []api.CredentialReference{
				{Bundle: "my-bundle", MountPath: "/tmp/bundle"},
				{Collection: "direct", Group: "group1", Field: "explicit", MountPath: "/tmp/direct"},
				{Collection: "auto", Group: "group2", MountPath: "/tmp/auto"},
			},
			gsmConfig: &api.GSMConfig{
				Bundles: []api.GSMBundle{
					{
						Name: "my-bundle",
						GSMSecrets: []api.GSMSecretRef{
							{Collection: "bundle-creds", Group: "g1", Fields: []api.FieldEntry{{Name: "bundle-field"}}},
						},
					},
				},
			},
			discoveredFields: map[collectionGroupKey][]string{
				{collection: "auto", group: "group2"}: {"auto-field-1", "auto-field-2"},
			},
			expected: []api.CredentialReference{
				{Collection: "bundle-creds", Group: "g1", Field: "bundle-field", MountPath: "/tmp/bundle"},
				{Collection: "direct", Group: "group1", Field: "explicit", MountPath: "/tmp/direct"},
				{Collection: "auto", Group: "group2", Field: "auto-field-1", MountPath: "/tmp/auto"},
				{Collection: "auto", Group: "group2", Field: "auto-field-2", MountPath: "/tmp/auto"},
			},
		},
		{
			name: "error: bundle not found in config",
			credentials: []api.CredentialReference{
				{Bundle: "non-existent-bundle", MountPath: "/tmp/test"},
			},
			gsmConfig: &api.GSMConfig{
				Bundles: []api.GSMBundle{
					{Name: "different-bundle"},
				},
			},
			expectedError: errors.New("bundle \"non-existent-bundle\" not found in config file"),
		},
		{
			name: "error: bundle reference without GSM config",
			credentials: []api.CredentialReference{
				{Bundle: "some-bundle", MountPath: "/tmp/test"},
			},
			expectedError: errors.New("bundle reference \"some-bundle\" requires gsm-config file, but it is not loaded"),
		},
		{
			name: "error: invalid credential (no bundle, collection, or group)",
			credentials: []api.CredentialReference{
				{MountPath: "/tmp/test"},
			},
			gsmConfig:     &api.GSMConfig{},
			expectedError: errors.New("invalid credential reference: must provide bundle, collection+group, or collection+group+field"),
		},
		{
			name: "error: credential only has field (no bundle, collection, or group)",
			credentials: []api.CredentialReference{
				{MountPath: "/tmp/test", Field: "some-field"},
			},
			gsmConfig:     &api.GSMConfig{},
			expectedError: errors.New("invalid credential reference: must provide bundle, collection+group, or collection+group+field"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ResolveCredentialReferences(
				context.Background(),
				tc.credentials,
				tc.gsmConfig,
				client,
				gsm.Config{},
				tc.discoveredFields,
			)

			if tc.expectedError != nil {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if diff := cmp.Diff(err.Error(), tc.expectedError.Error(), testhelper.EquateErrorMessage); diff != "" {
					t.Errorf("unexpected error (-want +got):\n%s", diff)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if diff := cmp.Diff(tc.expected, result); diff != "" {
				t.Errorf("result differs from expected:\n%s", diff)
			}
		})
	}
}

func TestExpandBundle(t *testing.T) {
	client := &fakeGSMClient{}
	projectConfig := gsm.Config{}
	for _, tc := range []struct {
		name             string
		bundle           *api.GSMBundle
		discoveredFields map[collectionGroupKey][]string
		expected         []api.CredentialReference
	}{
		{
			name: "bundle with explicit fields",
			bundle: &api.GSMBundle{
				Name: "test-bundle",
				GSMSecrets: []api.GSMSecretRef{
					{
						Collection: "my-creds",
						Group:      "aws",
						Fields: []api.FieldEntry{
							{Name: "access-key"},
							{Name: "secret-key", As: "renamed-secret"},
						},
					},
				},
			},
			expected: []api.CredentialReference{
				{Collection: "my-creds", Group: "aws", Field: "access-key"},
				{Collection: "my-creds", Group: "aws", Field: "secret-key", As: "renamed-secret"},
			},
			discoveredFields: map[collectionGroupKey][]string{},
		},
		{
			name: "bundle without explicit fields",
			bundle: &api.GSMBundle{
				Name: "test-bundle",
				GSMSecrets: []api.GSMSecretRef{
					{
						Collection: "my-creds",
						Group:      "aws",
					},
				},
			},
			discoveredFields: map[collectionGroupKey][]string{
				{collection: "my-creds", group: "aws"}: {"token", "password", "url"},
			},
			expected: []api.CredentialReference{
				{Collection: "my-creds", Group: "aws", Field: "token"},
				{Collection: "my-creds", Group: "aws", Field: "password"},
				{Collection: "my-creds", Group: "aws", Field: "url"},
			},
		},
		{
			name: "bundle with some explicit fields",
			bundle: &api.GSMBundle{
				Name: "test-bundle",
				GSMSecrets: []api.GSMSecretRef{
					{
						Collection: "my-creds",
						Group:      "aws",
					},
					{
						Collection: "my-creds",
						Group:      "testing",
						Fields: []api.FieldEntry{
							{Name: "key"},
							{Name: "pswd"},
						},
					},
				},
			},
			discoveredFields: map[collectionGroupKey][]string{
				{collection: "my-creds", group: "aws"}: {"token", "password", "url"},
			},
			expected: []api.CredentialReference{
				{Collection: "my-creds", Group: "aws", Field: "token"},
				{Collection: "my-creds", Group: "aws", Field: "password"},
				{Collection: "my-creds", Group: "aws", Field: "url"},
				{Collection: "my-creds", Group: "testing", Field: "key"},
				{Collection: "my-creds", Group: "testing", Field: "pswd"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := expandBundle(context.Background(), tc.bundle, client, projectConfig, tc.discoveredFields)
			if err != nil {
				t.Fatalf("failed to expand bundle: %v", err)
			}
			if diff := cmp.Diff(tc.expected, resolved); diff != "" {
				t.Fatal("unexpected expanded bundle (-want, +got) = ", diff)
			}
		})
	}
}

func TestValidateNoGroupCollisionsOnMountPath(t *testing.T) {
	for _, tc := range []struct {
		name        string
		credentials []api.CredentialReference
		expectError bool
	}{
		{
			name:        "no credentials",
			credentials: []api.CredentialReference{},
		},
		{
			name: "only one credential",
			credentials: []api.CredentialReference{
				{
					Collection: "test-collection",
					Field:      "key",
					Group:      "aws",
					MountPath:  "path`",
				},
			},
		},
		{
			name: "same collection and group but different mount paths",
			credentials: []api.CredentialReference{
				{
					Collection: "test-collection",
					Field:      "key",
					Group:      "aws",
					MountPath:  "/tmp/aws-secrets",
				},
				{
					Collection: "test-collection",
					Field:      "key",
					Group:      "gcp",
					MountPath:  "/tmp/gcp-secrets",
				},
			},
		},
		{
			name: "same collection and group and same mount path",
			credentials: []api.CredentialReference{
				{
					Collection: "test-collection",
					Field:      "key",
					Group:      "aws",
					MountPath:  "/tmp/secrets",
				},
				{
					Collection: "test-collection",
					Field:      "key",
					Group:      "gcp",
					MountPath:  "/tmp/secrets",
				},
			},
			expectError: true,
		},
		{
			name: "different collections with same groups",
			credentials: []api.CredentialReference{
				{
					Collection: "test-collection",
					Field:      "something",
					Group:      "aws",
					MountPath:  "/tmp/secrets",
				},
				{
					Collection: "another-collection",
					Field:      "key",
					Group:      "aws",
					MountPath:  "/tmp/secrets",
				},
			},
		},
		{
			name: "multiple fields, one collision",
			credentials: []api.CredentialReference{
				{
					Collection: "collection1",
					Field:      "key",
					Group:      "aws",
					MountPath:  "/tmp/secrets",
				},
				{
					Collection: "collection1",
					Field:      "password",
					Group:      "aws",
					MountPath:  "/tmp/secrets",
				},
				{
					Collection: "collection1",
					Field:      "key",
					Group:      "gcp",
					MountPath:  "/tmp/secrets",
				},
			},
			expectError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actualErr := ValidateNoGroupCollisionsOnMountPath(tc.credentials)
			if tc.expectError && actualErr == nil {
				t.Fatal("expected error but got none")
			}
			if tc.expectError == false && actualErr != nil {
				t.Fatalf("expected no error but got: %v", actualErr)
			}
		})
	}
}
