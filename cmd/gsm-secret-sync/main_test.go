package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"

	"cloud.google.com/go/iam/admin/apiv1/adminpb"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"go.uber.org/mock/gomock"

	"github.com/google/go-cmp/cmp"
	gax "github.com/googleapis/gax-go/v2"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestGetDesiredState(t *testing.T) {
	testCases := []struct {
		name       string
		configFile string
	}{
		{
			name:       "basic config",
			configFile: "testdata/basic-config.yaml",
		},
		{
			name:       "no secret collections",
			configFile: "testdata/no-secret-collections.yaml",
		},
		{
			name:       "one secret collection",
			configFile: "testdata/one-secret-collection.yaml",
		},
		{
			name:       "complex config",
			configFile: "testdata/complex-config.yaml",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			o := &options{
				configFile: tc.configFile,
			}

			serviceAccounts, secrets, bindings, collections, err := o.GetDesiredState()
			if err != nil {
				t.Fatalf("GetDesiredState() failed: %v", err)
			}

			testhelper.CompareWithFixture(t, serviceAccounts, testhelper.WithPrefix("sa-"))
			testhelper.CompareWithFixture(t, secrets, testhelper.WithPrefix("secrets-"))
			testhelper.CompareWithFixture(t, bindings, testhelper.WithPrefix("bindings-"))
			testhelper.CompareWithFixture(t, collections, testhelper.WithPrefix("collections-"))
		})
	}
}

