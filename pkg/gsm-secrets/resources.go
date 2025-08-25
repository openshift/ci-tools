package gsmsecrets

import (
	"fmt"
	"strings"
)

// GetProjectResourceIdNumber returns the resource id number for our GCP project
// in format `projects/{project id number}`, e.g., "projects/1234567890"
func GetProjectResourceIdNumber(projectIdNumber string) string {
	return fmt.Sprintf("projects/%s", projectIdNumber)
}

// GetProjectResourceString returns the resource string for our GCP project
// in format `projects/{project id string}`, e.g., "projects/ci-secrets"
func GetProjectResourceString(projectIdString string) string {
	return fmt.Sprintf("projects/%s", projectIdString)
}

// GetUpdaterSAFormat returns the regex pattern for updater service account emails for a given project
func GetUpdaterSAFormat(config Config) string {
	return fmt.Sprintf(`[a-z0-9-]+%s$`, config.GetUpdaterSAEmailSuffix())
}

// GetUpdaterSAEmail returns the updater service account email for a collection,
// e.g., "my-collection-updater@<project-id>.iam.gserviceaccount.com".
func GetUpdaterSAEmail(collection string, config Config) string {
	return fmt.Sprintf("%s-updater@%s.iam.gserviceaccount.com", collection, config.ProjectIdString)
}

// GetUpdaterSAId returns the updater service account ID for a given display name.
func GetUpdaterSAId(displayName string) string {
	return fmt.Sprintf("%s-updater", displayName)
}

// GetUpdaterSASecretName returns standardized name for updater service account secret,
// `{collection}__updater-service-account`.
func GetUpdaterSASecretName(collection string) string {
	return fmt.Sprintf("%s%s", collection, UpdaterSASecretSuffix)
}

// GetIndexSecretName returns standardized name for the index secret,
// `{collection}____index`.
func GetIndexSecretName(collection string) string {
	return fmt.Sprintf("%s%s", collection, IndexSecretSuffix)
}

// GetSecretID extracts the secret ID from the secret name, e.g.,
// "projects/openshift-ci-secrets/secrets/collection__secret" -> "collection__secret"
func GetSecretID(secretName string) string {
	return strings.Split(secretName, "/")[len(strings.Split(secretName, "/"))-1] // Extract just the secret ID
}
