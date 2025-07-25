// config.go
package main

import (
	"fmt"
	"strings"

	"cloud.google.com/go/iam/apiv1/iampb"
)

const (
	UpdaterSAEmailSuffix = "-updater@openshift-ci-secrets.iam.gserviceaccount.com"
	ProjectId            = "openshift-ci-secrets"
	ProjectIdNumber      = "384486694155"

	// SecretSyncLabel is a label used to identify secrets that are managed by this tool
	SecretSyncLabel = "dptp-secret-sync"

	SecretAccessorRole = "projects/openshift-ci-secrets/roles/CustomRole542" // read access only
	SecretUpdaterRole  = "projects/openshift-ci-secrets/roles/CustomRole"    // create, update, delete access

	updaterSASecretSuffix = "__updater-service-account"
	indexSecretSuffix     = "____index"
)

var updaterSAFormat = fmt.Sprintf(`[a-z0-9-]+%s$`, UpdaterSAEmailSuffix)

// DesiredGroupsMap represents the groups contained within the _config.yaml file.
type DesiredGroupsMap map[string]GroupAccessInfo
type SAMap map[string]ServiceAccountInfo

type GroupAccessInfo struct {
	Name              string
	Email             string
	SecretCollections []string
}

type DesiredCollection struct {
	Name             string
	GroupsWithAccess []string
	IAMBindings      []CanonicalIAMBinding
}

// SecretType represents the type of secret for cleanup decisions
type SecretType int

const (
	SecretTypeUnknown SecretType = iota
	SecretTypeSA                 // Service Account secrets
	SecretTypeIndex              // Index secrets
)

type GCPSecret struct {
	Name         string // just the name, e.g. "my-secret"
	ResourceName string // full resource name, e.g. "projects/openshift-ci-secrets/secrets/my-secret"
	Collection   string
	Labels       map[string]string
	Annotations  map[string]string
	Payload      []byte
	Type         SecretType // Classification for cleanup decisions
}

// CanonicalIAMBinding is a simplified, canonical representation for diffing IAM bindings.
type CanonicalIAMBinding struct {
	Role      string
	Members   string // Sorted members joined by a delimiter (e.g., ",")
	Condition string // The raw expression string, or "" if no condition
}

// ServiceAccountInfo represents the actual state of an updater Service Account in GCP
type ServiceAccountInfo struct {
	Email       string
	DisplayName string
	Collection  string
}

type Actions struct {
	SAsToCreate                         SAMap
	SAsToDelete                         SAMap
	SecretsToCreate                     map[string]GCPSecret
	SecretsToDelete                     []GCPSecret
	SecretCollectionsToCompletelyRemove []DesiredCollection //in case when the collection is not used anywhere: delete all secrets in that collection
	ConsolidatedIAMPolicy               *iampb.Policy
}

// getProjectResourceIdNumber returns the resource string for our GCP project
// in format `projects/{project id number}`.
func getProjectResourceIdNumber() string {
	return fmt.Sprintf("projects/%s", ProjectIdNumber)
}

// getProjectResourceString returns the resource string for our GCP project
// in format `projects/{project id number}`.
func getProjectResourceString() string {
	return fmt.Sprintf("projects/%s", ProjectId)
}

// getUpdaterSAEmail returns the updater service account email for a collection,
// e.g., "my-collection-updater@<project-id>.iam.gserviceaccount.com".
func getUpdaterSAEmail(collection string) string {
	return fmt.Sprintf("%s-updater@%s.iam.gserviceaccount.com", collection, ProjectId)
}

// getUpdaterSAId returns the updater service account ID for a given display name.
func getUpdaterSAId(displayName string) string {
	return fmt.Sprintf("%s-updater", displayName)
}

// getUpdaterSASecretName returns standardized name for updater service account secret,
// `{collection}__updater-service-account`.
func getUpdaterSASecretName(collection string) string {
	return fmt.Sprintf("%s%s", collection, updaterSASecretSuffix)
}

func getIndexSecretName(collection string) string {
	return fmt.Sprintf("%s%s", collection, indexSecretSuffix)
}

func buildSecretAccessorRoleConditionExpression(collection string) string {
	// Define the two specific secrets this role can access
	updaterSecret := fmt.Sprintf("%s%s", collection, updaterSASecretSuffix)
	indexSecret := fmt.Sprintf("%s%s", collection, indexSecretSuffix)

	return fmt.Sprintf(`(
  resource.type == "secretmanager.googleapis.com/SecretVersion" ||
  resource.type == "secretmanager.googleapis.com/Secret"
) && (
  resource.name.extract("secrets/{secret}/") == "%s" ||
  resource.name.extract("secrets/{secret}/") == "%s"
)`, updaterSecret, indexSecret)
}

func buildSecretUpdaterRoleConditionExpression(collection string) string {
	return fmt.Sprintf(`(
  resource.type == "secretmanager.googleapis.com/SecretVersion" ||
  resource.type == "secretmanager.googleapis.com/Secret"
) && 
  resource.name.extract("secrets/{secret}/").startsWith("%s__")`, collection)
}

// extractCollectionFromSecretName returns the part before "__" in the secret name.
// e.g., "colors__updater-service-account" -> "colors"
func extractCollectionFromSecretName(secretName string) string {
	parts := strings.SplitN(secretName, "__", 2)
	if len(parts) == 2 && parts[0] != "" {
		return parts[0]
	}
	return ""
}
