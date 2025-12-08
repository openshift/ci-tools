package gsmvalidation

import (
	"regexp"
	"strings"
)

const (
	// CollectionSecretDelimiter is the separator between collection and secret name in GSM
	CollectionSecretDelimiter = "__"

	// Encoding constants for special characters
	DotReplacementString        = "--dot--"
	SlashReplacementString      = "--slash--"
	UnderscoreReplacementString = "--u--"

	CollectionRegex = "^([a-z0-9_-]*[a-z0-9])?$"
	GroupRegex      = `^[a-z0-9]+([a-z0-9-]*[a-z0-9]+)?(/[a-z0-9]+([a-z0-9-]*[a-z0-9]+)?)*$`
	SecretNameRegex = "^[A-Za-z0-9_-]+$"

	// MaxCollectionLength is the maximum length of a collection name
	MaxCollectionLength = 50

	// GcpMaxNameLength is the maximum length for a GSM secret name
	GcpMaxNameLength = 255
)

var (
	collectionRegexp = regexp.MustCompile(CollectionRegex)
	groupRegexp      = regexp.MustCompile(GroupRegex)
	secretNameRegexp = regexp.MustCompile(SecretNameRegex)
)

// ValidateCollectionName validates a GSM collection name
func ValidateCollectionName(collection string) bool {
	if collection == "" || len(collection) > MaxCollectionLength {
		return false
	}

	// Cannot end with underscore (would create collection___secret)
	if strings.HasSuffix(collection, "_") {
		return false
	}

	// Cannot contain double underscore (conflicts with delimiter)
	if strings.Contains(collection, CollectionSecretDelimiter) {
		return false
	}

	return collectionRegexp.MatchString(collection)
}

func ValidateGroupName(group string) bool {
	if group == "" {
		return false
	}

	if strings.HasSuffix(group, "_") {
		return false
	}
	return groupRegexp.MatchString(group)
}

// ValidateSecretName validates a GSM secret name
func ValidateSecretName(secretName string) bool {
	if secretName == "" || len(secretName) > GcpMaxNameLength {
		return false
	}

	// Cannot start with underscore (would create collection___secret)
	if strings.HasPrefix(secretName, "_") {
		return false
	}

	// Cannot contain double underscore (conflicts with delimiter)
	if strings.Contains(secretName, CollectionSecretDelimiter) {
		return false
	}
	return secretNameRegexp.MatchString(secretName)
}

// NormalizeName replaces forbidden characters in field names with safe replacements.
// This is used when migrating from Vault to GSM to handle special characters in field names.
// Rules:
//   - `.` → `--dot--` (dots not allowed in GSM secret names)
//   - `_` → `--u--` (underscores act as delimiters in our 3-level hierarchy)
//   - `/` → `--slash--` (slashes only allowed in group paths, not field names)
//
// Example: "aws_creds" → "aws--u--creds"
// Example: ".dockerconfigjson" → "--dot--dockerconfigjson"
func NormalizeName(name string) string {
	// Encode in specific order to avoid conflicts
	normalized := strings.ReplaceAll(name, "_", UnderscoreReplacementString)
	normalized = strings.ReplaceAll(normalized, ".", DotReplacementString)
	normalized = strings.ReplaceAll(normalized, "/", SlashReplacementString)
	return normalized
}

// DenormalizeName decodes field names back to their original form.
// This reverses the encoding done by NormalizeName.
func DenormalizeName(name string) string {
	// Decode in reverse order
	denormalized := strings.ReplaceAll(name, SlashReplacementString, "/")
	denormalized = strings.ReplaceAll(denormalized, DotReplacementString, ".")
	denormalized = strings.ReplaceAll(denormalized, UnderscoreReplacementString, "_")
	return denormalized
}
