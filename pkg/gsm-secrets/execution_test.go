package gsmsecrets

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/iam/admin/apiv1/adminpb"
	"cloud.google.com/go/iam/apiv1/iampb"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/google/go-cmp/cmp"
	gax "github.com/googleapis/gax-go/v2"
	"go.uber.org/mock/gomock"
	"google.golang.org/api/googleapi"
	"google.golang.org/genproto/googleapis/type/expr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"k8s.io/apimachinery/pkg/util/wait"

	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
)

func TestGenerateServiceAccountKey(t *testing.T) {
	config := Config{
		ProjectIdString: "test-project",
		ProjectIdNumber: "123456789",
	}

	testCases := []struct {
		name                   string
		saEmail                string
		mockKeyData            []byte
		createKeyErrors        []error
		expectError            bool
		expectedCreateKeyCalls int
	}{
		{
			name:                   "successful key generation on first try",
			saEmail:                GetUpdaterSAEmail("test-collection", config),
			mockKeyData:            []byte("fake-private-key-data"),
			createKeyErrors:        []error{nil},
			expectError:            false,
			expectedCreateKeyCalls: 1,
		},
		{
			name:                   "non-retryable IAM client error",
			saEmail:                GetUpdaterSAEmail("test-collection", config),
			mockKeyData:            nil,
			createKeyErrors:        []error{errors.New("some non-retryable GCP error")},
			expectError:            true,
			expectedCreateKeyCalls: 1,
		},
		{
			name:        "retryable NotFound error - eventual success",
			saEmail:     GetUpdaterSAEmail("test-collection", config),
			mockKeyData: []byte("fake-private-key-data"),
			createKeyErrors: []error{
				status.Error(codes.NotFound, "service account not found"),
				nil, // Success on second attempt
			},
			expectError:            false,
			expectedCreateKeyCalls: 2,
		},
		{
			name:        "retryable NotFound error - all attempts fail",
			saEmail:     GetUpdaterSAEmail("test-collection", config),
			mockKeyData: nil,
			createKeyErrors: []error{
				status.Error(codes.NotFound, "service account not found"),
				status.Error(codes.NotFound, "service account not found"),
				status.Error(codes.NotFound, "service account not found"),
			},
			expectError:            true,
			expectedCreateKeyCalls: 3,
		},
		{
			name:        "retryable HTTP 404 error - eventual success",
			saEmail:     GetUpdaterSAEmail("test-collection", config),
			mockKeyData: []byte("fake-private-key-data"),
			createKeyErrors: []error{
				&googleapi.Error{Code: http.StatusNotFound, Message: "service account not found"},
				nil, // Success on second attempt
			},
			expectError:            false,
			expectedCreateKeyCalls: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockIAMClient := NewMockIAMClient(mockCtrl)

			keyRequest := &adminpb.CreateServiceAccountKeyRequest{
				Name: fmt.Sprintf("%s/serviceAccounts/%s", GetProjectResourceString(config.ProjectIdString), tc.saEmail),
			}

			// Set up CreateServiceAccountKey calls based on the errors list
			callCount := 0
			mockIAMClient.EXPECT().
				CreateServiceAccountKey(gomock.Any(), keyRequest).
				DoAndReturn(func(ctx context.Context, req *adminpb.CreateServiceAccountKeyRequest, opts ...gax.CallOption) (*adminpb.ServiceAccountKey, error) {
					if callCount < len(tc.createKeyErrors) {
						err := tc.createKeyErrors[callCount]
						callCount++
						if err != nil {
							return nil, err
						}
						return &adminpb.ServiceAccountKey{
							PrivateKeyData: tc.mockKeyData,
						}, nil
					}
					return nil, errors.New("unexpected CreateServiceAccountKey call")
				}).
				Times(tc.expectedCreateKeyCalls)

			testBackoff := wait.Backoff{
				Steps:    3,
				Duration: 10 * time.Millisecond,
				Factor:   2.0,
				Cap:      50 * time.Millisecond,
			}
			result, actualErr := generateServiceAccountKeyWithBackoff(context.Background(), mockIAMClient, tc.saEmail, config.ProjectIdString, testBackoff)

			if tc.expectError {
				if actualErr == nil {
					t.Errorf("Expected error but got none")
					return
				}
				return
			}

			if actualErr != nil {
				t.Errorf("Unexpected error: %v", actualErr)
				return
			}

			if string(result) != string(tc.mockKeyData) {
				t.Errorf("Expected key data %q, got %q", tc.mockKeyData, result)
			}
		})
	}
}

