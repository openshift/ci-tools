package main

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestValidate(t *testing.T) {
	var testCases = []struct {
		name     string
		expected error
		profiles api.ClusterProfiles
	}{
		{
			name: "Empty config file",
		},
		{
			name: "Valid config file",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name:   "aws",
						Owners: []api.ClusterProfileOwners{{Org: "aws", Repos: []string{"repo1"}}},
					},
					{
						Name:   "gcp",
						Owners: []api.ClusterProfileOwners{{Org: "gcp-org"}},
					},
					{Name: "aws2"},
				},
			},
		},
		{
			name: "Duplicated profile in config file",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name:   "aws",
						Owners: []api.ClusterProfileOwners{{Org: "aws", Repos: []string{"repo1"}}},
					},
					{
						Name:   "gcp",
						Owners: []api.ClusterProfileOwners{{Org: "gcp-org"}},
					},
					{Name: "aws"},
					{Name: "gcp2"},
				},
			},
			expected: fmt.Errorf("cluster profile 'aws' already exists in the configuration file"),
		},
		{
			name: "Duplicated org within profile",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "aws", Repos: []string{"repo1"}},
							{Org: "aws", Repos: []string{"repo2"}},
						},
					},
				},
			},
			expected: fmt.Errorf(`cluster profile 'aws' has duplicate org "aws"`),
		},
		{
			name: "Invalid owner",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name:   "aws",
						Owners: []api.ClusterProfileOwners{{}},
					},
				},
			},
			expected: fmt.Errorf(`cluster profile 'aws' has an invalid owner`),
		},
		{
			name: "Konflux and org mutually exclusive",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{{
							Org:     "openshift",
							Konflux: &api.ClusterProfileKonfluxOwner{Tenant: "openshift"},
						}},
					},
				},
			},
			expected: fmt.Errorf(`cluster profile 'aws' has both org and tenant set`),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			validator := newValidator(fakectrlruntimeclient.NewFakeClient())
			err := validator.Validate(tc.profiles)
			if diff := cmp.Diff(tc.expected, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("error differs from expected:\n%v", diff)
			}
		})
	}
}

