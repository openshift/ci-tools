package gsmsecrets

import (
	"regexp"
	"strings"

	"github.com/openshift/ci-tools/pkg/group"
)

// ValidateSecretName validates if a secret name matches the allowed pattern
func ValidateSecretName(secretName string) bool {
	return regexp.MustCompile(SecretNameRegex).MatchString(secretName)
}

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
		if collection != "" && group.ValidateCollectionName(collection) {
			return collection
		}
		return ""
	}

	parts := strings.Split(secretName, "__")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		if !group.ValidateCollectionName(parts[0]) {
			return ""
		}
		if !ValidateSecretName(parts[1]) {
			return ""
		}
		return parts[0]
	}

	return ""
}