func TestCreateServiceAccounts(t *testing.T) {
	config := Config{
		ProjectIdString: "test-project",
		ProjectIdNumber: "123456789",
	}
	collection := "test-collection"
	longCollection := "this-is-a-very-long-collection-name-that-exceeds-normal-limits"
	testCases := []struct {
		name                     string
		serviceAccountsToCreate  map[string]ServiceAccountInfo
		secretsToCreate          map[string]GCPSecret
		clientCreateSAError      error
		clientGenerateKeyError   error
		expectedSecretsRemaining int
		expectPayloadSet         bool
	}{

		{
			name:                     "no service accounts to create",
			serviceAccountsToCreate:  map[string]ServiceAccountInfo{},
			secretsToCreate:          map[string]GCPSecret{},
			expectedSecretsRemaining: 0,
			expectPayloadSet:         false,
		},
		{
			name: "successful service account and key creation",
			serviceAccountsToCreate: map[string]ServiceAccountInfo{
				collection: {
					Email:       GetUpdaterSAEmail(collection, config),
					DisplayName: GetUpdaterSADisplayName(collection),
					ID:          GetUpdaterSAId(collection),
					Collection:  collection,
					Description: GetUpdaterSADescription(collection),
				},
			},
			secretsToCreate: map[string]GCPSecret{
				GetUpdaterSASecretName(collection): {
					Name:       GetUpdaterSASecretName(collection),
					Type:       SecretTypeSA,
					Collection: collection,
				},
			},
			expectedSecretsRemaining: 1,
			expectPayloadSet:         true,
		},
		{
			name: "successful service account and key creation with long collection name",
			serviceAccountsToCreate: map[string]ServiceAccountInfo{
				longCollection: {
					Email:       GetUpdaterSAEmail(longCollection, config),
					DisplayName: GetUpdaterSADisplayName(longCollection),
					ID:          GetUpdaterSAId(longCollection),
					Collection:  longCollection,
					Description: GetUpdaterSADescription(longCollection),
				},
			},
			secretsToCreate: map[string]GCPSecret{
				GetUpdaterSASecretName(longCollection): {
					Name:       GetUpdaterSASecretName(longCollection),
					Type:       SecretTypeSA,
					Collection: longCollection,
				},
			},
			expectedSecretsRemaining: 1,
			expectPayloadSet:         true,
		},
		{
			name: "CreateServiceAccount fails - secret should be removed",
			serviceAccountsToCreate: map[string]ServiceAccountInfo{
				collection: {
					Email:       GetUpdaterSAEmail(collection, config),
					DisplayName: GetUpdaterSADisplayName(collection),
					ID:          GetUpdaterSAId(collection),
					Collection:  collection,
					Description: GetUpdaterSADescription(collection),
				},
			},
			secretsToCreate: map[string]GCPSecret{
				GetUpdaterSASecretName(collection): {
					Name:       GetUpdaterSASecretName(collection),
					Type:       SecretTypeSA,
					Collection: collection,
				},
			},
			clientCreateSAError:      errors.New("Some GCP CreateServiceAccount failure"),
			expectedSecretsRemaining: 0,
			expectPayloadSet:         false,
		},
		{
			name: "generateServiceAccountKey fails - secret should be removed",
			serviceAccountsToCreate: map[string]ServiceAccountInfo{
				collection: {
					Email:       GetUpdaterSAEmail(collection, config),
					DisplayName: GetUpdaterSADisplayName(collection),
					ID:          GetUpdaterSAId(collection),
					Collection:  collection,
					Description: GetUpdaterSADescription(collection),
				},
			},
			secretsToCreate: map[string]GCPSecret{
				GetUpdaterSASecretName(collection): {
					Name:       GetUpdaterSASecretName(collection),
					Type:       SecretTypeSA,
					Collection: collection,
				},
			},
			clientGenerateKeyError:   errors.New("GCP CreateServiceAccountKey failed"),
			expectedSecretsRemaining: 0,
			expectPayloadSet:         false,
		},
		{
			name: "multiple service accounts to create",
			serviceAccountsToCreate: map[string]ServiceAccountInfo{
				collection: {
					Email:       GetUpdaterSAEmail(collection, config),
					DisplayName: GetUpdaterSADisplayName(collection),
					ID:          GetUpdaterSAId(collection),
					Collection:  collection,
					Description: GetUpdaterSADescription(collection),
				},
				"another-collection": {
					Email:       GetUpdaterSAEmail("another-collection", config),
					DisplayName: GetUpdaterSAId("another-collection"),
					Collection:  "another-collection",
				},
			},
			secretsToCreate: map[string]GCPSecret{
				GetUpdaterSASecretName(collection): {
					Name:       GetUpdaterSASecretName(collection),
					Type:       SecretTypeSA,
					Collection: collection,
				},
				GetUpdaterSASecretName("another-collection"): {
					Name:       GetUpdaterSASecretName("another-collection"),
					Type:       SecretTypeSA,
					Collection: "another-collection",
				},
			},
			expectedSecretsRemaining: 2,
			expectPayloadSet:         true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockIAMClient := NewMockIAMClient(mockCtrl)

			// Set up expectations for all service accounts
			if tc.clientCreateSAError != nil {
				mockIAMClient.EXPECT().
					CreateServiceAccount(gomock.Any(), gomock.Any()).
					Return(nil, tc.clientCreateSAError).
					Times(len(tc.serviceAccountsToCreate))
			} else {
				mockIAMClient.EXPECT().
					CreateServiceAccount(gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *adminpb.CreateServiceAccountRequest, opts ...gax.CallOption) (*adminpb.ServiceAccount, error) {
						// Find the matching SA from the test case
						for _, sa := range tc.serviceAccountsToCreate {
							if req.AccountId == sa.ID {
								return &adminpb.ServiceAccount{
									Email:       sa.Email,
									DisplayName: sa.DisplayName,
									Description: sa.Description,
								}, nil
							}
						}
						return nil, fmt.Errorf("unexpected service account: %s", req.AccountId)
					}).
					Times(len(tc.serviceAccountsToCreate))

				if tc.clientGenerateKeyError != nil {
					mockIAMClient.EXPECT().
						CreateServiceAccountKey(gomock.Any(), gomock.Any()).
						Return(nil, tc.clientGenerateKeyError).
						Times(len(tc.serviceAccountsToCreate))
				} else {
					mockIAMClient.EXPECT().
						CreateServiceAccountKey(gomock.Any(), gomock.Any()).
						DoAndReturn(func(ctx context.Context, req *adminpb.CreateServiceAccountKeyRequest, opts ...gax.CallOption) (*adminpb.ServiceAccountKey, error) {
							// Extract collection from the service account name
							for _, sa := range tc.serviceAccountsToCreate {
								expectedName := fmt.Sprintf("%s/serviceAccounts/%s", GetProjectResourceString(config.ProjectIdString), sa.Email)
								if req.Name == expectedName {
									return &adminpb.ServiceAccountKey{
										PrivateKeyData: []byte("generated-key-data-for-" + sa.Collection),
									}, nil
								}
							}
							return nil, fmt.Errorf("unexpected service account key request: %s", req.Name)
						}).
						Times(len(tc.serviceAccountsToCreate))
				}
			}

			secretsCopy := make(map[string]GCPSecret)
			maps.Copy(secretsCopy, tc.secretsToCreate)

			actions := &Actions{
				Config:          config,
				SAsToCreate:     tc.serviceAccountsToCreate,
				SecretsToCreate: secretsCopy,
			}

			actions.CreateServiceAccounts(context.Background(), mockIAMClient)

			if len(actions.SecretsToCreate) != tc.expectedSecretsRemaining {
				t.Errorf("Expected %d secrets remaining, got %d", tc.expectedSecretsRemaining, len(actions.SecretsToCreate))
			}

			if tc.expectPayloadSet {
				for _, sa := range tc.serviceAccountsToCreate {
					secretName := GetUpdaterSASecretName(sa.Collection)
					secret, exists := actions.SecretsToCreate[secretName]
					if !exists {
						t.Errorf("Expected secret %q to exist after successful creation", secretName)
						continue
					}
					if len(secret.Payload) == 0 {
						t.Errorf("Expected secret %q to have payload set, but it's empty", secretName)
					}
					expectedPayload := "generated-key-data-for-" + sa.Collection
					if string(secret.Payload) != expectedPayload {
						t.Errorf("Expected payload %q, got %q", expectedPayload, string(secret.Payload))
					}
				}
			}
		})
	}
}

