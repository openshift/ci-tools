package gsmsecrets

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"testing"
	"time"

	"cloud.google.com/go/iam/admin/apiv1/adminpb"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	gax "github.com/googleapis/gax-go/v2"
	"go.uber.org/mock/gomock"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"k8s.io/apimachinery/pkg/util/wait"
)

func TestGenerateServiceAccountKey(t *testing.T) {
	config := Config{
		ProjectIdString: "test-project",
		ProjectIdNumber: "123456789",
	}

	testCases := []struct {
		name                    string
		saEmail                 string
		mockKeyData             []byte
		firstCreateKeyError     error
		secondCreateKeyError    error
		getServiceAccountErrors []error
		expectError             bool
		expectedCreateKeyCalls  int
		expectedGetSACalls      int
	}{
		{
			name:                   "successful key generation on first try",
			saEmail:                GetUpdaterSAEmail("test-collection", config),
			mockKeyData:            []byte("fake-private-key-data"),
			firstCreateKeyError:    nil,
			expectError:            false,
			expectedCreateKeyCalls: 1,
			expectedGetSACalls:     0,
		},
		{
			name:                   "non-retryable IAM client error",
			saEmail:                GetUpdaterSAEmail("test-collection", config),
			mockKeyData:            nil,
			firstCreateKeyError:    errors.New("some non-retryable GCP error"),
			expectError:            true,
			expectedCreateKeyCalls: 1,
			expectedGetSACalls:     0,
		},
		{
			name:                "retryable NotFound error - eventual success",
			saEmail:             GetUpdaterSAEmail("test-collection", config),
			mockKeyData:         []byte("fake-private-key-data"),
			firstCreateKeyError: status.Error(codes.NotFound, "service account not found"),
			getServiceAccountErrors: []error{
				status.Error(codes.NotFound, "service account not found"),
				status.Error(codes.NotFound, "service account not found"),
				nil, // Success on third check
			},
			secondCreateKeyError:   nil,
			expectError:            false,
			expectedCreateKeyCalls: 2, // First attempt + retry after SA becomes available
			expectedGetSACalls:     3, // Three checks for SA availability
		},
		{
			name:                "retryable NotFound error - all checks fail",
			saEmail:             GetUpdaterSAEmail("test-collection", config),
			mockKeyData:         nil,
			firstCreateKeyError: status.Error(codes.NotFound, "service account not found"),
			getServiceAccountErrors: []error{
				status.Error(codes.NotFound, "service account not found"),
				status.Error(codes.NotFound, "service account not found"),
				status.Error(codes.NotFound, "service account not found"),
			},
			expectError:            true,
			expectedCreateKeyCalls: 1, // Only first attempt
			expectedGetSACalls:     3, // Three failed checks
		},
		{
			name:                "retryable HTTP 404 error - eventual success",
			saEmail:             GetUpdaterSAEmail("test-collection", config),
			mockKeyData:         []byte("fake-private-key-data"),
			firstCreateKeyError: &googleapi.Error{Code: http.StatusNotFound, Message: "service account not found"},
			getServiceAccountErrors: []error{
				nil, // SA becomes available immediately
			},
			secondCreateKeyError:   nil,
			expectError:            false,
			expectedCreateKeyCalls: 2, // First attempt + retry
			expectedGetSACalls:     1, // One successful check
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
			getRequest := &adminpb.GetServiceAccountRequest{
				Name: fmt.Sprintf("%s/serviceAccounts/%s", GetProjectResourceString(config.ProjectIdString), tc.saEmail),
			}

			// Set up first CreateServiceAccountKey call
			firstCall := mockIAMClient.EXPECT().
				CreateServiceAccountKey(gomock.Any(), keyRequest).
				Return(nil, tc.firstCreateKeyError)

			// Set up GetServiceAccount calls if needed
			if tc.expectedGetSACalls > 0 {
				getCallCount := 0
				mockIAMClient.EXPECT().
					GetServiceAccount(gomock.Any(), getRequest).
					DoAndReturn(func(ctx context.Context, req *adminpb.GetServiceAccountRequest, opts ...gax.CallOption) (*adminpb.ServiceAccount, error) {
						if getCallCount < len(tc.getServiceAccountErrors) {
							err := tc.getServiceAccountErrors[getCallCount]
							getCallCount++
							return nil, err
						}
						return nil, errors.New("unexpected GetServiceAccount call")
					}).
					Times(tc.expectedGetSACalls)
			}

			// Set up second CreateServiceAccountKey call if needed
			if tc.expectedCreateKeyCalls > 1 {
				secondCall := mockIAMClient.EXPECT().
					CreateServiceAccountKey(gomock.Any(), keyRequest).
					After(firstCall)

				if tc.secondCreateKeyError != nil {
					secondCall.Return(nil, tc.secondCreateKeyError)
				} else {
					secondCall.Return(&adminpb.ServiceAccountKey{
						PrivateKeyData: tc.mockKeyData,
					}, nil)
				}
			} else if tc.firstCreateKeyError == nil {
				// First call succeeded, return the key data
				firstCall.Return(&adminpb.ServiceAccountKey{
					PrivateKeyData: tc.mockKeyData,
				}, tc.firstCreateKeyError)
			}

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
