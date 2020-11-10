/*
 package imageapiregistration copies https://github.com/openshift/api/blob/27004eede9292623460c183b189b2f741cef2dc6/image/v1/register.go with
 the key difference that it doesn't register v1.SecretList, which otherwise breaks clients due to the fact that its registered multiple times.
 TODO: Remove once we have https://github.com/openshift/api/pull/780
*/
package imageapiregistration

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/openshift/api/image/docker10"
	"github.com/openshift/api/image/dockerpre012"
	imagev1 "github.com/openshift/api/image/v1"
)

var (
	GroupName     = "image.openshift.io"
	GroupVersion  = schema.GroupVersion{Group: GroupName, Version: "v1"}
	schemeBuilder = runtime.NewSchemeBuilder(addKnownTypes, docker10.AddToScheme, dockerpre012.AddToScheme, corev1.AddToScheme)
	// Install is a function which adds this version to a scheme
	Install = schemeBuilder.AddToScheme

	// SchemeGroupVersion generated code relies on this name
	// Deprecated
	SchemeGroupVersion = GroupVersion
	// AddToScheme exists solely to keep the old generators creating valid code
	// DEPRECATED
	AddToScheme = schemeBuilder.AddToScheme
)

// Resource generated code relies on this being here, but it logically belongs to the group
// DEPRECATED
func Resource(resource string) schema.GroupResource {
	return schema.GroupResource{Group: GroupName, Resource: resource}
}

// Adds the list of known types to api.Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&imagev1.Image{},
		&imagev1.ImageList{},
		&imagev1.ImageSignature{},
		&imagev1.ImageStream{},
		&imagev1.ImageStreamList{},
		&imagev1.ImageStreamMapping{},
		&imagev1.ImageStreamTag{},
		&imagev1.ImageStreamTagList{},
		&imagev1.ImageStreamImage{},
		&imagev1.ImageStreamLayers{},
		&imagev1.ImageStreamImport{},
		&imagev1.ImageTag{},
		&imagev1.ImageTagList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
