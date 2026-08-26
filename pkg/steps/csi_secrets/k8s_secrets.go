package csi_secrets

import (
	"context"
	"fmt"
	"strings"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
)

// SourceSecretName is the collision-resistant name a cross-namespace source
// secret is copied to inside the test namespace. We don't want secrets imported
// from separate namespaces to collide but we want to keep them generally
// recognizable for debugging, and the chance we get a second-level collision
// (ns-a, name) and (ns, a-name) is small, so we can get away with this string
// prefixing.
func SourceSecretName(namespace, name string) string {
	return fmt.Sprintf("%s-%s", namespace, name)
}

// K8sSecretVolumeName builds a DNS-1123 compliant volume name for a K8s Secret
// credential copied from another namespace.
func K8sSecretVolumeName(namespace, name string) string {
	return strings.ReplaceAll(SourceSecretName(namespace, name), ".", "-")
}

// CreateSourceCredentials copies each referenced source Kubernetes secret into
// targetNamespace under its SourceSecretName. It is used for credentials backed
// by an existing K8s Secret on the cluster (bundles with sync_to_cluster: true,
// which includes cluster profile bundles) rather than by GSM.
func CreateSourceCredentials(ctx context.Context, client ctrlruntimeclient.Client, targetNamespace string, credentials []api.CredentialReference) error {
	toCreate := map[string]*coreapi.Secret{}
	for _, credential := range credentials {
		name := SourceSecretName(credential.Namespace, credential.Name)
		if _, ok := toCreate[name]; ok {
			continue
		}
		raw := &coreapi.Secret{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: credential.Namespace, Name: credential.Name}, raw); err != nil {
			return fmt.Errorf("could not read source credential: %w", err)
		}
		toCreate[name] = &coreapi.Secret{
			TypeMeta: raw.TypeMeta,
			ObjectMeta: meta.ObjectMeta{
				Name:      name,
				Namespace: targetNamespace,
			},
			Type:       raw.Type,
			Data:       raw.Data,
			StringData: raw.StringData,
		}
	}
	for name := range toCreate {
		if err := client.Create(ctx, toCreate[name]); err != nil && !kerrors.IsAlreadyExists(err) {
			return fmt.Errorf("could not create source credential: %w", err)
		}
	}
	return nil
}
