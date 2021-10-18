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

	// HiveCluster is the cluster where Hive is deployed
	HiveCluster = ClusterHive

	// HiveAdminKubeconfigSecret is the name of the secret in ci-op-<hash> namespace that stores the Admin's kubeconfig for the ephemeral cluster provisioned by Hive.
	HiveAdminKubeconfigSecret = "hive-admin-kubeconfig"
	// HiveAdminKubeconfigSecretKey is the key to the kubeconfig in the secret HiveAdminKubeconfigSecret
	HiveAdminKubeconfigSecretKey = "kubeconfig"
	// HiveAdminPasswordSecret the name of the secret in ci-op-<hash> namespace that stores the password for the user "kubeadmin" for the ephemeral cluster provisioned by Hive.
	HiveAdminPasswordSecret = "hive-admin-password"
	// HiveAdminPasswordSecretKey is the key to the password in the secret HiveAdminKubeconfigSecret
	HiveAdminPasswordSecretKey = "password"

	// HiveControlPlaneKubeconfigSecret is the name of the secret that stores kubeconfig to contact the cluster where Hive is deployed
	HiveControlPlaneKubeconfigSecret = "hive-hive-credentials"
	// HiveControlPlaneKubeconfigSecretArg is the flag to ci-operator
	HiveControlPlaneKubeconfigSecretArg = "--hive-kubeconfig=/secrets/hive-hive-credentials/kubeconfig"

	AutoScalePodsLabel = "ci.openshift.io/scale-pods"

	NamespaceDir = "build-resources"

	APPCIKubeAPIURL = "https://api.ci.l2s4.p1.openshiftapps.com:6443"

	DefaultLeaseEnv = "LEASED_RESOURCE"

	OauthTokenSecretKey  = "oauth"
	OauthTokenSecretName = "github-credentials-openshift-ci-robot-private-git-cloner"

	GroupSuffix = "-group"
)

var (
	ValidClusterNames = sets.NewString(
		string(ClusterAPPCI),
		string(ClusterBuild01),
		string(ClusterBuild02),
		string(ClusterVSphere),
	)
)
