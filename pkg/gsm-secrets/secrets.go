package gsmsecrets

import (
	"fmt"
	"strings"

	validation "github.com/openshift/ci-tools/pkg/gsm-validation"
)

// ClassifySecret determines the type of secret based on its name
func ClassifySecret(secretName string) SecretType {
	if strings.HasSuffix(secretName, UpdaterSASecretSuffix) {
		return SecretTypeSA
	}
	if strings.HasSuffix(secretName, IndexSecretSuffix) {
		return SecretTypeIndex
	}
	if strings.Contains(secretName, "__") {
		return SecretTypeGeneric
	}
	return SecretTypeUnknown
}

// ExtractCollectionFromSecretName returns the substring before the first "__" in a secret name.
// Supports both 2-level (collection__field) and 3-level (collection__group__field) hierarchies.
func ExtractCollectionFromSecretName(secretName string) string {
	// Special case: index secrets (collection____index)
	if strings.HasSuffix(secretName, IndexSecretSuffix) {
		collection := strings.TrimSuffix(secretName, IndexSecretSuffix)
		if collection != "" && validation.ValidateCollectionName(collection) {
			return collection
		}
		return ""
	}

	// Reject malformed index secrets (contains ____index but doesn't end with it)
	if strings.Contains(secretName, IndexSecretSuffix) {
		return ""
	}

	// Split by delimiter
	parts := strings.Split(secretName, "__")

	// Need at least 2 parts: collection and field (or collection, group, field, etc.)
	if len(parts) < 2 {
		return ""
	}

	// Validate collection (first part)
	collection := parts[0]
	if !validation.ValidateCollectionName(collection) {
		return ""
	}

	// Validate that we have at least one more non-empty part
	hasValidPart := false
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			hasValidPart = true
			break
		}
	}

	if !hasValidPart {
		return ""
	}

	return collection
}

// VerifyIndexSecretContent verifies that the index secret content is correct.
// At this point we assume that the index secret only contains the updater service account secret name.
func VerifyIndexSecretContent(payload []byte) error {
	expectedContent := "- updater-service-account"
	actualContent := strings.TrimSpace(string(payload))

	if actualContent != expectedContent {
		return fmt.Errorf("index secret content mismatch: expected %q, got %q", expectedContent, actualContent)
	}

	return nil
}