func TestCreateSecrets(t *testing.T) {
	config := Config{
		ProjectIdString: "test-project",
		ProjectIdNumber: "123456789",
	}

	testCases := []struct {
		name                            string
		secrets                         map[string]GCPSecret
		numberOfSASecretsWithoutPayload int
	}{

		{
			name:                            "no secrets to create",
			secrets:                         map[string]GCPSecret{},
			numberOfSASecretsWithoutPayload: 0,
		},
		{
			name: "create secrets for one collection",
			secrets: map[string]GCPSecret{
				GetUpdaterSASecretName("test-collection"): {
					Name:       GetUpdaterSASecretName("test-collection"),
					Type:       SecretTypeSA,
					Collection: "test-collection",
				},
				GetIndexSecretName("test-collection"): {
					Name:       GetIndexSecretName("test-collection"),
					Type:       SecretTypeIndex,
					Collection: "test-collection",
				},
			},
			numberOfSASecretsWithoutPayload: 1,
		},
		{
			name: "create one service account secret",
			secrets: map[string]GCPSecret{
				GetUpdaterSASecretName("test-collection"): {
					Name:       GetUpdaterSASecretName("test-collection"),
					Type:       SecretTypeSA,
					Collection: "test-collection",
				},
			},
			numberOfSASecretsWithoutPayload: 1,
		},
		{
			name: "create one index secret",
			secrets: map[string]GCPSecret{
				GetIndexSecretName("test-collection"): {
					Name:       GetIndexSecretName("test-collection"),
					Type:       SecretTypeIndex,
					Collection: "test-collection",
				},
			},
			numberOfSASecretsWithoutPayload: 0,
		},
		{
			name: "multiple secrets",
			secrets: map[string]GCPSecret{
				GetUpdaterSASecretName("test-collection"): {
					Name:       GetUpdaterSASecretName("test-collection"),
					Type:       SecretTypeSA,
					Collection: "test-collection",
				},
				GetIndexSecretName("test-collection"): {
					Name:       GetIndexSecretName("test-collection"),
					Type:       SecretTypeIndex,
					Collection: "test-collection",
				},
				GetUpdaterSASecretName("another-collection"): {
					Name:       GetUpdaterSASecretName("another-collection"),
					Type:       SecretTypeSA,
					Collection: "another-collection",
				},
			},
			numberOfSASecretsWithoutPayload: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockSecretsClient := NewMockSecretManagerClient(mockCtrl)

			// GetSecret will be called first to check if secret exists (returns not found error)
			mockSecretsClient.EXPECT().GetSecret(gomock.Any(), gomock.Any()).
				Return(nil, status.Error(codes.NotFound, "secret not found")).
				Times(len(tc.secrets))

			mockSecretsClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, req *secretmanagerpb.CreateSecretRequest, opts ...gax.CallOption) (*secretmanagerpb.Secret, error) {
					return &secretmanagerpb.Secret{
						Name:        fmt.Sprintf("%s/secrets/%s", GetProjectResourceIdNumber(config.ProjectIdNumber), req.SecretId),
						Labels:      req.Secret.Labels,
						Annotations: req.Secret.Annotations,
						Replication: &secretmanagerpb.Replication{
							Replication: &secretmanagerpb.Replication_Automatic_{
								Automatic: &secretmanagerpb.Replication_Automatic{},
							},
						},
					}, nil
				}).Times(len(tc.secrets))

			mockSecretsClient.EXPECT().AddSecretVersion(gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, req *secretmanagerpb.AddSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.SecretVersion, error) {
					return &secretmanagerpb.SecretVersion{
						Name: fmt.Sprintf("%s/versions/1", req.Parent),
					}, nil
				}).Times(len(tc.secrets))

			mockIAMClient := NewMockIAMClient(mockCtrl)
			mockIAMClient.EXPECT().CreateServiceAccountKey(gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, req *adminpb.CreateServiceAccountKeyRequest, opts ...gax.CallOption) (*adminpb.ServiceAccountKey, error) {
					return &adminpb.ServiceAccountKey{
						PrivateKeyData: []byte("generated-sa-key-data"),
					}, nil
				}).Times(tc.numberOfSASecretsWithoutPayload)

			secretsCopy := make(map[string]GCPSecret)
			maps.Copy(secretsCopy, tc.secrets)
			actions := &Actions{
				Config:          config,
				SecretsToCreate: secretsCopy,
			}
			actions.CreateSecrets(context.Background(), mockSecretsClient, mockIAMClient)

			for name, secret := range actions.SecretsToCreate {
				if secret.Type == SecretTypeIndex {
					if len(secret.Payload) == 0 {
						t.Errorf("Expected index secret %q to have payload, but it has none", name)
					}
				}
			}
		})
	}
}

