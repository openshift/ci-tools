package csi_secrets

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api"
	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
)

// ValidateNoFileCollisionsOnMountPath ensures that no two credentials at the same
// mount path produce the same file name. Checks across all collections and groups.
// Expects credentials to already have been resolved into concrete (collection, group, field) tuples.
func ValidateNoFileCollisionsOnMountPath(credentials []api.CredentialReference) error {
	byMountPath := make(map[string][]api.CredentialReference)
	for _, cred := range credentials {
		byMountPath[cred.MountPath] = append(byMountPath[cred.MountPath], cred)
	}

	for mountPath, creds := range byMountPath {
		type credSource struct {
			collection, group, field string
		}
		seenFiles := make(map[string]credSource)
		for _, cred := range creds {
			fileName := cred.Field
			if cred.As != "" {
				fileName = cred.As
			}
			restored, err := RestoreForbiddenSymbolsInSecretName(fileName)
			if err != nil {
				return fmt.Errorf("invalid field name %q at mount_path %s: %w", fileName, mountPath, err)
			}
			src := credSource{cred.Collection, cred.Group, cred.Field}
			if existing, ok := seenFiles[restored]; ok {
				return fmt.Errorf(
					"file name collision at mount_path=%s: %s/%s/%s and %s/%s/%s both produce file %q",
					mountPath,
					existing.collection, existing.group, existing.field,
					src.collection, src.group, src.field,
					restored,
				)
			}
			seenFiles[restored] = src
		}
	}
	return nil
}

func getBundle(config *api.GSMConfig, name string) *api.GSMBundle {
	for i := range config.Bundles {
		if config.Bundles[i].Name == name {
			return &config.Bundles[i]
		}
	}
	return nil
}

// ResolveCredentialReferences resolves bundle references and auto-discovery
// to concrete (collection, group, field) tuples.
//
// Handles three cases of possible credential entries:
// 1. GSMBundle reference -> expands to all secrets in bundle
// 2. Auto-discovery (collection+group, no field) -> discovers relevant fields from GSM
// 3. Explicit field -> passes through unchanged
func ResolveCredentialReferences(
	ctx context.Context,
	credentials []api.CredentialReference,
	gsmConfig *api.GSMConfig,
	gsmClient gsm.SecretManagerClient,
	gsmProjectConfig gsm.Config,
	discoveredFields map[CollectionGroupKey][]string,
) ([]api.CredentialReference, error) {
	var resolvedCredentials []api.CredentialReference
	var errs []error

	if discoveredFields == nil {
		discoveredFields = make(map[CollectionGroupKey][]string)
	}

	for _, cred := range credentials {
		if cred.IsBundleReference() {
			if gsmConfig == nil {
				errs = append(errs, fmt.Errorf("bundle reference %q requires gsm-config file, but it is not loaded", cred.Bundle))
				break
			}
			bundle := getBundle(gsmConfig, cred.Bundle)
			if bundle == nil {
				errs = append(errs, fmt.Errorf("bundle %q not found in config file", cred.Bundle))
				break
			}

			if bundle.SyncToCluster {
				// K8s Secret bundle - convert to K8s Secret reference
				if cred.Namespace == "" {
					errs = append(errs, fmt.Errorf("bundle %q has sync_to_cluster: true but credential has no namespace specified", cred.Bundle))
					break
				}
				resolvedCredentials = append(resolvedCredentials, api.CredentialReference{
					Namespace: cred.Namespace,
					Name:      bundle.Name,
					MountPath: cred.MountPath,
				})
				continue
			}
			expanded, err := expandBundle(ctx, bundle, gsmClient, gsmProjectConfig, discoveredFields)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to expand bundle %q: %w", cred.Bundle, err))
				break
			}

			for _, r := range expanded {
				resolvedCredentials = append(resolvedCredentials, api.CredentialReference{
					Collection: r.Collection,
					Group:      r.Group,
					Field:      r.Field,
					As:         r.As,
					MountPath:  cred.MountPath,
				})
			}

		} else if cred.IsAutoDiscovery() {
			if gsmClient == nil {
				errs = append(errs, fmt.Errorf("auto-discovery for %s__%s requires GSM client, but client is not initialized", cred.Collection, cred.Group))
				break
			}
			key := CollectionGroupKey{
				cred.Collection,
				cred.Group,
			}
			if _, found := discoveredFields[key]; !found {
				fieldNames, err := gsm.ListSecretFieldsByCollectionAndGroup(
					ctx, gsmClient, gsmProjectConfig, cred.Collection, cred.Group)
				if err != nil {
					errs = append(errs, fmt.Errorf("auto-discovery failed for %s__%s: %w",
						cred.Collection, cred.Group, err))
					break // if any credential is not correctly set up or available in GSM, we want to fail the whole test
				}
				discoveredFields[key] = fieldNames
			}

			for _, field := range discoveredFields[key] {
				resolvedCredentials = append(resolvedCredentials, api.CredentialReference{
					Collection: cred.Collection,
					Group:      cred.Group,
					Field:      field,
					MountPath:  cred.MountPath,
				})
			}

		} else if cred.IsExplicitField() {
			resolvedCredentials = append(resolvedCredentials, api.CredentialReference{
				Collection: cred.Collection,
				Group:      cred.Group,
				Field:      cred.Field,
				As:         cred.As,
				MountPath:  cred.MountPath,
			})
		} else {
			errs = append(errs, fmt.Errorf("invalid credential reference: must provide bundle, collection+group, or collection+group+field"))
			break
		}
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}

	return resolvedCredentials, nil
}

