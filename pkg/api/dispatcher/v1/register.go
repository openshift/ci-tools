package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// SchemeGroupVersion is the API group and version used for dispatcher controls.
var SchemeGroupVersion = schema.GroupVersion{Group: "ci.openshift.io", Version: "v1"}

// Kind returns a dispatcher API GroupKind for kind.
func Kind(kind string) schema.GroupKind {
	return SchemeGroupVersion.WithKind(kind).GroupKind()
}

// Resource returns a dispatcher API GroupResource for resource.
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

var (
	// SchemeBuilder collects dispatcher API registration functions.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	// AddToScheme registers dispatcher API types in a runtime scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion, &DispatchOverride{}, &DispatchOverrideList{})
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