// TestListSecretFieldsValidation tests the client-side validation logic
// for non-recursive secret listing in ListSecretFieldsByCollectionAndGroup
func TestListSecretFieldsValidation(t *testing.T) {
	testCases := []struct {
		name          string
		collection    string
		group         string
		secretID      string
		shouldInclude bool
		description   string
	}{
		{
			name:          "valid direct child - simple group",
			collection:    "vsphere",
			group:         "ibmcloud",
			secretID:      "vsphere__ibmcloud__username",
			shouldInclude: true,
			description:   "Direct child with correct structure",
		},
		{
			name:          "invalid - nested subgroup",
			collection:    "vsphere",
			group:         "ibmcloud",
			secretID:      "vsphere__ibmcloud__subgroup__username",
			shouldInclude: false,
			description:   "Has extra subgroup level",
		},
		{
			name:          "valid - hierarchical group",
			collection:    "vsphere",
			group:         "ibmcloud/ci",
			secretID:      "vsphere__ibmcloud__ci__password",
			shouldInclude: true,
			description:   "Hierarchical group with correct depth",
		},
		{
			name:          "invalid - hierarchical group with nesting",
			collection:    "vsphere",
			group:         "ibmcloud/ci",
			secretID:      "vsphere__ibmcloud__ci__prod__password",
			shouldInclude: false,
			description:   "Hierarchical group with extra nesting",
		},
		{
			name:          "valid - empty group",
			collection:    "test",
			group:         "",
			secretID:      "test__field",
			shouldInclude: true,
			description:   "Empty group with direct field",
		},
		{
			name:          "invalid - empty group with nesting",
			collection:    "test",
			group:         "",
			secretID:      "test__subgroup__field",
			shouldInclude: false,
			description:   "Empty group but has subgroup",
		},
		{
			name:          "invalid - wrong collection",
			collection:    "vsphere",
			group:         "ibmcloud",
			secretID:      "other-collection__ibmcloud__username",
			shouldInclude: false,
			description:   "Collection doesn't match",
		},
		{
			name:          "invalid - wrong group",
			collection:    "vsphere",
			group:         "ibmcloud",
			secretID:      "vsphere__other-group__username",
			shouldInclude: false,
			description:   "Group doesn't match",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Build the expected prefix
			groupParts := strings.ReplaceAll(tc.group, "/", gsmvalidation.CollectionSecretDelimiter)
			base := fmt.Sprintf("%s%s", tc.collection, gsmvalidation.CollectionSecretDelimiter)
			if groupParts != "" {
				base = fmt.Sprintf("%s%s%s", base, groupParts, gsmvalidation.CollectionSecretDelimiter)
			}

			// Check prefix match
			if !strings.HasPrefix(tc.secretID, base) && tc.shouldInclude {
				t.Errorf("Expected secret %q to match prefix %q, but it doesn't", tc.secretID, base)
				return
			}

			if !strings.HasPrefix(tc.secretID, base) {
				// Prefix doesn't match, so it should be excluded
				if tc.shouldInclude {
					t.Errorf("Secret %q doesn't match prefix %q but was expected to be included", tc.secretID, base)
				}
				return
			}

			// Validate part count
			parts := strings.Split(tc.secretID, gsmvalidation.CollectionSecretDelimiter)
			expectedParts := 2 // collection + field
			if tc.group != "" {
				expectedParts += strings.Count(tc.group, "/") + 1
			}

			actuallyIncluded := len(parts) == expectedParts

			if actuallyIncluded != tc.shouldInclude {
				t.Errorf("%s: Secret %q - expected included=%v, got included=%v (parts=%d, expected=%d)",
					tc.description, tc.secretID, tc.shouldInclude, actuallyIncluded, len(parts), expectedParts)
			}
		})
	}
}

