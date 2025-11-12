package gsmsecrets

import (
	"fmt"
	"strings"

	validation "github.com/openshift/ci-tools/pkg/gsm-validation"
)

const GcpMaxNameLength = 255

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
func ExtractCollectionFromSecretName(secretName string) string {
	if strings.HasSuffix(secretName, IndexSecretSuffix) {
		collection := strings.TrimSuffix(secretName, IndexSecretSuffix)
		if collection != "" && validation.ValidateCollectionName(collection) {
			return collection
		}
		return ""
	}

	parts := strings.Split(secretName, "__")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		if !validation.ValidateCollectionName(parts[0]) {
			return ""
		}
		if !validation.ValidateSecretName(parts[1]) {
			return ""
		}
		return parts[0]
	}

	return ""
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
