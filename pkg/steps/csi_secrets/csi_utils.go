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

func IsK8sSecretReference(c api.CredentialReference) bool {
	return c.Namespace != "" && c.Name != ""
}

func IsGSMReference(c api.CredentialReference) bool {
	return c.Collection != "" && c.Group != "" && c.Field != ""
}

// GroupCredentialsByCollectionGroupAndMountPath groups credentials by (collection, group, mount_path)
// to avoid duplicate mount paths, which causes Kubernetes errors.
func GroupCredentialsByCollectionGroupAndMountPath(credentials []api.CredentialReference) map[string][]api.CredentialReference {
	mountGroups := make(map[string][]api.CredentialReference)
	for _, credential := range credentials {
		key := fmt.Sprintf("%s:%s:%s", credential.Collection, credential.Group, credential.MountPath)
		mountGroups[key] = append(mountGroups[key], credential)
	}
	return mountGroups
}

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

// GetSPCName generates a unique SPC name for a set of credentials.
// All credentials in the input slice must have the same collection, group, and mount path
// (enforced by GroupCredentialsByCollectionGroupAndMountPath and ValidateNoGroupCollisionsOnMountPath).
// The hash includes collection, group, mount path, and sorted field names to ensure uniqueness.
func GetSPCName(namespace string, credentials []api.CredentialReference) string {
	if len(credentials) == 0 {
		return strings.ToLower(fmt.Sprintf("%s-empty-spc", namespace))
	}
	collection := credentials[0].Collection
	group := credentials[0].Group
	mountPath := credentials[0].MountPath

	var parts []string
	parts = append(parts, collection, group, mountPath)

	// Sort credential field names for deterministic hashing
	var credFields []string
	for _, cred := range credentials {
		credFields = append(credFields, cred.Field)
	}
	sort.Strings(credFields)
	parts = append(parts, credFields...)

	hash := sha256.Sum256([]byte(strings.Join(parts, "-")))
	hashStr := fmt.Sprintf("%x", hash[:12])
	name := fmt.Sprintf("%s-%s-spc", namespace, hashStr)

	return strings.ToLower(name)
}

// GetCSIVolumeName generates a deterministic, DNS-compliant name for a CSI volume
// based on the namespace and credentials. All credentials in the slice must have
// the same collection, group, and mount path.
//
// The name is constructed as "<namespace>-<hash>", where the hash is computed from
// the collection, group and mountPath. If the resulting name exceeds 63 characters
// (the Kubernetes DNS label limit), only the hash is used as the name.
func GetCSIVolumeName(ns string, credentials []api.CredentialReference) string {
	if len(credentials) == 0 {
		return strings.ToLower(fmt.Sprintf("%s-empty-vol", ns))
	}
	collection := credentials[0].Collection
	group := credentials[0].Group
	mountPath := credentials[0].MountPath

	hash := sha256.Sum256([]byte(strings.Join([]string{collection, group, mountPath}, "-")))
	hashStr := fmt.Sprintf("%x", hash[:8])
	name := fmt.Sprintf("%s-%s", ns, hashStr)

	if len(name) > KubernetesDNSLabelLimit {
		hashStr := fmt.Sprintf("%x", hash[:16])
		name = hashStr
	}

	return strings.ToLower(name)
}

func GetCensorMountPath(secretName string) string {
	return fmt.Sprintf("/censor/%s", secretName)
}

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

// BuildSPCsForCredentials builds all SecretProviderClass objects needed for the given credentials.
// It creates one SPC per (collection, group, mount_path) group for actual volumes, and one SPC
// per unique credential for sidecar censoring. Callers are responsible for creating the returned
// objects in the cluster.
func BuildSPCsForCredentials(namespace string, credentials []api.CredentialReference) (map[string]*csiapi.SecretProviderClass, error) {
	spcsToCreate := map[string]*csiapi.SecretProviderClass{}
	collectionMountGroups := GroupCredentialsByCollectionGroupAndMountPath(credentials)

	for _, credentials := range collectionMountGroups {
		spcName := GetSPCName(namespace, credentials)
		secrets, err := BuildGCPSecretsParameter(credentials)
		if err != nil {
			return nil, fmt.Errorf("could not marshal secrets for mount path %s: %w", credentials[0].MountPath, err)
		}
		spcsToCreate[spcName] = BuildSecretProviderClass(spcName, namespace, secrets)
	}

	seenCredentials := make(map[string]bool)
	for _, credential := range credentials {
		fullSecretName := gsm.GetGSMSecretName(credential.Collection, credential.Group, credential.Field)
		if seenCredentials[fullSecretName] {
			continue
		}
		seenCredentials[fullSecretName] = true

		censorMountPath := GetCensorMountPath(fullSecretName)
		censoredCredential := credential
		censoredCredential.MountPath = censorMountPath
		individualCredentials := []api.CredentialReference{censoredCredential}
		spcName := GetSPCName(namespace, individualCredentials)

		secrets, err := BuildGCPSecretsParameter(individualCredentials)
		if err != nil {
			return nil, fmt.Errorf("could not marshal secrets for censored credential %s: %w", fullSecretName, err)
		}
		spcsToCreate[spcName] = BuildSecretProviderClass(spcName, namespace, secrets)
	}

	return spcsToCreate, nil
}
