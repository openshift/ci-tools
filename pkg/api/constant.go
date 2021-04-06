package api

import (
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	RegistryPullCredentialsSecret = "registry-pull-credentials"

	RegistryPushCredentialsCICentralSecret          = "registry-push-credentials-ci-central"
	RegistryPushCredentialsCICentralSecretMountPath = "/etc/push-secret"

	GCSUploadCredentialsSecret          = "gce-sa-credentials-gcs-publisher"
	GCSUploadCredentialsSecretMountPath = "/secrets/gcs"

	ReleaseAnnotationSoftDelete = "release.openshift.io/soft-delete"

	// DPTPRequesterLabel is the label on a Kubernates CR whose value indicates the automated tool that requests the CR
	DPTPRequesterLabel = "dptp.openshift.io/requester"

	KVMDeviceLabel = "devices.kubevirt.io/kvm"
	ClusterLabel   = "ci-operator.openshift.io/cluster"
)

var (
	ValidClusterNames = sets.NewString(
		string(ClusterAPICI),
		string(ClusterAPPCI),
		string(ClusterBuild01),
		string(ClusterBuild02),
		string(ClusterVSphere),
	)
)
