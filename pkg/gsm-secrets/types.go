package gsmsecrets

import (
	"fmt"

	"cloud.google.com/go/iam/apiv1/iampb"
)

const (
	TestPlatform = "test platform"

	UpdaterSASecretSuffix = "__updater-service-account"
	IndexSecretSuffix     = "____index"

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

func (c Config) GetUpdaterSAEmailSuffix() string {
	return fmt.Sprintf("-updater@%s.iam.gserviceaccount.com", c.ProjectIdString)
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
