package api

const (
	RegistryPushCredentialsCICentralSecret          = "registry-push-credentials-ci-central"
	RegistryPushCredentialsCICentralSecretMountPath = "/etc/push-secret"

	GCSUploadCredentialsSecret          = "gce-sa-credentials-gcs-publisher"
	GCSUploadCredentialsSecretMountPath = "/secrets/gcs"
)