func TestGenerateServiceAccountKey(t *testing.T) {
	testCases := []struct {
		name        string
		saEmail     string
		mockKeyData []byte
		mockError   error
		expectError bool
	}{
		{
			name:        "successful key generation",
			saEmail:     getUpdaterSAEmail("test-collection"),
			mockKeyData: []byte("fake-private-key-data"),
			mockError:   nil,
			expectError: false,
		},
		{
			name:        "IAM client error",
			saEmail:     getUpdaterSAEmail("test-collection"),
			mockKeyData: nil,
			mockError:   errors.New("some GCP error"),
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockIAMClient := NewMockIAMClient(mockCtrl)

			keyRequest := &adminpb.CreateServiceAccountKeyRequest{
				Name: fmt.Sprintf("%s/serviceAccounts/%s", getProjectResourceString(), tc.saEmail),
			}

			if tc.mockError != nil {
				mockIAMClient.EXPECT().
					CreateServiceAccountKey(gomock.Any(), keyRequest).
					Return(nil, tc.mockError).
					Times(1)
			} else {
				mockIAMClient.EXPECT().
					CreateServiceAccountKey(gomock.Any(), keyRequest).
					Return(&adminpb.ServiceAccountKey{
						PrivateKeyData: tc.mockKeyData,
					}, nil).
					Times(1)
			}

			result, actualErr := generateServiceAccountKey(context.Background(), mockIAMClient, tc.saEmail)

			if tc.expectError {
				if actualErr == nil {
					t.Errorf("Expected error but got none")
				}
				if diff := cmp.Diff(tc.mockError, actualErr, testhelper.EquateErrorMessage); diff != "" {
					t.Errorf("%s: mismatch (-expected +actual), diff: %s", tc.name, diff)
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
	collection := "test-collection"
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
					Email:       getUpdaterSAEmail(collection),
					DisplayName: getUpdaterSAId(collection),
					Collection:  collection,
				},
			},
			secretsToCreate: map[string]GCPSecret{
				getUpdaterSASecretName(collection): {
					Name:       getUpdaterSASecretName(collection),
					Type:       SecretTypeSA,
					Collection: collection,
				},
			},
			expectedSecretsRemaining: 1,
			expectPayloadSet:         true,
		},
		{
			name: "CreateServiceAccount fails - secret should be removed",
			serviceAccountsToCreate: map[string]ServiceAccountInfo{
				collection: {
					Email:       getUpdaterSAEmail(collection),
					DisplayName: getUpdaterSAId(collection),
					Collection:  collection,
				},
			},
			secretsToCreate: map[string]GCPSecret{
				getUpdaterSASecretName(collection): {
					Name:       getUpdaterSASecretName(collection),
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
					Email:       getUpdaterSAEmail(collection),
					DisplayName: getUpdaterSAId(collection),
					Collection:  collection,
				},
			},
			secretsToCreate: map[string]GCPSecret{
				getUpdaterSASecretName(collection): {
					Name:       getUpdaterSASecretName(collection),
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
					Email:       getUpdaterSAEmail(collection),
					DisplayName: getUpdaterSAId(collection),
					Collection:  collection,
				},
				"another-collection": {
					Email:       getUpdaterSAEmail("another-collection"),
					DisplayName: getUpdaterSAId("another-collection"),
					Collection:  "another-collection",
				},
			},
			secretsToCreate: map[string]GCPSecret{
				getUpdaterSASecretName(collection): {
					Name:       getUpdaterSASecretName(collection),
					Type:       SecretTypeSA,
					Collection: collection,
				},
				getUpdaterSASecretName("another-collection"): {
					Name:       getUpdaterSASecretName("another-collection"),
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

			for _, sa := range tc.serviceAccountsToCreate {
				createSARequest := &adminpb.CreateServiceAccountRequest{
					Name:      getProjectResourceString(),
					AccountId: sa.DisplayName,
					ServiceAccount: &adminpb.ServiceAccount{
						DisplayName: sa.DisplayName,
					},
				}

				if tc.clientCreateSAError != nil { // client.CreateServiceAccount fails scenario
					mockIAMClient.EXPECT().
						CreateServiceAccount(gomock.Any(), createSARequest).
						Return(nil, tc.clientCreateSAError).
						Times(1)
				} else { // client.CreateServiceAccount succeeds scenario
					mockIAMClient.EXPECT().
						CreateServiceAccount(gomock.Any(), createSARequest).
						Return(&adminpb.ServiceAccount{
							Email:       sa.Email,
							DisplayName: sa.DisplayName,
						}, nil).
						Times(1)

					// Set up key generation expectation
					keyRequest := &adminpb.CreateServiceAccountKeyRequest{
						Name: fmt.Sprintf("%s/serviceAccounts/%s", getProjectResourceString(), sa.Email),
					}

					if tc.clientGenerateKeyError != nil { // client.CreateServiceAccountKey fails scenario
						mockIAMClient.EXPECT().
							CreateServiceAccountKey(gomock.Any(), keyRequest).
							Return(nil, tc.clientGenerateKeyError).
							Times(1)
					} else { // client.CreateServiceAccountKey succeeds scenario
						mockIAMClient.EXPECT().
							CreateServiceAccountKey(gomock.Any(), keyRequest).
							Return(&adminpb.ServiceAccountKey{
								PrivateKeyData: []byte("generated-key-data-for-" + sa.Collection),
							}, nil).
							Times(1)
					}
				}
			}

			secretsCopy := make(map[string]GCPSecret)
			maps.Copy(secretsCopy, tc.secretsToCreate)

			actions := &Actions{
				SAsToCreate:     tc.serviceAccountsToCreate,
				SecretsToCreate: secretsCopy,
			}

			actions.createServiceAccounts(context.Background(), mockIAMClient)

			if len(actions.SecretsToCreate) != tc.expectedSecretsRemaining {
				t.Errorf("Expected %d secrets remaining, got %d", tc.expectedSecretsRemaining, len(actions.SecretsToCreate))
			}

			if tc.expectPayloadSet {
				for _, sa := range tc.serviceAccountsToCreate {
					secretName := getUpdaterSASecretName(sa.Collection)
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
				"test-collection__updater-service-account": {
					Name:       "test-collection__updater-service-account",
					Type:       SecretTypeSA,
					Collection: "test-collection",
				},
				"test-collection____index": {
					Name:       "test-collection____index",
					Type:       SecretTypeIndex,
					Collection: "test-collection",
				},
			},
			numberOfSASecretsWithoutPayload: 1,
		},
		{
			name: "create one service account secret",
			secrets: map[string]GCPSecret{
				"test-collection__updater-service-account": {
					Name:       "test-collection__updater-service-account",
					Type:       SecretTypeSA,
					Collection: "test-collection",
				},
			},
			numberOfSASecretsWithoutPayload: 1,
		},
		{
			name: "create one index secret",
			secrets: map[string]GCPSecret{
				"test-collection____index": {
					Name:       "test-collection____index",
					Type:       SecretTypeIndex,
					Collection: "test-collection",
				},
			},
			numberOfSASecretsWithoutPayload: 0,
		},
		{
			name: "multiple secrets",
			secrets: map[string]GCPSecret{
				"test-collection__updater-service-account": {
					Name:       "test-collection__updater-service-account",
					Type:       SecretTypeSA,
					Collection: "test-collection",
				},
				"test-collection____index": {
					Name:       "test-collection____index",
					Type:       SecretTypeIndex,
					Collection: "test-collection",
				},
				"another-collection__some-secret": {
					Name:       "another-collection__updater-service-account",
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
						Name:        fmt.Sprintf("%s/secrets/%s", getProjectResourceIdNumber(), req.SecretId),
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
				SecretsToCreate: secretsCopy,
			}
			actions.createSecrets(context.Background(), mockSecretsClient, mockIAMClient)

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

func TestValidateSecretName(t *testing.T) {
	testCases := []struct {
		name          string
		secretName    string
		expectedValid bool
	}{
		{
			name:          "valid secret name: updater-service-account",
			secretName:    "updater-service-account",
			expectedValid: true,
		},
		{
			name:          "valid secret name: mixed case",
			secretName:    "UpdaterServiceAccount",
			expectedValid: true,
		},
		{
			name:          "valid secret name: numbers",
			secretName:    "secret123",
			expectedValid: true,
		},
		{
			name:          "valid secret name: hyphens",
			secretName:    "my-secret-name",
			expectedValid: true,
		},
		{
			name:          "valid secret name: single character",
			secretName:    "A",
			expectedValid: true,
		},
		{
			name:          "valid secret name: uppercase",
			secretName:    "UPPERCASE",
			expectedValid: true,
		},
		{
			name:          "invalid secret name: underscores",
			secretName:    "updater_service_account",
			expectedValid: false,
		},
		{
			name:          "invalid secret name: special characters",
			secretName:    "!123symbols",
			expectedValid: false,
		},
		{
			name:          "invalid secret name: spaces",
			secretName:    "my secret",
			expectedValid: false,
		},
		{
			name:          "invalid secret name: dots",
			secretName:    "my.secret",
			expectedValid: false,
		},
		{
			name:          "invalid secret name: empty string",
			secretName:    "",
			expectedValid: false,
		},
		{
			name:          "invalid secret name: double underscores",
			secretName:    "secret__name",
			expectedValid: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualValid := validateSecretName(tc.secretName)
			if actualValid != tc.expectedValid {
				t.Errorf("Expected %t, got %t for secret name %q", tc.expectedValid, actualValid, tc.secretName)
			}
		})
	}
}

func TestValidateCollectionName(t *testing.T) {
	testCases := []struct {
		name           string
		collection     string
		expectedValid  bool
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualValid := validateCollectionName(tc.collection)
			if actualValid != tc.expectedValid {
				t.Errorf("Expected %t, got %t for collection %q", tc.expectedValid, actualValid, tc.collection)
			}
		})
	}
}

func TestExtractCollectionFromSecretName(t *testing.T) {
	testCases := []struct {
		name               string
		secretName         string
		expectedCollection string
	}{
		{
			name:               "correct secret name: updater service account",
			secretName:         "test-collection__updater-service-account",
			expectedCollection: "test-collection",
		},
		{
			name:               "correct secret name: index",
			secretName:         "test-collection____index",
			expectedCollection: "test-collection",
		},
		{
			name:               "malformed secret name: too many __",
			secretName:         "test-collection__updater-service-account__malformed",
			expectedCollection: "",
		},
		{
			name:               "incorrect secret name: index with only __ at the end",
			secretName:         "test-collection____index__",
			expectedCollection: "",
		},
		{
			name:               "incorrect secret name: string after __index",
			secretName:         "test-collection____index__something-else",
			expectedCollection: "",
		},
		{
			name:               "incorrect secret name: index with concatenated string",
			secretName:         "test-collection____indexsomethingelse",
			expectedCollection: "",
		},
		{
			name:               "incorrect secret name: wrong symbols in secret name",
			secretName:         "test-collection__!123symbols",
			expectedCollection: "",
		},
		{
			name:               "malformed secret name: no __",
			secretName:         "test-collectionupdater-service-account",
			expectedCollection: "",
		},
		{
			name:               "malformed secret name: no __ simple chars",
			secretName:         "testaccount",
			expectedCollection: "",
		},
		{
			name:               "malformed secret name: empty string",
			secretName:         "",
			expectedCollection: "",
		},
		{
			name:               "malformed secret name: strange characters",
			secretName:         "!4@#$%^&*()_+__some-secret",
			expectedCollection: "",
		},
		{
			name:               "malformed secret name: __ at the start",
			secretName:         "__test-collection__updater-service-account",
			expectedCollection: "",
		},
		{
			name:               "malformed secret name: __ at the end",
			secretName:         "test-collection____index__",
			expectedCollection: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualCollection := extractCollectionFromSecretName(tc.secretName)
			if actualCollection != tc.expectedCollection {
				t.Errorf("Expected collection %q, got %q", tc.expectedCollection, actualCollection)
			}
		})
	}
}
