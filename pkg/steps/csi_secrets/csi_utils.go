package csi_secrets

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/GoogleCloudPlatform/secrets-store-csi-driver-provider-gcp/config"

	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	csiapi "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
)

// GSMProject is the name of the GCP Secret Manager project where the secrets are stored.
const GSMProject = "openshift-ci-secrets"
const KubernetesDNSLabelLimit = 63

// IsK8sSecretReference returns true if the credential refers to a K8s Secret (has namespace and name).
func IsK8sSecretReference(c api.CredentialReference) bool {
	return c.Namespace != "" && c.Name != ""
}

// IsGSMReference returns true if the credential is a fully resolved GSM reference (has collection, group, and field).
func IsGSMReference(c api.CredentialReference) bool {
	return c.Collection != "" && c.Group != "" && c.Field != ""
}

// GroupCredentialsByMountPath groups resolved credentials by mount_path to produce
// one CSI volume per unique path. Expects all bundles and auto-discovery to have
// been resolved into concrete (collection, group, field) tuples beforehand.
func GroupCredentialsByMountPath(credentials []api.CredentialReference) map[string][]api.CredentialReference {
	mountGroups := make(map[string][]api.CredentialReference)
	for _, credential := range credentials {
		mountGroups[credential.MountPath] = append(mountGroups[credential.MountPath], credential)
	}
	return mountGroups
}

// BuildGCPSecretsParameter builds the YAML "secrets" parameter for a SecretProviderClass
// from the given credentials. Each credential maps to a GSM secret path and a mount file name.
func BuildGCPSecretsParameter(credentials []api.CredentialReference) (string, error) {
	var secrets []config.Secret
	for _, credential := range credentials {
		gsmSecretName := gsm.GetGSMSecretName(credential.Collection, credential.Group, credential.Field)
		mountName := credential.Field
		if credential.As != "" {
			mountName = credential.As
		}
		mountName, err := RestoreForbiddenSymbolsInSecretName(mountName)
		if err != nil {
			return "", fmt.Errorf("invalid mount name '%s': %w", mountName, err)
		}
		secrets = append(secrets, config.Secret{
			ResourceName: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", GSMProject, gsmSecretName),
			FileName:     mountName,
		})
	}
	secretsYaml, err := yaml.Marshal(secrets)
	if err != nil {
		return "", fmt.Errorf("could not marshal secrets: %w", err)
	}
	return string(secretsYaml), nil
}

// RestoreForbiddenSymbolsInSecretName replaces all replacement substrings with the original symbols,
// e.g. '--dot--awscreds' to '.awscreds'
func RestoreForbiddenSymbolsInSecretName(s string) (string, error) {
	// This is an unfortunate workaround needed for the initial migration.
	// Google Secret Manager doesn't support dots in Secret names. Due to migration from Vault,
	// where we had to shard each (multi key-value) Vault secret into multiple ones in GSM,
	// some of secret names, or their keys, contained forbidden symbols, usually '.' (dot) and '/' (slash) in their names.
	// Because all credentials with these forbidden symbols in their names or keys have been renamed,
	// e.g. '.awscreds' to '--dot--awscreds', to preserve backwards compatibility,
	// we now need to mount the secret as the original '.awscreds' file to the Pod that will be created by ci-operator.

	replacedName := gsmvalidation.DenormalizeName(s)

	re := regexp.MustCompile(`[^a-zA-Z0-9\-._/]`)
	invalidCharacters := re.FindAllString(replacedName, -1)
	if invalidCharacters != nil {
		return "", fmt.Errorf("secret name '%s' decodes to '%s' which contains forbidden characters (%s); decoded names must only contain letters, numbers, dashes (-), dots (.), underscores (_), and slashes (/)", s, replacedName, strings.Join(invalidCharacters, ", "))
	} else {
		return replacedName, nil
	}
}

// GetSPCName generates a unique SPC name for a set of credentials that share
// a mount path. The hash includes the mount path and sorted collection:group:field
// tuples to ensure uniqueness even when credentials span multiple collections.
func GetSPCName(namespace string, credentials []api.CredentialReference) string {
	if len(credentials) == 0 {
		return strings.ToLower(fmt.Sprintf("%s-empty-spc", namespace))
	}
	mountPath := credentials[0].MountPath

	var parts []string
	parts = append(parts, mountPath)

	var credKeys []string
	for _, cred := range credentials {
		credKeys = append(credKeys, fmt.Sprintf("%s:%s:%s", cred.Collection, cred.Group, cred.Field))
	}
	sort.Strings(credKeys)
	parts = append(parts, credKeys...)

	hash := sha256.Sum256([]byte(strings.Join(parts, "-")))
	hashStr := fmt.Sprintf("%x", hash[:12])
	name := fmt.Sprintf("%s-%s-spc", namespace, hashStr)

	return strings.ToLower(name)
}

// GetCSIVolumeName generates a deterministic, DNS-compliant name for a CSI volume
// based on the namespace and mount path. One mount path produces one CSI volume.
func GetCSIVolumeName(ns string, credentials []api.CredentialReference) string {
	if len(credentials) == 0 {
		return strings.ToLower(fmt.Sprintf("%s-empty-vol", ns))
	}
	mountPath := credentials[0].MountPath

	hash := sha256.Sum256([]byte(mountPath))
	hashStr := fmt.Sprintf("%x", hash[:8])
	name := fmt.Sprintf("%s-%s", ns, hashStr)

	if len(name) > KubernetesDNSLabelLimit {
		hashStr := fmt.Sprintf("%x", hash[:16])
		name = hashStr
	}

	return strings.ToLower(name)
}

// GetCensorMountPath returns the mount path used for sidecar censoring of a GSM secret.
func GetCensorMountPath(secretName string) string {
	return fmt.Sprintf("/censor/%s", secretName)
}

// BuildSecretProviderClass constructs a SecretProviderClass object for the GCP provider.
func BuildSecretProviderClass(name, namespace, secrets string) *csiapi.SecretProviderClass {
	return &csiapi.SecretProviderClass{
		TypeMeta: meta.TypeMeta{
			Kind:       "SecretProviderClass",
			APIVersion: csiapi.GroupVersion.String(),
		},
		ObjectMeta: meta.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: csiapi.SecretProviderClassSpec{
			Provider: "gcp",
			Parameters: map[string]string{
				"auth":    "provider-adc",
				"secrets": secrets,
			},
		},
	}
}

// BuildCSIVolume constructs a CSI volume that references the given SecretProviderClass.
func BuildCSIVolume(name, spcName string) coreapi.Volume {
	return coreapi.Volume{
		Name: name,
		VolumeSource: coreapi.VolumeSource{
			CSI: &coreapi.CSIVolumeSource{
				Driver:   "secrets-store.csi.k8s.io",
				ReadOnly: ptr.To(true),
				VolumeAttributes: map[string]string{
					"secretProviderClass": spcName,
				},
			},
		},
	}
}
