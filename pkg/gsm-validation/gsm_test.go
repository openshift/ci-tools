package gsmvalidation

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateCollectionName(t *testing.T) {
	testCases := []struct {
		name          string
		collection    string
		expectedValid bool
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
			name:          "valid collection name: multiphe hyphens",
			collection:    "test--collection",
			expectedValid: true,
		},
		{
			name:          "valid collection name: single character",
			collection:    "a",
			expectedValid: true,
		},
		{
			name:          "valid collection name: underscore",
			collection:    "some_collection",
			expectedValid: true,
		},
		{
			name:          "valid collection name: underscore at the beginning",
			collection:    "_name",
			expectedValid: true,
		},
		{
			name:          "valid collection name: multiple underscores",
			collection:    "some_collection_name",
			expectedValid: true,
		},
		{
			name:          "invalid collection name: uppercase letters",
			collection:    "Test-Collection",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: contains a slash",
			collection:    "test/collection",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: various special symbols",
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
			name:          "invalid collection name: dot",
			collection:    "test.collection",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: double underscores",
			collection:    "test__collection",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: triple underscores",
			collection:    "test___collection",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: double underscores at the end",
			collection:    "testcollection__",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: double underscores at the beginning",
			collection:    "__testcollection",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: hyphen at the end",
			collection:    "test-",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: underscore at the end",
			collection:    "test-collection_",
			expectedValid: false,
		},
		{
			name:          "invalid collection name: a single underscore",
			collection:    "_",
			expectedValid: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualValid := ValidateCollectionName(tc.collection)
			if actualValid != tc.expectedValid {
				t.Errorf("Expected %t, got %t for collection %q", tc.expectedValid, actualValid, tc.collection)
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
			name:          "valid secret name: underscores",
			secretName:    "updater_service_account",
			expectedValid: true,
		},
		{
			name:          "invalid secret name: underscore at the beginning",
			secretName:    "_updater_service_account",
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
		{
			name:          "invalid secret name: too long",
			secretName:    fmt.Sprintf("collection__%s", strings.Repeat("a", 243)),
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

func TestNormalizeSecretName(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "secret with dashes",
			input:    "simple-secret-name",
			expected: "simple-secret-name",
		},
		{
			name:     "dots only",
			input:    "secret.with.dots",
			expected: fmt.Sprintf("secret%swith%sdots", DotReplacementString, DotReplacementString),
		},
		{
			name:     "valid secret with underscores",
			input:    "secret_with_underscores",
			expected: "secret_with_underscores",
		},
		{
			name:     "dot in the middle of name",
			input:    "SECRET.NAME",
			expected: fmt.Sprintf("SECRET%sNAME", DotReplacementString),
		},
		{
			name:     "real world example",
			input:    "build_farm.cluster-init.build01.config",
			expected: fmt.Sprintf("build_farm%scluster-init%sbuild01%sconfig", DotReplacementString, DotReplacementString, DotReplacementString),
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "multiple consecutive dots",
			input:    "secret...name",
			expected: fmt.Sprintf("secret%s%s%sname", DotReplacementString, DotReplacementString, DotReplacementString),
		},
		{
			name:     "multiple consecutive underscores",
			input:    "secret___name",
			expected: "secret___name",
		},
		{
			name:     "dots and underscores together",
			input:    "secret._name",
			expected: fmt.Sprintf("secret%s_name", DotReplacementString),
		},
		{
			name:     "name with slash",
			input:    "secret/path",
			expected: fmt.Sprintf("secret%spath", SlashReplacementString),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := NormalizeName(tc.input)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}
