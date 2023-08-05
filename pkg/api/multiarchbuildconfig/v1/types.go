package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	buildv1 "github.com/openshift/api/build/v1"
)

const (
	MultiArchBuildConfigNameLabel = "multiarchbuildconfigs.ci.openshift.io/name"
	MultiArchBuildConfigArchLabel = "multiarchbuildconfigs.ci.openshift.io/arch"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:shortName=mabc

type MultiArchBuildConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	// +kubebuilder:validation:Required
	Spec   MultiArchBuildConfigSpec   `json:"spec"`
	Status MultiArchBuildConfigStatus `json:"status,omitempty"`
}

type MultiArchBuildConfigSpec struct {
	BuildSpec buildv1.BuildConfigSpec `json:"build_spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type MultiArchBuildConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []MultiArchBuildConfig `json:"items"`
}

type MultiArchBuildConfigStatus struct {
	Conditions []metav1.Condition        `json:"conditions,omitempty"`
	State      MultiArchBuildConfigState `json:"state,omitempty"`
	Builds     map[string]*buildv1.Build `json:"builds,omitempty"`
}

type MultiArchBuildConfigState string

const (
	// SuccessState means all builds were completed without error (exit 0)
	SuccessState MultiArchBuildConfigState = "success"
	// FailureState means that all builds were completed with errors (exit non-zero)
	FailureState MultiArchBuildConfigState = "failure"
)