func TestCheckCISecrets(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		profiles api.ClusterProfiles
		secrets  []coreapi.Secret
		expected error
	}{
		{
			name: "existing secret for profile",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{{
					Name:   "aws",
					Secret: "cluster-secrets-aws",
				}},
			},
			secrets: []coreapi.Secret{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ci",
					Name:      "cluster-secrets-aws",
				},
			}},
		},
		{
			name: "profile set is skipped",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{{
					Name:       "openshift-org-aws",
					SetMembers: []string{"aws", "aws-2"},
				}},
			},
		},
		{
			name: "missing secret in cluster",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{{
					Name:   "aws",
					Secret: "cluster-secrets-aws",
				}},
			},
			expected: fmt.Errorf(`failed to get secret 'cluster-secrets-aws' for cluster profile 'aws': secrets "cluster-secrets-aws" not found`),
		},
		{
			name: "profile without secret field",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{{
					Name: "aws",
				}},
			},
			expected: fmt.Errorf(`cluster profile 'aws' is missing a secret`),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			objects := make([]runtime.Object, 0, len(tc.secrets))
			for i := range tc.secrets {
				objects = append(objects, &tc.secrets[i])
			}

			client := fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(objects...).Build()
			validator := newValidator(client)

			if err := validator.Validate(tc.profiles); err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}

			err := validator.checkCISecrets()
			if diff := cmp.Diff(tc.expected, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("error differs from expected:\n%v", diff)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		profiles     api.ClusterProfiles
		wantProfiles api.ClusterProfiles
	}{
		{
			name:         "Empty profile list",
			profiles:     api.ClusterProfiles{},
			wantProfiles: api.ClusterProfiles{},
		},
		{
			name: "Profile with nil owners",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{Name: "aws", Secret: "aws-secret", Owners: nil},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{Name: "aws", Secret: "aws-secret", Owners: nil},
				},
			},
		},
		{
			name: "Profile with empty owners slice",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{Name: "aws", Secret: "aws-secret", Owners: []api.ClusterProfileOwners{}},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{Name: "aws", Secret: "aws-secret", Owners: []api.ClusterProfileOwners{}},
				},
			},
		},
		{
			name: "Owner with nil repos",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: nil},
						},
					},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: nil},
						},
					},
				},
			},
		},
		{
			name: "Owner with empty repos slice",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: []string{}},
						},
					},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: []string{}},
						},
					},
				},
			},
		},
		{
			name: "Mix of nil and empty repos",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: nil},
							{Org: "redhat-developer", Repos: []string{}},
						},
					},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: nil},
							{Org: "redhat-developer", Repos: []string{}},
						},
					},
				},
			},
		},
		{
			name: "All owners with nil repos",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "redhat-developer", Repos: nil},
							{Org: "ComplianceAsCode", Repos: nil},
							{Org: "openshift", Repos: nil},
						},
					},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "ComplianceAsCode", Repos: nil},
							{Org: "openshift", Repos: nil},
							{Org: "redhat-developer", Repos: nil},
						},
					},
				},
			},
		},
		{
			name: "Profile with unsorted repos",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: []string{"origin", "api", "installer"}},
						},
					},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: []string{"api", "installer", "origin"}},
						},
					},
				},
			},
		},
		{
			name: "Profile with unsorted orgs",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "redhat-developer", Repos: []string{"kam"}},
							{Org: "openshift", Repos: []string{"api"}},
						},
					},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: []string{"api"}},
							{Org: "redhat-developer", Repos: []string{"kam"}},
						},
					},
				},
			},
		},
		{
			name: "Profile with already sorted owners and repos",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: []string{"api", "installer", "origin"}},
							{Org: "redhat-developer", Repos: []string{"gitops-operator", "kam"}},
						},
					},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: []string{"api", "installer", "origin"}},
							{Org: "redhat-developer", Repos: []string{"gitops-operator", "kam"}},
						},
					},
				},
			},
		},
		{
			name: "Profile with different orgs - sorts orgs alphabetically",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "redhat-developer", Repos: []string{"installer", "api"}},
							{Org: "openshift", Repos: []string{"origin", "cli"}},
						},
					},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: []string{"cli", "origin"}},
							{Org: "redhat-developer", Repos: []string{"api", "installer"}},
						},
					},
				},
			},
		},
		{
			name: "Case-sensitive org sorting",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: []string{"api"}},
							{Org: "ComplianceAsCode", Repos: []string{"content"}},
							{Org: "AWS-Org", Repos: []string{"repo1"}},
							{Org: "azure-org", Repos: []string{"repo2"}},
						},
					},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "AWS-Org", Repos: []string{"repo1"}},
							{Org: "ComplianceAsCode", Repos: []string{"content"}},
							{Org: "azure-org", Repos: []string{"repo2"}},
							{Org: "openshift", Repos: []string{"api"}},
						},
					},
				},
			},
		},
		{
			name: "Multiple profiles - sorts each independently",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "redhat-developer", Repos: []string{"kam", "gitops-operator"}},
							{Org: "openshift", Repos: []string{"origin", "api"}},
						},
					},
					{
						Name: "gcp",
						Owners: []api.ClusterProfileOwners{
							{Org: "redhat-developer", Repos: []string{"cli"}},
							{Org: "openshift", Repos: []string{"installer"}},
						},
					},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: []string{"api", "origin"}},
							{Org: "redhat-developer", Repos: []string{"gitops-operator", "kam"}},
						},
					},
					{
						Name: "gcp",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: []string{"installer"}},
							{Org: "redhat-developer", Repos: []string{"cli"}},
						},
					},
				},
			},
		},
		{
			name: "Single org with single repo",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: []string{"api"}},
						},
					},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "openshift", Repos: []string{"api"}},
						},
					},
				},
			},
		},
		{
			name: "Complex: unsorted orgs and repos, with duplicates",
			profiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "redhat-developer", Repos: []string{"kam", "gitops-operator"}},
							{Org: "ComplianceAsCode", Repos: []string{"ocp4e2e", "content"}},
							{Org: "openshift", Repos: []string{"origin", "installer"}},
							{Org: "azure-org", Repos: []string{"repo2", "repo1"}},
						},
					},
				},
			},
			wantProfiles: api.ClusterProfiles{
				Items: []api.ClusterProfile{
					{
						Name: "aws",
						Owners: []api.ClusterProfileOwners{
							{Org: "ComplianceAsCode", Repos: []string{"content", "ocp4e2e"}},
							{Org: "azure-org", Repos: []string{"repo1", "repo2"}},
							{Org: "openshift", Repos: []string{"installer", "origin"}},
							{Org: "redhat-developer", Repos: []string{"gitops-operator", "kam"}},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.profiles.Items = normalize(tc.profiles.Items)

			if diff := cmp.Diff(tc.wantProfiles, tc.profiles); diff != "" {
				t.Errorf("normalized result differs from expected (-want +got):\n%s", diff)
			}
		})
	}
}
