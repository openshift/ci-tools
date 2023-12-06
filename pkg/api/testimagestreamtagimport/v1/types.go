package v1

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/api/utils"
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
	LabelKeyImageStreamTagNamespace = "imagestreamtag-namespace"
	LabelKeyImageStreamTagName      = "imagestreamtag-name"
)

// WithImageStreamLabels sets namespace and name labels so we can easily
// filter for specific imports
func (t *TestImageStreamTagImport) WithImageStreamLabels() *TestImageStreamTagImport {
	if t.Labels == nil {
		t.Labels = map[string]string{}
	}
	for k, v := range LabelsForImageStreamTag(t.Spec.Namespace, t.Spec.Name) {
		t.Labels[k] = v
	}
	return t
}

// LabelsForImageStreamTag returns the labels by which testimagestreamtagimports
// for a given imagestreamtag can be selected.
func LabelsForImageStreamTag(namespace, name string) map[string]string {
	return utils.SanitizeLabels(map[string]string{
		LabelKeyImageStreamTagNamespace: namespace,
		LabelKeyImageStreamTagName:      name,
	})
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
