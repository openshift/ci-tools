package gsmsecrets

import (
	"fmt"
	"strings"

	"cloud.google.com/go/iam/apiv1/iampb"
)

const (
	TestPlatform = "test platform"

	UpdaterSASecretSuffix = "__updater-service-account"
	IndexSecretSuffix     = "____index"

	ServiceAccountIDSuffix = "-sa"

	SecretNameRegex = "^[A-Za-z0-9-]+$"

	// IAM binding condition title prefixes
	SecretsViewerConditionTitlePrefix  = "Read access to secrets for "
	SecretsUpdaterConditionTitlePrefix = "Create, update, and delete access for "

	// IAM binding condition description templates
	SecretsViewerConditionDescriptionTemplate  = "Managed by %s: Read access to secrets in %s collection"
	SecretsUpdaterConditionDescriptionTemplate = "Managed by %s: Create, update, and delete access to secrets in %s collection"
)

type Config struct {
	ProjectIdString string
	ProjectIdNumber string
}

var Production = Config{
	ProjectIdString: "openshift-ci-secrets",
	ProjectIdNumber: "384486694155",
}

func (c Config) GetSecretAccessorRole() string {
	return fmt.Sprintf("projects/%s/roles/openshift_ci_secrets_viewer", c.ProjectIdString)
}

func (c Config) GetSecretUpdaterRole() string {
	return fmt.Sprintf("projects/%s/roles/openshift_ci_secrets_updater", c.ProjectIdString)
}

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
}

// SecretType represents the type of secret for cleanup decisions
type SecretType int

const (
	SecretTypeUnknown SecretType = iota
	SecretTypeSA                 // Service Account secrets
	SecretTypeIndex              // Index secrets
	SecretTypeGeneric            // Generic secrets
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
	Role           string
	Members        string // Sorted members joined by a delimiter (e.g., ",")
	ConditionTitle string // The condition title, or "" if no condition
	ConditionDesc  string // The condition description, or "" if no condition
	ConditionExpr  string // The raw expression string, or "" if no condition
}

// ServiceAccountInfo represents the actual state of an updater Service Account in GCP
type ServiceAccountInfo struct {
	Email       string
	DisplayName string
	Collection  string
}

type Actions struct {
	Config                Config
	SAsToCreate           SAMap
	SAsToDelete           SAMap
	SecretsToCreate       map[string]GCPSecret
	SecretsToDelete       []GCPSecret
	ConsolidatedIAMPolicy *iampb.Policy
}

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

// GetUpdaterSAEmailSuffix returns the suffix for updater service account emails for a given project
// e.g., "-sa@<project-id>.iam.gserviceaccount.com".
func (c Config) GetUpdaterSAEmailSuffix() string {
	return fmt.Sprintf("%s@%s.iam.gserviceaccount.com", ServiceAccountIDSuffix, c.ProjectIdString)
}

// GetUpdaterSAEmail returns the updater service account email for a collection,
// e.g., "my-collection-sa@<project-id>.iam.gserviceaccount.com".
func GetUpdaterSAEmail(collection string, config Config) string {
	return fmt.Sprintf("%s%s@%s.iam.gserviceaccount.com", collection, ServiceAccountIDSuffix, config.ProjectIdString)
}

// GetUpdaterSAId returns the updater service account ID for a given display name.
func GetUpdaterSAId(displayName string) string {
	return fmt.Sprintf("%s%s", displayName, ServiceAccountIDSuffix)
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
