package gsmsecrets

import (
	"fmt"
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
			expectedEmail: fmt.Sprintf("alpha-secrets%s@test-project.iam.gserviceaccount.com", ServiceAccountIDSuffix),
		},
		{
			name:          "single word collection",
			collection:    "beta",
			expectedEmail: fmt.Sprintf("beta%s@test-project.iam.gserviceaccount.com", ServiceAccountIDSuffix),
		},
		{
			name:          "collection with numbers",
			collection:    "test-collection-123",
			expectedEmail: fmt.Sprintf("test-collection-123%s@test-project.iam.gserviceaccount.com", ServiceAccountIDSuffix),
		},
		{
			name:          "one letter collection name",
			collection:    "a",
			expectedEmail: fmt.Sprintf("a%s@test-project.iam.gserviceaccount.com", ServiceAccountIDSuffix),
		},
		{
			name:          "collection with multiple hyphens",
			collection:    "my-test-collection-name",
			expectedEmail: fmt.Sprintf("my-test-collection-name%s@test-project.iam.gserviceaccount.com", ServiceAccountIDSuffix),
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
			expectedId:  fmt.Sprintf("alpha-secrets%s", ServiceAccountIDSuffix),
		},
		{
			name:        "single word display name",
			displayName: "beta",
			expectedId:  fmt.Sprintf("beta%s", ServiceAccountIDSuffix),
		},
		{
			name:        "display name with numbers",
			displayName: "test-collection-123",
			expectedId:  fmt.Sprintf("test-collection-123%s", ServiceAccountIDSuffix),
		},
		{
			name:        "display name with multiple hyphens",
			displayName: "my-test-collection-name",
			expectedId:  fmt.Sprintf("my-test-collection-name%s", ServiceAccountIDSuffix),
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
