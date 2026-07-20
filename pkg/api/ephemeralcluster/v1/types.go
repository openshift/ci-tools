package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	CIOperatorJobsGenerateFailureReason     = "CIOperatorJobsGenerateFailure"
	ProwJobFailureReason                    = "ProwJobFailure"
	ProwJobCompletedReason                  = "ProwJobCompleted"
	TooManyProwJobsBoundReason              = "TooManyProwJobsBound"
	SecretsFetchFailureReason               = "SecretsFetchFailure"
	CreateTestCompletedSecretFailureReason  = "CreateTestCompletedSecretFailure"
	EphemeralClusterValidationFailureReason = "EphemeralClusterValidationFailure"

	CIOperatorNSNotFoundMsg = "ci-operator NS not found"
	KubeconfigNotReadyMsg   = "kubeconfig not ready"
	HiveSecretsNotReadyMsg  = "hive secrets not ready"

	KonfluxClusterAnnotation  = "ephemeralcluster.ci.openshift.io/konflux-cluster"
	KonfluxTenantAnnotation   = "ephemeralcluster.ci.openshift.io/konflux-tenant"
	PipelineRunNameAnnotation = "ephemeralcluster.ci.openshift.io/pipeline-run-name"
	TaskRunNameAnnotation     = "ephemeralcluster.ci.openshift.io/task-run-name"
)

// EphemeralClusterCondition is a valid value for EphemeralClusterCondition.Type
type EphemeralClusterConditionType string

const (
	// ProwJobCreating indicates whether the prowjob is being created.
	ProwJobCreating EphemeralClusterConditionType = "ProwJobCreating"
	// ContainersReady indicates whether the cluster is up and running.
	ClusterReady EphemeralClusterConditionType = "ClusterReady"
	// ProwJobCompleted indicates whether the ProwJob has done.
	ProwJobCompleted EphemeralClusterConditionType = "ProwJobCompleted"
	// TestCompleted indicates test has completed and the ephemeral cluster isn't needed anymore.
	TestCompleted EphemeralClusterConditionType = "TestCompleted"
)

type ConditionStatus string

// These are valid condition statuses. "ConditionTrue" means a resource is in the condition.
// "ConditionFalse" means a resource is not in the condition.
const (
	ConditionTrue  ConditionStatus = "True"
	ConditionFalse ConditionStatus = "False"
)

type EphemeralClusterPhase string

const (
	// EphemeralClusterProvisioning describes everything that happens before the kubeconfig is available.
	// This phase includes creating a ProwJob and waiting for the kubeconfig to show up.
	EphemeralClusterProvisioning EphemeralClusterPhase = "Provisioning"
	// EphemeralClusterReady means the cluster is running and the kubeconfig is available.
	EphemeralClusterReady EphemeralClusterPhase = "Ready"
	// EphemeralClusterDeprovisioning means that the deprovisioning procedures are happening.
	EphemeralClusterDeprovisioning EphemeralClusterPhase = "Deprovisioning"
	// EphemeralClusterDeprovisioning means that the cluster has been deprovisioned.
	EphemeralClusterDeprovisioned EphemeralClusterPhase = "Deprovisioned"
	// EphemeralClusterFailed means that either the cluster is in a error state or the
	// provisioning/deprovisioning procedures didn't succeed.
	EphemeralClusterFailed EphemeralClusterPhase = "Failed"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:shortName=ec
// +kubebuilder:subresource:status
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

func (ec *EphemeralCluster) PipelineRunName() string {
	if value, ok := ec.Annotations[PipelineRunNameAnnotation]; ok {
		return value
	}
	return ""
}

func (ec *EphemeralCluster) TaskRunName() string {
	if value, ok := ec.Annotations[TaskRunNameAnnotation]; ok {
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

type PullRequestMeta struct {
	Payload string `json:"event,omitempty"`
	Headers string `json:"headers,omitempty"`
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
	Phase      EphemeralClusterPhase       `json:"phase"`
	Conditions []EphemeralClusterCondition `json:"conditions,omitempty"`
	ProwJobID  string                      `json:"prowJobId,omitempty"`
	ProwJobURL string                      `json:"prowJobURL,omitempty"`
	// SecretRef is the name of the Secret containing credentials to access the
	// ephemeral cluster. The Secret is in the same namespace as the EphemeralCluster
	// and contains a "kubeconfig" key and optionally a "kubeAdminPassword" key.
	SecretRef string `json:"secretRef,omitempty"`
}

// EphemeralClusterCondition contains details for the current condition of this EphemeralCluster.
type EphemeralClusterCondition struct {
	// Type is the type of the condition.
	Type EphemeralClusterConditionType `json:"type"`
	// Status is the status of the condition.
	Status ConditionStatus `json:"status"`
	// Last time the condition transitioned from one status to another.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	// Unique, one-word, CamelCase reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`
	// Human-readable message indicating details about last transition.
	// +optional
	Message string `json:"message,omitempty"`
}
