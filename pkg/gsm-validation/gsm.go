package gsmvalidation

import (
	"regexp"
	"strings"
)

const (
	// CollectionSecretDelimiter is the separator between collection and secret name in GSM
	CollectionSecretDelimiter = "__"

	// Encoding constants for special characters
	DotReplacementString   = "--dot--"
	SlashReplacementString = "--slash--"

	CollectionRegex = "^([a-z0-9_-]*[a-z0-9])?$"
	SecretNameRegex = "^[A-Za-z0-9_-]+$"

	// MaxCollectionLength is the maximum length of a collection name
	MaxCollectionLength = 50

	// GcpMaxNameLength is the maximum length for a GSM secret name
	GcpMaxNameLength = 255
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

	return regexp.MustCompile(CollectionRegex).MatchString(collection)
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

	return regexp.MustCompile(SecretNameRegex).MatchString(secretName)
}

// NormalizeName replaces forbidden characters in GSM collections and secret names with safe replacements.
// GSM doesn't support dots and other special characters in secret names, so we need special handling to avoid conflicts.
func NormalizeName(name string) string {
	normalized := strings.ReplaceAll(name, ".", DotReplacementString)
	normalized = strings.ReplaceAll(normalized, "/", SlashReplacementString)
	return normalized
}

// Decoding counterpart to NormalizeName
func DenormalizeName(name string) string {
	denormalized := strings.ReplaceAll(name, DotReplacementString, ".")
	denormalized = strings.ReplaceAll(denormalized, SlashReplacementString, "/")
	return denormalized
}
