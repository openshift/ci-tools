package v1

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TestImageStreamTagImport can be used to request an ImageStreamTag import
type TestImageStreamTagImport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec TestImageStreamTagImportSpec `json:"spec"`
}

// SetDeterministicName sets the name of an TestImageStreamTagImport. Using it allows to avoid
// creating multiple objects for the same import.
func (t *TestImageStreamTagImport) SetDeterministicName() {
	t.Name = fmt.Sprintf("%s-%s-%s", t.Spec.ClusterName, t.Spec.Namespace, strings.ReplaceAll(t.Spec.Name, ":", "."))
	t.WithImageStreamLabels()
}

const (
	LabelKeyImageStreamNamespace = "imagestream-namespace"
	LabelKeyImageStreamName      = "imagestream-name"
)

// WithImageStreamLabels sets namespace and name labels so we can easily
// filter for specific imports
func (t *TestImageStreamTagImport) WithImageStreamLabels() *TestImageStreamTagImport {
	if t.Labels == nil {
		t.Labels = map[string]string{}
	}
	t.Labels[LabelKeyImageStreamNamespace] = t.Spec.Namespace
	t.Labels[LabelKeyImageStreamName] = t.Spec.Name
	return t
}

type TestImageStreamTagImportSpec struct {
	// ClusterName is the name of the cluster in which the import should be created
	ClusterName string `json:"clusterName,omitempty"`
	// Namespace is the namespace of the imagestreamtag
	Namespace string `json:"namespace,omitempty"`
	// Name is the name of the imagestreamtag
	Name string `json:"name,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TestImageStreamTagImportList is a list of TestImageStreamTagImport resources
type TestImageStreamTagImportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []TestImageStreamTagImport `json:"items"`
}