// expandBundle expands a bundle definition into individual credential references.
// If fields are not specified for a secret entry, it uses auto-discovery to list all fields.
func expandBundle(
	ctx context.Context,
	bundle *api.GSMBundle,
	gsmClient gsm.SecretManagerClient,
	gsmProjectConfig gsm.Config,
	discoveredFields map[CollectionGroupKey][]string,
) ([]api.CredentialReference, error) {
	var resolvedCredentials []api.CredentialReference

	for _, secretEntry := range bundle.GSMSecrets {
		if len(secretEntry.Fields) == 0 { // if fields are not specified, discover them using GSM listing
			if gsmClient == nil {
				return nil, fmt.Errorf("bundle auto-discovery for %s__%s requires GSM client, but client is not initialized", secretEntry.Collection, secretEntry.Group)
			}
			key := CollectionGroupKey{
				secretEntry.Collection,
				secretEntry.Group,
			}

			// check if we've already discovered fields for this collection+group
			if _, alreadyDiscovered := discoveredFields[key]; !alreadyDiscovered {
				fieldNames, err := gsm.ListSecretFieldsByCollectionAndGroup(
					ctx, gsmClient, gsmProjectConfig, secretEntry.Collection, secretEntry.Group)
				if err != nil {
					return nil, fmt.Errorf("failed to list fields for collection=%s, group=%s: %w",
						secretEntry.Collection, secretEntry.Group, err)
				}
				discoveredFields[key] = fieldNames
				logrus.Debugf("discovered %d fields for collection=%s, group=%s", len(fieldNames), secretEntry.Collection, secretEntry.Group)
			}

			for _, fieldName := range discoveredFields[key] {
				resolvedCredentials = append(resolvedCredentials, api.CredentialReference{
					Collection: secretEntry.Collection,
					Group:      secretEntry.Group,
					Field:      fieldName,
				})
			}
		} else {
			for _, fieldEntry := range secretEntry.Fields {
				resolvedCredentials = append(resolvedCredentials, api.CredentialReference{
					Collection: secretEntry.Collection,
					Group:      secretEntry.Group,
					Field:      fieldEntry.Name,
					As:         fieldEntry.As,
				})
			}
		}
	}

	return resolvedCredentials, nil
}
