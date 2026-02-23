package multi_stage

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api"
	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
)

type collectionGroupKey struct {
	collection string
	group      string
}

// ValidateNoGroupCollisionsOnMountPath ensures that different groups within the same collection
// don't share a mount path, which could cause file name collisions.
//
// Example of invalid configuration:
//   - collection: my-creds, group: aws, field: access-key, mount_path: /tmp/secrets
//   - collection: my-creds, group: gcp, field: access-key, mount_path: /tmp/secrets
//
// Both would try to create /tmp/secrets/access-key.
// Expects the credentials to already have been resolved into concrete (collection, group, field) tuples.
func ValidateNoGroupCollisionsOnMountPath(credentials []api.CredentialReference) error {
	type collectionMountKey struct {
		collection string
		mountPath  string
	}
	mountPathGroups := make(map[collectionMountKey]map[string]bool)

	for _, cred := range credentials {
		key := collectionMountKey{
			collection: cred.Collection,
			mountPath:  cred.MountPath,
		}
		if mountPathGroups[key] == nil {
			mountPathGroups[key] = make(map[string]bool)
		}
		mountPathGroups[key][cred.Group] = true
	}

	for key, groups := range mountPathGroups {
		if len(groups) > 1 {
			var groupList []string
			for group := range groups {
				groupList = append(groupList, group)
			}
			return fmt.Errorf("multiple groups (%v) found for collection=%s, mount_path=%s - different groups in the same collection must use different mount paths to avoid file name collisions",
				groupList, key.collection, key.mountPath)
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
	discoveredFields map[collectionGroupKey][]string,
) ([]api.CredentialReference, error) {
	var resolvedCredentials []api.CredentialReference
	var errs []error

	// Track discovered fields to avoid redundant GSM calls
	if discoveredFields == nil {
		discoveredFields = make(map[collectionGroupKey][]string)
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
			key := collectionGroupKey{
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
	discoveredFields map[collectionGroupKey][]string,
) ([]api.CredentialReference, error) {
	var resolvedCredentials []api.CredentialReference

	for _, secretEntry := range bundle.GSMSecrets {
		if len(secretEntry.Fields) == 0 { // if fields are not specified, discover them using GSM listing
			if gsmClient == nil {
				return nil, fmt.Errorf("bundle auto-discovery for %s__%s requires GSM client, but client is not initialized", secretEntry.Collection, secretEntry.Group)
			}
			key := collectionGroupKey{
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
