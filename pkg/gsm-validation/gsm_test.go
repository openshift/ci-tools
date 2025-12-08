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
			name:          "valid collection name: multiple hyphens",
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

func TestNormalizeName(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple secret with dashes",
			input:    "simple-secret-name",
			expected: "simple-secret-name",
		},
		{
			name:     "dots only",
			input:    "secret.with.dots",
			expected: fmt.Sprintf("secret%swith%sdots", DotReplacementString, DotReplacementString),
		},
		{
			name:     "underscores get encoded",
			input:    "secret_with_underscores",
			expected: fmt.Sprintf("secret%swith%sunderscores", UnderscoreReplacementString, UnderscoreReplacementString),
		},
		{
			name:     "dot in the middle of name",
			input:    "SECRET.NAME",
			expected: fmt.Sprintf("SECRET%sNAME", DotReplacementString),
		},
		{
			name:     "real world example with underscores and dots",
			input:    "build_farm.cluster-init.build01.config",
			expected: fmt.Sprintf("build%sfarm%scluster-init%sbuild01%sconfig", UnderscoreReplacementString, DotReplacementString, DotReplacementString, DotReplacementString),
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
			expected: fmt.Sprintf("secret%s%s%sname", UnderscoreReplacementString, UnderscoreReplacementString, UnderscoreReplacementString),
		},
		{
			name:     "dots and underscores together",
			input:    "secret._name",
			expected: fmt.Sprintf("secret%s%sname", DotReplacementString, UnderscoreReplacementString),
		},
		{
			name:     "name with slash",
			input:    "secret/path",
			expected: fmt.Sprintf("secret%spath", SlashReplacementString),
		},
		{
			name:     "backwards compatibility: aws_creds",
			input:    "aws_creds",
			expected: fmt.Sprintf("aws%screds", UnderscoreReplacementString),
		},
		{
			name:     ".dockerconfigjson",
			input:    ".dockerconfigjson",
			expected: fmt.Sprintf("%sdockerconfigjson", DotReplacementString),
		},
		{
			name:     "complex field name",
			input:    "sa.ci-operator.build01.config",
			expected: fmt.Sprintf("sa%sci-operator%sbuild01%sconfig", DotReplacementString, DotReplacementString, DotReplacementString),
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

func TestDenormalizeName(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "decode underscores",
			input:    fmt.Sprintf("aws%screds", UnderscoreReplacementString),
			expected: "aws_creds",
		},
		{
			name:     "decode dots",
			input:    fmt.Sprintf("%sdockerconfigjson", DotReplacementString),
			expected: ".dockerconfigjson",
		},
		{
			name:     "decode complex field name",
			input:    fmt.Sprintf("sa%sci-operator%sbuild01%sconfig", DotReplacementString, DotReplacementString, DotReplacementString),
			expected: "sa.ci-operator.build01.config",
		},
		{
			name:     "decode mixed characters",
			input:    fmt.Sprintf("build%sfarm%scluster-init%sbuild01%sconfig", UnderscoreReplacementString, DotReplacementString, DotReplacementString, DotReplacementString),
			expected: "build_farm.cluster-init.build01.config",
		},
		{
			name:     "no encoding to decode",
			input:    "simple-name",
			expected: "simple-name",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := DenormalizeName(tc.input)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestValidateGroupName(t *testing.T) {
	testCases := []struct {
		name          string
		group         string
		expectedValid bool
	}{
		{
			name:          "valid group: single segment",
			group:         "group",
			expectedValid: true,
		},
		{
			name:          "valid group: multi-level",
			group:         "group/b/c",
			expectedValid: true,
		},
		{
			name:          "valid group: with hyphens",
			group:         "my-group/sub-group",
			expectedValid: true,
		},
		{
			name:          "valid group: with numbers",
			group:         "group1/group2/group3",
			expectedValid: true,
		},
		{
			name:          "valid group: single character segments",
			group:         "a/b/c",
			expectedValid: true,
		},
		{
			name:          "valid group: starts with number",
			group:         "123group/456test",
			expectedValid: true,
		},
		{
			name:          "valid group: hyphen in middle",
			group:         "my-group",
			expectedValid: true,
		},
		{
			name:          "valid group: multiple hyphens",
			group:         "my--group",
			expectedValid: true,
		},
		{
			name:          "valid group: long path",
			group:         "a/b/c/d/e/f/g",
			expectedValid: true,
		},
		{
			name:          "invalid group: empty string",
			group:         "",
			expectedValid: false,
		},
		{
			name:          "invalid group: leading slash",
			group:         "/group",
			expectedValid: false,
		},
		{
			name:          "invalid group: trailing slash",
			group:         "group/",
			expectedValid: false,
		},
		{
			name:          "invalid group: double slash",
			group:         "a/b//c",
			expectedValid: false,
		},
		{
			name:          "invalid group: contains underscore",
			group:         "group_name",
			expectedValid: false,
		},
		{
			name:          "invalid group: underscore in path",
			group:         "group/sub_group",
			expectedValid: false,
		},
		{
			name:          "invalid group: uppercase letters",
			group:         "Group/Name",
			expectedValid: false,
		},
		{
			name:          "invalid group: special characters",
			group:         "group@name",
			expectedValid: false,
		},
		{
			name:          "invalid group: starts with hyphen",
			group:         "-group",
			expectedValid: false,
		},
		{
			name:          "invalid group: ends with hyphen",
			group:         "group-",
			expectedValid: false,
		},
		{
			name:          "invalid group: segment starts with hyphen",
			group:         "group/-segment",
			expectedValid: false,
		},
		{
			name:          "invalid group: segment ends with hyphen",
			group:         "group/segment-",
			expectedValid: false,
		},
		{
			name:          "invalid group: just a slash",
			group:         "/",
			expectedValid: false,
		},
		{
			name:          "invalid group: just a hyphen",
			group:         "-",
			expectedValid: false,
		},
		{
			name:          "invalid group: spaces",
			group:         "group name",
			expectedValid: false,
		},
		{
			name:          "invalid group: dots",
			group:         "group.name",
			expectedValid: false,
		},
		{
			name:          "invalid group: ends with underscore",
			group:         "group_",
			expectedValid: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualValid := ValidateGroupName(tc.group)
			if actualValid != tc.expectedValid {
				t.Errorf("Expected %t, got %t for group %q", tc.expectedValid, actualValid, tc.group)
			}
		})
	}
}