func TestApplyPolicy(t *testing.T) {
	config := Config{
		ProjectIdString: "test-project",
		ProjectIdNumber: "123456789",
	}

	newPolicy := func() *iampb.Policy {
		return &iampb.Policy{
			Version: 3,
			Etag:    []byte("etag"),
			Bindings: []*iampb.Binding{
				{
					Role:    config.GetSecretAccessorRole(),
					Members: []string{"group:good-team@redhat.com"},
					Condition: &expr.Expr{
						Title:      GetSecretsViewerGroupConditionTitle("good-team"),
						Expression: BuildSecretAccessorRoleConditionExpression("good-collection"),
					},
				},
				{
					Role:    config.GetSecretUpdaterRole(),
					Members: []string{"group:missing-team@redhat.com"},
					Condition: &expr.Expr{
						Title:      GetSecretsUpdaterGroupConditionTitle("missing-team"),
						Expression: BuildSecretUpdaterRoleConditionExpression("missing-collection"),
					},
				},
				{
					Role:    config.GetSecretAccessorRole(),
					Members: []string{"group:other-missing@redhat.com", "serviceAccount:collection-updater@test-project.iam.gserviceaccount.com"},
					Condition: &expr.Expr{
						Title:      GetSecretsViewerConditionTitle("collection"),
						Expression: BuildSecretAccessorRoleConditionExpression("collection"),
					},
				},
				// Hand-made grant on the same role.
				{
					Role:    config.GetSecretAccessorRole(),
					Members: []string{"group:exception-team@redhat.com"},
					Condition: &expr.Expr{
						Title:      "EXCEPTION: manually granted access",
						Expression: BuildSecretAccessorRoleConditionExpression("legacy-collection"),
					},
				},
			},
		}
	}

	nonExistentGroup := func(email string) error {
		return status.Errorf(codes.InvalidArgument, "Group %s does not exist.", email)
	}

	newCrowdedPolicy := func() *iampb.Policy {
		var members []string
		for i := range maxUnresolvableGroups + 2 {
			members = append(members, fmt.Sprintf("group:g%02d@redhat.com", i))
		}
		return &iampb.Policy{
			Version: 3,
			Etag:    []byte("etag"),
			Bindings: []*iampb.Binding{{
				Role:    config.GetSecretAccessorRole(),
				Members: members,
				Condition: &expr.Expr{
					Title:      GetSecretsViewerGroupConditionTitle("crowded"),
					Expression: BuildSecretAccessorRoleConditionExpression("crowded-collection"),
				},
			}},
		}
	}
	var tooManyMissingErrors []error
	var tooManySkipped []string
	var tooManyMembersLeft []string
	for i := 0; i <= maxUnresolvableGroups; i++ {
		email := fmt.Sprintf("g%02d@redhat.com", i)
		tooManyMissingErrors = append(tooManyMissingErrors, nonExistentGroup(email))
		if i < maxUnresolvableGroups {
			tooManySkipped = append(tooManySkipped, email)
		} else {
			tooManyMembersLeft = append(tooManyMembersLeft, "group:"+email)
		}
	}
	tooManyMembersLeft = append(tooManyMembersLeft, fmt.Sprintf("group:g%02d@redhat.com", maxUnresolvableGroups+1))

	testCases := []struct {
		name                 string
		policy               *iampb.Policy
		setPolicyErrors      []error
		expectError          bool
		expectedSkippedGroup []string
		expectedCalls        int
		expectedMembers      []string
	}{
		{
			name:            "policy applied on first try",
			setPolicyErrors: []error{nil},
			expectedCalls:   1,
			expectedMembers: []string{"group:exception-team@redhat.com", "group:good-team@redhat.com", "group:missing-team@redhat.com", "group:other-missing@redhat.com", "serviceAccount:collection-updater@test-project.iam.gserviceaccount.com"},
		},
		{
			name:                 "group without a Google group is dropped and the rest is applied",
			setPolicyErrors:      []error{nonExistentGroup("missing-team@redhat.com"), nil},
			expectedSkippedGroup: []string{"missing-team@redhat.com"},
			expectedCalls:        2,
			expectedMembers:      []string{"group:exception-team@redhat.com", "group:good-team@redhat.com", "group:other-missing@redhat.com", "serviceAccount:collection-updater@test-project.iam.gserviceaccount.com"},
		},
		{
			name:                 "every group without a Google group is dropped, shared bindings keep their other members",
			setPolicyErrors:      []error{nonExistentGroup("missing-team@redhat.com"), nonExistentGroup("other-missing@redhat.com"), nil},
			expectedSkippedGroup: []string{"missing-team@redhat.com", "other-missing@redhat.com"},
			expectedCalls:        3,
			expectedMembers:      []string{"group:exception-team@redhat.com", "group:good-team@redhat.com", "serviceAccount:collection-updater@test-project.iam.gserviceaccount.com"},
		},
		{
			name:            "concurrent policy change is not retried",
			setPolicyErrors: []error{status.Error(codes.FailedPrecondition, "etag mismatch")},
			expectError:     true,
			expectedCalls:   1,
			expectedMembers: []string{"group:exception-team@redhat.com", "group:good-team@redhat.com", "group:missing-team@redhat.com", "group:other-missing@redhat.com", "serviceAccount:collection-updater@test-project.iam.gserviceaccount.com"},
		},
		{
			name:            "unrelated invalid argument is not retried",
			setPolicyErrors: []error{status.Error(codes.InvalidArgument, "Role roles/nonexistent is not supported for this resource.")},
			expectError:     true,
			expectedCalls:   1,
			expectedMembers: []string{"group:exception-team@redhat.com", "group:good-team@redhat.com", "group:missing-team@redhat.com", "group:other-missing@redhat.com", "serviceAccount:collection-updater@test-project.iam.gserviceaccount.com"},
		},
		{
			name:            "message merely quoting the diagnostic does not drop a binding",
			setPolicyErrors: []error{status.Error(codes.InvalidArgument, "Invalid condition title 'Group good-team@redhat.com does not exist' for binding.")},
			expectError:     true,
			expectedCalls:   1,
			expectedMembers: []string{"group:exception-team@redhat.com", "group:good-team@redhat.com", "group:missing-team@redhat.com", "group:other-missing@redhat.com", "serviceAccount:collection-updater@test-project.iam.gserviceaccount.com"},
		},
		{
			name:            "rejected group that is not part of the policy does not loop",
			setPolicyErrors: []error{nonExistentGroup("not-in-policy@redhat.com")},
			expectError:     true,
			expectedCalls:   1,
			expectedMembers: []string{"group:exception-team@redhat.com", "group:good-team@redhat.com", "group:missing-team@redhat.com", "group:other-missing@redhat.com", "serviceAccount:collection-updater@test-project.iam.gserviceaccount.com"},
		},
		{
			name:                 "dropping stops once too many groups turn out to be unresolvable",
			policy:               newCrowdedPolicy(),
			setPolicyErrors:      tooManyMissingErrors,
			expectError:          true,
			expectedSkippedGroup: tooManySkipped,
			expectedCalls:        maxUnresolvableGroups + 1,
			expectedMembers:      tooManyMembersLeft,
		},
		{
			name:            "rejected group of a binding we do not manage is never dropped",
			setPolicyErrors: []error{nonExistentGroup("exception-team@redhat.com")},
			expectError:     true,
			expectedCalls:   1,
			expectedMembers: []string{"group:exception-team@redhat.com", "group:good-team@redhat.com", "group:missing-team@redhat.com", "group:other-missing@redhat.com", "serviceAccount:collection-updater@test-project.iam.gserviceaccount.com"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockClient := NewMockResourceManagerClient(mockCtrl)
			policy := tc.policy
			if policy == nil {
				policy = newPolicy()
			}
			actions := Actions{
				Config:                config,
				ConsolidatedIAMPolicy: policy,
				GroupCollections: map[string][]string{
					"good-team@redhat.com":     {"good-collection"},
					"missing-team@redhat.com":  {"missing-collection", "another-missing-collection"},
					"other-missing@redhat.com": {"collection"},
				},
			}

			callCount := 0
			mockClient.EXPECT().
				SetIamPolicy(gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, req *iampb.SetIamPolicyRequest, opts ...gax.CallOption) (*iampb.Policy, error) {
					if expected := GetProjectResourceIdNumber(config.ProjectIdNumber); req.Resource != expected {
						t.Errorf("expected resource %s, got %s", expected, req.Resource)
					}
					err := tc.setPolicyErrors[callCount]
					callCount++
					if err != nil {
						return nil, err
					}
					return req.Policy, nil
				}).
				Times(tc.expectedCalls)

			skippedGroups, err := actions.ApplyPolicy(context.Background(), mockClient)
			if tc.expectError && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tc.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if diff := cmp.Diff(tc.expectedSkippedGroup, skippedGroups); diff != "" {
				t.Errorf("skipped groups differ from expected, diff: %s", diff)
			}

			var members []string
			for _, binding := range actions.ConsolidatedIAMPolicy.Bindings {
				members = append(members, binding.Members...)
			}
			sort.Strings(members)
			if diff := cmp.Diff(tc.expectedMembers, members); diff != "" {
				t.Errorf("members left in the policy differ from expected, diff: %s", diff)
			}
		})
	}
}
