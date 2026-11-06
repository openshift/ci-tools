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
			name:          "invalid collection name: underscore at the beginning",
			collection:    "_name",
			expectedValid: false,
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

func TestParseSecretPath(t *testing.T) {
	testCases := []struct {
		name          string
		collection    string
		path          string
		expectedGroup string
		expectedField string
		expectedOK    bool
	}{
		{
			name:          "simple group",
			collection:    "team",
			path:          "aws/password",
			expectedGroup: "aws",
			expectedField: "password",
			expectedOK:    true,
		},
		{
			name:          "hierarchical group",
			collection:    "team",
			path:          "aws/prod/password",
			expectedGroup: "aws/prod",
			expectedField: "password",
			expectedOK:    true,
		},
		{
			name:          "encoded names",
			collection:    "team",
			path:          "--dot--config/token_1",
			expectedGroup: "--dot--config",
			expectedField: "token_1",
			expectedOK:    true,
		},
		{
			name:       "empty collection",
			collection: "",
			path:       "aws/password",
		},
		{
			name:       "collection contains delimiter",
			collection: "team__other",
			path:       "aws/password",
		},
		{
			name:       "empty path",
			collection: "team",
			path:       "",
		},
		{
			name:       "missing group",
			collection: "team",
			path:       "password",
		},
		{
			name:       "empty group",
			collection: "team",
			path:       "/password",
		},
		{
			name:       "empty field",
			collection: "team",
			path:       "aws/",
		},
		{
			name:       "empty group segment",
			collection: "team",
			path:       "aws//password",
		},
		{
			name:       "path traversal",
			collection: "team",
			path:       "aws/../password",
		},
		{
			name:       "group contains the reserved delimiter",
			collection: "team",
			path:       "aws__other/password",
		},
		{
			name:       "field contains the reserved delimiter",
			collection: "team",
			path:       "aws/field__other",
		},
		{
			name:       "service account key has no group",
			collection: "team",
			path:       "updater-service-account",
		},
		{
			name:          "combined GSM name at length limit",
			collection:    "team",
			path:          "aws/" + strings.Repeat("a", 244),
			expectedGroup: "aws",
			expectedField: strings.Repeat("a", 244),
			expectedOK:    true,
		},
		{
			name:       "combined GSM name exceeds length limit",
			collection: "team",
			path:       "aws/" + strings.Repeat("a", 245),
		},
		{
			name:          "nested GSM name at length limit",
			collection:    "team",
			path:          "aws/prod/" + strings.Repeat("a", 238),
			expectedGroup: "aws/prod",
			expectedField: strings.Repeat("a", 238),
			expectedOK:    true,
		},
		{
			name:       "nested GSM name exceeds length limit",
			collection: "team",
			path:       "aws/prod/" + strings.Repeat("a", 239),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			group, field, ok := ParseSecretPath(tc.collection, tc.path)
			if ok != tc.expectedOK || group != tc.expectedGroup || field != tc.expectedField {
				t.Errorf("ParseSecretPath(%q, %q) = (%q, %q, %t), want (%q, %q, %t)",
					tc.collection, tc.path, group, field, ok, tc.expectedGroup, tc.expectedField, tc.expectedOK)
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
			name:     "underscores are ok",
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
			name:     "dockerconfig",
			input:    ".dockerconfigjson",
			expected: fmt.Sprintf("%sdockerconfigjson", DotReplacementString),
		},
		{
			name:     "complex field name",
			input:    "sa.ci-operator.build01.config",
			expected: fmt.Sprintf("sa%sci-operator%sbuild01%sconfig", DotReplacementString, DotReplacementString, DotReplacementString),
		},
		{
			name:     "double underscores",
			input:    "mac_ai__base_dir",
			expected: "mac_ai--uu--base_dir",
		},
		{
			name:     "triple underscores",
			input:    "some___field",
			expected: "some--uuu--field",
		},
		{
			name:     "quadruple underscores",
			input:    "test____name",
			expected: "test--uuuu--name",
		},
		{
			name:     "dots and double underscores",
			input:    "field.with__both",
			expected: "field--dot--with--uu--both",
		},
		{
			name:     "real-world example with underscores and dots",
			input:    "mac_ai__base_work_dir.mac4",
			expected: "mac_ai--uu--base_work_dir--dot--mac4",
		},
		{
			name:     "multiple double underscores",
			input:    "a__b__c",
			expected: "a--uu--b--uu--c",
		},
		{
			name:     "mixed consecutive underscores",
			input:    "a__b___c____d",
			expected: "a--uu--b--uuu--c--uuuu--d",
		},
		{
			name:     "starts with double underscores",
			input:    "__field",
			expected: "--uu--field",
		},
		{
			name:     "ends with double underscores",
			input:    "field__",
			expected: "field--uu--",
		},
		{
			name:     "starts and ends with double underscores",
			input:    "__field__",
			expected: "--uu--field--uu--",
		},
		{
			name:     "starts with triple underscores",
			input:    "___name",
			expected: "--uuu--name",
		},
		{
			name:     "ends with triple underscores",
			input:    "name___",
			expected: "name--uuu--",
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
			input:    fmt.Sprintf("build_farm%scluster-init%sbuild01%sconfig", DotReplacementString, DotReplacementString, DotReplacementString),
			expected: "build_farm.cluster-init.build01.config",
		},
		{
			name:     "no encoding to decode",
			input:    "simple-name",
			expected: "simple-name",
		},
		{
			name:     "decode double underscores",
			input:    "mac_ai--uu--base_dir",
			expected: "mac_ai__base_dir",
		},
		{
			name:     "decode triple underscores",
			input:    "some--uuu--field",
			expected: "some___field",
		},
		{
			name:     "decode quadruple underscores",
			input:    "test--uuuu--name",
			expected: "test____name",
		},
		{
			name:     "decode dots and double underscores",
			input:    "field--dot--with--uu--both",
			expected: "field.with__both",
		},
		{
			name:     "decode real-world example with underscores and dots",
			input:    "mac_ai--uu--base_work_dir--dot--mac4",
			expected: "mac_ai__base_work_dir.mac4",
		},
		{
			name:     "decode multiple double underscores",
			input:    "a--uu--b--uu--c",
			expected: "a__b__c",
		},
		{
			name:     "decode mixed consecutive underscores",
			input:    "a--uu--b--uuu--c--uuuu--d",
			expected: "a__b___c____d",
		},
		{
			name:     "decode starts with double underscores",
			input:    "--uu--field",
			expected: "__field",
		},
		{
			name:     "decode ends with double underscores",
			input:    "field--uu--",
			expected: "field__",
		},
		{
			name:     "decode starts and ends with double underscores",
			input:    "--uu--field--uu--",
			expected: "__field__",
		},
		{
			name:     "decode starts with triple underscores",
			input:    "--uuu--name",
			expected: "___name",
		},
		{
			name:     "decode ends with triple underscores",
			input:    "name--uuu--",
			expected: "name___",
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
			name:          "valid group: contains single underscore",
			group:         "group_name",
			expectedValid: true,
		},
		{
			name:          "valid group: underscore in path",
			group:         "group/sub_group",
			expectedValid: true,
		},
		{
			name:          "valid group: multiple single underscores",
			group:         "aws_something/my_subgroup",
			expectedValid: true,
		},
		{
			name:          "invalid group: starts with underscore",
			group:         "_group",
			expectedValid: false,
		},
		{
			name:          "invalid group: double underscores",
			group:         "group__name",
			expectedValid: false,
		},
		{
			name:          "invalid group: double underscores in path",
			group:         "group/sub__group",
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
		{
			name:          "valid group: uppercase letters",
			group:         "MY_UPPERCASE_GROUP",
			expectedValid: true,
		},
		{
			name:          "valid group: mixed case",
			group:         "scalingPipelines",
			expectedValid: true,
		},
		{
			name:          "valid group: --dot-- prefix", // sadly we have to have this, because of backwards compatibility
			group:         "--dot--awscred",
			expectedValid: true,
		},
		{
			name:          "valid group: uppercase in nested path",
			group:         "teams/slcm/BOS2-Nokia-bastion",
			expectedValid: true,
		},
		{
			name:          "invalid group: arbitrary leading hyphen",
			group:         "--awscred",
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
