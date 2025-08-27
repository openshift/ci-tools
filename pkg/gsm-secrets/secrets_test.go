package gsmsecrets

import (
	"fmt"
	"testing"
)

func TestClassifySecret(t *testing.T) {
	testCases := []struct {
		name       string
		secretName string
		expected   SecretType
	}{
		{
			name:       "updater-sa secret is classified as SA",
			secretName: "collection1__updater-service-account",
			expected:   SecretTypeSA,
		},
		{
			name:       "index secret is classified as index",
			secretName: "collection1____index",
			expected:   SecretTypeIndex,
		},
		{
			name:       "secret is classified a common secret",
			secretName: "collection1__some-random-secret",
			expected:   SecretTypeGeneric,
		},
		{
			name:       "secret is classified as unknown",
			secretName: "some-random-secret",
			expected:   SecretTypeUnknown,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := ClassifySecret(tc.secretName)
			if actual != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, actual)
			}
		})
	}
}

func TestVerifyIndexSecretContent(t *testing.T) {
	testCases := []struct {
		name          string
		payload       []byte
		expectedError error
	}{
		{
			name:    "test-collection-updater-sa",
			payload: fmt.Appendf(nil, "- updater-service-account"),
		},
		{
			name:    "test-collection-updater-sa-with-newline",
			payload: fmt.Appendf(nil, "- updater-service-account\n"),
		},
		{
			name:          "test-collection-updater-sa-with-multiple-lines",
			payload:       fmt.Appendf(nil, "- updater-service-account\n- another-service-account"),
			expectedError: fmt.Errorf("index secret content mismatch: expected %q, got %q", "- updater-service-account\n- another-service-account", "- updater-service-account\n- another-service-account"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyIndexSecretContent(tc.payload)
			if tc.expectedError != nil {
				if err == nil {
					t.Fatalf("verifyIndexSecretContent should have failed: %v", tc.expectedError)
				}
			} else {
				if err != nil {
					t.Fatalf("verifyIndexSecretContent failed: %v", err)
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
			actualValid := ValidateSecretName(tc.secretName)
			if actualValid != tc.expectedValid {
				t.Errorf("Expected %t, got %t for secret name %q", tc.expectedValid, actualValid, tc.secretName)
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
			actualCollection := ExtractCollectionFromSecretName(tc.secretName)
			if actualCollection != tc.expectedCollection {
				t.Errorf("Expected collection %q, got %q", tc.expectedCollection, actualCollection)
			}
		})
	}
}
