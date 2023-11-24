package v1

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
)

func init() {
	if err := AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add pullrequestpayloadtest api to scheme: %v", err))
	}
}

// SchemeGroupVersion is group version used to register these objects
var SchemeGroupVersion = schema.GroupVersion{Group: "ci.openshift.io", Version: "v1"}

// Kind takes an unqualified kind and returns back a Group qualified GroupKind
func Kind(kind string) schema.GroupKind {
	return SchemeGroupVersion.WithKind(kind).GroupKind()
}

// Resource takes an unqualified resource and returns a Group qualified GroupResource
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

var (
	// SchemeBuilder collects functions that add things to a scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	// AddToScheme applies all the stored functions to the scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// Adds the list of known types to the Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&MultiArchBuildConfig{},
		&MultiArchBuildConfigList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
