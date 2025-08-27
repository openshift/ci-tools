package gsmsecrets

import (
	"testing"
)

func TestGetUpdaterSAEmail(t *testing.T) {
	config := Config{
		ProjectIdString: "test-project",
		ProjectIdNumber: "123456789",
	}

	testCases := []struct {
		name          string
		collection    string
		expectedEmail string
	}{
		{
			name:          "standard collection",
			collection:    "alpha-secrets",
			expectedEmail: "alpha-secrets-updater@test-project.iam.gserviceaccount.com",
		},
		{
			name:          "single word collection",
			collection:    "beta",
			expectedEmail: "beta-updater@test-project.iam.gserviceaccount.com",
		},
		{
			name:          "collection with numbers",
			collection:    "test-collection-123",
			expectedEmail: "test-collection-123-updater@test-project.iam.gserviceaccount.com",
		},
		{
			name:          "one letter collection name",
			collection:    "a",
			expectedEmail: "a-updater@test-project.iam.gserviceaccount.com",
		},
		{
			name:          "collection with multiple hyphens",
			collection:    "my-test-collection-name",
			expectedEmail: "my-test-collection-name-updater@test-project.iam.gserviceaccount.com",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetUpdaterSAEmail(tc.collection, config)
			if actual != tc.expectedEmail {
				t.Errorf("Expected %q, got %q", tc.expectedEmail, actual)
			}
		})
	}
}

func TestGetUpdaterSAId(t *testing.T) {
	testCases := []struct {
		name        string
		displayName string
		expectedId  string
	}{
		{
			name:        "standard display name",
			displayName: "alpha-secrets",
			expectedId:  "alpha-secrets-updater",
		},
		{
			name:        "single word display name",
			displayName: "beta",
			expectedId:  "beta-updater",
		},
		{
			name:        "display name with numbers",
			displayName: "test-collection-123",
			expectedId:  "test-collection-123-updater",
		},
		{
			name:        "display name with multiple hyphens",
			displayName: "my-test-collection-name",
			expectedId:  "my-test-collection-name-updater",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetUpdaterSAId(tc.displayName)
			if actual != tc.expectedId {
				t.Errorf("Expected %q, got %q", tc.expectedId, actual)
			}
		})
	}
}

func TestGetIndexSecretName(t *testing.T) {
	testCases := []struct {
		name               string
		collection         string
		expectedSecretName string
	}{
		{
			name:               "standard collection",
			collection:         "alpha-secrets",
			expectedSecretName: "alpha-secrets____index",
		},
		{
			name:               "single word collection",
			collection:         "beta",
			expectedSecretName: "beta____index",
		},
		{
			name:               "collection with numbers",
			collection:         "test-collection-123",
			expectedSecretName: "test-collection-123____index",
		},
		{
			name:               "short collection name",
			collection:         "a",
			expectedSecretName: "a____index",
		},
		{
			name:               "empty collection",
			collection:         "",
			expectedSecretName: "____index",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetIndexSecretName(tc.collection)
			if actual != tc.expectedSecretName {
				t.Errorf("Expected %q, got %q", tc.expectedSecretName, actual)
			}
		})
	}
}

func TestGetSecretID(t *testing.T) {
	testCases := []struct {
		name       string
		secretName string
		expectedID string
	}{
		{
			name:       "full GCP secret resource name",
			secretName: "projects/openshift-ci-secrets/secrets/alpha-secrets__updater-service-account",
			expectedID: "alpha-secrets__updater-service-account",
		},
		{
			name:       "index secret resource name",
			secretName: "projects/test-project/secrets/beta____index",
			expectedID: "beta____index",
		},
		{
			name:       "generic secret resource name",
			secretName: "projects/my-project/secrets/collection__my-secret",
			expectedID: "collection__my-secret",
		},
		{
			name:       "production secret example",
			secretName: "projects/123456789012/secrets/shared-collection__updater-service-account",
			expectedID: "shared-collection__updater-service-account",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetSecretID(tc.secretName)
			if actual != tc.expectedID {
				t.Errorf("Expected %q, got %q", tc.expectedID, actual)
			}
		})
	}
}
