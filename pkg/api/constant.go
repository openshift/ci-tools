package api

const (
	RegistryPushCredentialsCICentralSecret          = "registry-push-credentials-ci-central"
	RegistryPushCredentialsCICentralSecretMountPath = "/etc/push-secret"

	ImageCreatorKubeconfigSecret          = "image-creator-kubeconfig"
	ImageCreatorKubeconfigSecretMountPath = "/etc/image-creator-kubeconfig"
	FilenameImageCreatorKubeConfig        = "sa.image-creator.config"
)
