package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	// Event reasons
	EventReasonBuildClientNotFound   = "BuildClientNotFound"
	EventReasonAborted               = "Aborted"
	EventReasonDeprovisioningStarted = "DeprovisioningStarted"
	EventReasonValidationFailed      = "ValidationFailed"
	EventReasonProwJobCreationFailed = "ProwJobCreationFailed"
	EventReasonProwJobCreated        = "ProwJobCreated"
	EventReasonClusterReady          = "ClusterReady"
	EventReasonProwJobFailed         = "ProwJobFailed"
	EventReasonProwJobSucceeded      = "ProwJobSucceeded"
	EventReasonProwJobAborted        = "ProwJobAborted"

	KonfluxClusterAnnotation = "ephemeralcluster.ci.openshift.io/konflux-cluster"
	KonfluxTenantAnnotation  = "ephemeralcluster.ci.openshift.io/konflux-tenant"
)

// Conditions
const (
	// ProwJobCreated indicates whether the prowjob was created.
	ProwJobCreated string = "ProwJobCreated"
	// Reasons
	CIOperatorJobsGenerateFailureReason     = "CIOperatorJobsGenerateFailure"
	CouldNotPersistProwJobReason            = "CouldNotPersistProwJob"
	ProwJobProperlyCreatedReason            = "ProwJobProperlyCreated"
	OrphanProwJobFoundReason                = "OrphanProwJobFound"
	TooManyProwJobsBoundReason              = "TooManyProwJobsBound"
	EphemeralClusterValidationFailureReason = "EphemeralClusterValidationFailure"

	// ClusterReady type indicates whether the cluster is up and running.
	ClusterReady string = "ClusterReady"
	// Reasons
	SecretsFetchFailureReason  = "SecretsFetchFailure"
	HiveCredentialsReadyReason = "HiveCredentialsReady"
	CredentialsReadyReason     = "CredentialsReady"
	KubeconfigNotReadyMsg      = "kubeconfig not ready"
	HiveSecretsNotReadyMsg     = "hive secrets not ready"

	// TestCompleted indicates test has completed and the ephemeral cluster isn't needed anymore.
	TestCompleted string = "TestCompleted"
	// Reasons
	TestDoneReason                         = "TestDone"
	CreateTestCompletedSecretFailureReason = "CreateTestCompletedSecretFailure"

	// ProwJobCompleted type indicates whether the ProwJob has done.
	ProwJobCompleted string = "ProwJobCompleted"
	// Reasons
	ProwJobFailureReason   string = "ProwJobFailure"
	ProwJobCompletedReason string = "ProwJobCompleted"

	// Shared resons
	CIOperatorNSNotFoundMsg = "ci-operator NS not found"
)

// EphemeralClusterPhase is an high level description of where the ephemeral cluster is in its lifecycle
type EphemeralClusterPhase string

const (
	// EphemeralClusterInitializing describes everything that happens before the ProwJob is created.
	EphemeralClusterInitializing EphemeralClusterPhase = "Initializing"
	// EphemeralClusterInitializationFailed describes an initialization failure.
	EphemeralClusterInitializationFailed EphemeralClusterPhase = "InitializationFailed"
	// EphemeralClusterProvisioning describes everything that happens after the ProwJob was created
	// and before the kubeconfig is available.
	EphemeralClusterProvisioning EphemeralClusterPhase = "Provisioning"
	// EphemeralClusterProvisioningFailed means the controller could not provision the cluster.
	EphemeralClusterProvisioningFailed EphemeralClusterPhase = "ProvisioningFailed"
	// EphemeralClusterReady means the cluster is running and the kubeconfig is available.
	EphemeralClusterReady EphemeralClusterPhase = "Ready"
	// EphemeralClusterDeprovisioning means that the deprovisioning procedures are happening.
	EphemeralClusterDeprovisioning EphemeralClusterPhase = "Deprovisioning"
	// EphemeralClusterDeprovisioning means the controller could not deprovisioning the cluster.
	EphemeralClusterDeprovisioningFailure EphemeralClusterPhase = "DeprovisioningFailure"
	// EphemeralClusterDeprovisioning means that the cluster has been deprovisioned.
	EphemeralClusterDeprovisioned EphemeralClusterPhase = "Deprovisioned"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:shortName=ec
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="ProwJob_Id",type=string,JSONPath=`.status.prowJobId`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type EphemeralCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	// +kubebuilder:validation:Required
	Spec   EphemeralClusterSpec   `json:"spec"`
	Status EphemeralClusterStatus `json:"status,omitempty"`
}

func (ec *EphemeralCluster) KonfluxCluster() string {
	if value, ok := ec.Annotations[KonfluxClusterAnnotation]; ok {
		return value
	}
	return ""
}

func (ec *EphemeralCluster) KonfluxTenant() string {
	if value, ok := ec.Annotations[KonfluxTenantAnnotation]; ok {
		return value
	}
	return ""
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type EphemeralClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []EphemeralCluster `json:"items"`
}

type EphemeralClusterSpec struct {
	CIOperator CIOperatorSpec `json:"ciOperator"`
	// When set to true, signals the controller that the ephemeral cluster is no longer needed,
	// allowing decommissioning procedures to begin.
	TearDownCluster bool `json:"tearDownCluster,omitempty"`
}

// CIOperatorSpec contains what is needed to run ci-operator
type CIOperatorSpec struct {
	BuildRootImage *api.BuildRootImageConfiguration       `json:"buildRoot,omitempty"`
	BaseImages     map[string]api.ImageStreamTagReference `json:"baseImages,omitempty"`
	ExternalImages map[string]api.ExternalImage           `json:"externalImages,omitempty"`
	Releases       map[string]api.UnresolvedRelease       `json:"releases,omitempty"`
	Resources      api.ResourceConfiguration              `json:"resources,omitempty"`
	Test           TestSpec                               `json:"test,omitempty"`
}

// TestSpec determines the workflow will be executed by the ci-operator to provision a cluster.
type TestSpec struct {
	Workflow       string            `json:"workflow,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	ClusterProfile string            `json:"clusterProfile,omitempty"`
	ClusterClaim   *api.ClusterClaim `json:"clusterClaim,omitempty"`
}

type EphemeralClusterStatus struct {
	// Phase is an high level description of where the ephemeral cluster is in its lifecycle
	Phase EphemeralClusterPhase `json:"phase"`
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	ProwJobID  string             `json:"prowJobId,omitempty"`
	ProwJobURL string             `json:"prowJobURL,omitempty"`
	// SecretRef is the name of the Secret containing credentials to access the
	// ephemeral cluster. The Secret is in the same namespace as the EphemeralCluster
	// and contains a "kubeconfig" key and optionally a "kubeAdminPassword" key.
	SecretRef string `json:"secretRef,omitempty"`
}
