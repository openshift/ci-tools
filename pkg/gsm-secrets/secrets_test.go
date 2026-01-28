package gsmsecrets

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
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

func TestExtractCollectionFromSecretName(t *testing.T) {
	testCases := []struct {
		name               string
		secretName         string
		expectedCollection string
	}{
		{
			name:               "2-level: updater service account",
			secretName:         "test-collection__updater-service-account",
			expectedCollection: "test-collection",
		},
		{
			name:               "2-level: index",
			secretName:         "test-collection____index",
			expectedCollection: "test-collection",
		},
		{
			name:               "2-level: simple field",
			secretName:         "my-creds__password",
			expectedCollection: "my-creds",
		},
		{
			name:               "3-level: collection, group, field",
			secretName:         "vsphere__ibmcloud__username",
			expectedCollection: "vsphere",
		},
		{
			name:               "4-level: deep hierarchy",
			secretName:         "telcov10n-ci-network__clusters__hlxcl8__password",
			expectedCollection: "telcov10n-ci-network",
		},
		{
			name:               "5-level: very deep hierarchy",
			secretName:         "my-creds__a__b__c__field",
			expectedCollection: "my-creds",
		},
		{
			name:               "incorrect: index with trailing __",
			secretName:         "test-collection____index__",
			expectedCollection: "",
		},
		{
			name:               "incorrect: string after __index",
			secretName:         "test-collection____index__something-else",
			expectedCollection: "",
		},
		{
			name:               "incorrect: index with concatenated string",
			secretName:         "test-collection____indexsomethingelse",
			expectedCollection: "",
		},
		{
			name:               "malformed: no __",
			secretName:         "test-collectionupdater-service-account",
			expectedCollection: "",
		},
		{
			name:               "malformed: no __ simple chars",
			secretName:         "testaccount",
			expectedCollection: "",
		},
		{
			name:               "malformed: empty string",
			secretName:         "",
			expectedCollection: "",
		},
		{
			name:               "malformed: invalid collection characters",
			secretName:         "!4@#$%^&*()_+__some-secret",
			expectedCollection: "",
		},
		{
			name:               "malformed: __ at the start",
			secretName:         "__test-collection__updater-service-account",
			expectedCollection: "",
		},
		{
			name:               "malformed: only delimiter",
			secretName:         "__",
			expectedCollection: "",
		},
		{
			name:               "malformed: empty parts",
			secretName:         "collection____",
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

func TestParseIndexSecretContent(t *testing.T) {
	testCases := []struct {
		name     string
		content  []byte
		expected []string
	}{
		{
			name:     "empty content",
			content:  []byte{},
			expected: nil,
		},
		{
			name:     "single entry",
			content:  []byte("- secret1"),
			expected: []string{"secret1"},
		},
		{
			name:     "single entry with group",
			content:  []byte("- group1__secret1"),
			expected: []string{"group1__secret1"},
		},
		{
			name:     "multiple entries",
			content:  []byte("- secret1\n- group1__secret2\n- group1__secret3"),
			expected: []string{"secret1", "group1__secret2", "group1__secret3"},
		},
		{
			name:     "filters out updater service account",
			content:  []byte("- secret1\n- updater-service-account\n- group1__secret2"),
			expected: []string{"secret1", "group1__secret2"},
		},
		{
			name:     "handles empty lines",
			content:  []byte("- secret1\n\n- secret2\n\n"),
			expected: []string{"secret1", "secret2"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := ParseIndexSecretContent(tc.content)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("mismatch (-expected, +actual):\n%s", diff)
			}
		})
	}
}
