package csi_secrets

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

func (f *fakeGSMClient) ListSecretVersions(ctx context.Context, req *secretmanagerpb.ListSecretVersionsRequest, opts ...gax.CallOption) *secretmanager.SecretVersionIterator {
	return nil
}

func (f *fakeGSMClient) DestroySecretVersion(ctx context.Context, req *secretmanagerpb.DestroySecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.SecretVersion, error) {
	panic("DestroySecretVersion not implemented in test")
}

func TestResolveCredentialReferences(t *testing.T) {
	client := &fakeGSMClient{}

	testCases := []struct {
		name             string
		credentials      []api.CredentialReference
		gsmConfig        *api.GSMConfig
		discoveredFields map[CollectionGroupKey][]string
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
			discoveredFields: map[CollectionGroupKey][]string{
				{Collection: "my-creds", Group: "aws"}: {"token", "password"},
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
			discoveredFields: map[CollectionGroupKey][]string{
				{Collection: "my-creds", Group: "aws"}: {"discovered-key-1", "discovered-key-2"},
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
			discoveredFields: map[CollectionGroupKey][]string{
				{Collection: "auto", Group: "group2"}: {"auto-field-1", "auto-field-2"},
			},
			expected: []api.CredentialReference{
				{Collection: "bundle-creds", Group: "g1", Field: "bundle-field", MountPath: "/tmp/bundle"},
				{Collection: "direct", Group: "group1", Field: "explicit", MountPath: "/tmp/direct"},
				{Collection: "auto", Group: "group2", Field: "auto-field-1", MountPath: "/tmp/auto"},
				{Collection: "auto", Group: "group2", Field: "auto-field-2", MountPath: "/tmp/auto"},
			},
		},
		{
			name: "bundle with sync_to_cluster: true and dockerconfig - converts to K8s Secret reference",
			credentials: []api.CredentialReference{
				{
					Bundle:    "docker-bundle",
					Namespace: "test-credentials",
					MountPath: "/tmp/docker",
				},
			},
			gsmConfig: &api.GSMConfig{
				DPTPCollection: "test-platform-infra",
				Bundles: []api.GSMBundle{
					{
						Name:          "docker-bundle",
						SyncToCluster: true,
						Targets: []api.TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
						DockerConfig: &api.DockerConfigSpec{
							Registries: []api.RegistryAuthData{
								{Group: "registry", RegistryURL: "registry.ci.openshift.org", AuthField: "auth"},
							},
						},
					},
				},
			},
			expected: []api.CredentialReference{
				{Namespace: "test-credentials", Name: "docker-bundle", MountPath: "/tmp/docker"},
			},
		},
		{
			name: "bundle with sync_to_cluster: true - converts to K8s Secret reference",
			credentials: []api.CredentialReference{
				{
					Bundle:    "k8s-secret-bundle",
					Namespace: "test-credentials",
					MountPath: "/tmp/k8s-secret",
				},
			},
			gsmConfig: &api.GSMConfig{
				Bundles: []api.GSMBundle{
					{
						Name:          "k8s-secret-bundle",
						SyncToCluster: true,
						Targets: []api.TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
							{Cluster: "build02", Namespace: "ci"},
						},
						GSMSecrets: []api.GSMSecretRef{
							{Collection: "creds", Group: "aws", Fields: []api.FieldEntry{{Name: "key"}}},
						},
					},
				},
			},
			expected: []api.CredentialReference{
				{Namespace: "test-credentials", Name: "k8s-secret-bundle", MountPath: "/tmp/k8s-secret"},
			},
		},
		{
			name: "error: bundle with sync_to_cluster: true but no namespace",
			credentials: []api.CredentialReference{
				{
					Bundle:    "k8s-secret-bundle",
					MountPath: "/tmp/k8s-secret",
				},
			},
			gsmConfig: &api.GSMConfig{
				Bundles: []api.GSMBundle{
					{
						Name:          "k8s-secret-bundle",
						SyncToCluster: true,
						Targets: []api.TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectedError: errors.New("bundle \"k8s-secret-bundle\" has sync_to_cluster: true but credential has no namespace specified"),
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
		discoveredFields map[CollectionGroupKey][]string
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
			discoveredFields: map[CollectionGroupKey][]string{},
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
			discoveredFields: map[CollectionGroupKey][]string{
				{Collection: "my-creds", Group: "aws"}: {"token", "password", "url"},
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
			discoveredFields: map[CollectionGroupKey][]string{
				{Collection: "my-creds", Group: "aws"}: {"token", "password", "url"},
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

func TestValidateNoFileCollisionsOnMountPath(t *testing.T) {
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
			name: "single credential",
			credentials: []api.CredentialReference{
				{Collection: "col", Group: "grp", Field: "key", MountPath: "/tmp/secrets"},
			},
		},
		{
			name: "same field name at different mount paths is ok",
			credentials: []api.CredentialReference{
				{Collection: "col", Group: "aws", Field: "key", MountPath: "/tmp/aws"},
				{Collection: "col", Group: "gcp", Field: "key", MountPath: "/tmp/gcp"},
			},
		},
		{
			name: "different field names at same mount path is ok",
			credentials: []api.CredentialReference{
				{Collection: "col-a", Group: "grp1", Field: "login", MountPath: "/tmp/secrets"},
				{Collection: "col-b", Group: "grp2", Field: "password", MountPath: "/tmp/secrets"},
			},
		},
		{
			name: "same field name from different collections at same path collides",
			credentials: []api.CredentialReference{
				{Collection: "col-a", Group: "grp1", Field: "key", MountPath: "/tmp/secrets"},
				{Collection: "col-b", Group: "grp2", Field: "key", MountPath: "/tmp/secrets"},
			},
			expectError: true,
		},
		{
			name: "same field name from different groups in same collection collides",
			credentials: []api.CredentialReference{
				{Collection: "col", Group: "aws", Field: "key", MountPath: "/tmp/secrets"},
				{Collection: "col", Group: "gcp", Field: "key", MountPath: "/tmp/secrets"},
			},
			expectError: true,
		},
		{
			name: "`as` rename that collides with another field",
			credentials: []api.CredentialReference{
				{Collection: "col", Group: "grp", Field: "original", MountPath: "/tmp/secrets"},
				{Collection: "col", Group: "grp", Field: "renamed", As: "original", MountPath: "/tmp/secrets"},
			},
			expectError: true,
		},
		{
			name: "denormalized name collision via --dot--",
			credentials: []api.CredentialReference{
				{Collection: "col-a", Group: "grp", Field: "config--dot--yaml", MountPath: "/tmp/secrets"},
				{Collection: "col-b", Group: "grp", Field: "config--dot--yaml", MountPath: "/tmp/secrets"},
			},
			expectError: true,
		},
		{
			name: "multi-collection bundle with unique field names is ok",
			credentials: []api.CredentialReference{
				{Collection: "col-a", Group: "grp-x", Field: "login", MountPath: "/var/bundle"},
				{Collection: "col-a", Group: "grp-x", Field: "pswd", MountPath: "/var/bundle"},
				{Collection: "col-b", Group: "grp-y", Field: "token", MountPath: "/var/bundle"},
			},
		},
		{
			name: "sync_to_cluster bundle resolved to k8s secret reference is skipped",
			credentials: []api.CredentialReference{
				{Namespace: "ci", Name: "cluster-init", MountPath: "/etc/kubeconfigs"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actualErr := ValidateNoFileCollisionsOnMountPath(tc.credentials)
			if tc.expectError && actualErr == nil {
				t.Fatal("expected error but got none")
			}
			if !tc.expectError && actualErr != nil {
				t.Fatalf("expected no error but got: %v", actualErr)
			}
		})
	}
}
