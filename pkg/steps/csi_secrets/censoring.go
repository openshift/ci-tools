package csi_secrets

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
	"github.com/openshift/ci-tools/pkg/secrets"
)

func RegisterGSMCredentialsForCensoring(
	ctx context.Context,
	client gsm.SecretManagerClient,
	projectConfig gsm.Config,
	censor *secrets.DynamicCensor,
	credentials []api.CredentialReference,
) error {
	if client == nil || censor == nil {
		return nil
	}

	seen := sets.New[string]()
	for _, credential := range credentials {
		if !IsGSMReference(credential) {
			continue
		}
		name := gsm.GetGSMSecretName(credential.Collection, credential.Group, credential.Field)
		if seen.Has(name) {
			continue
		}
		seen.Insert(name)

		resourceName := gsm.GetGSMSecretResourceName(projectConfig.ProjectIdNumber,
			credential.Collection, credential.Group, credential.Field)
		payload, err := gsm.GetSecretPayload(ctx, client, resourceName)
		if err != nil {
			return fmt.Errorf("could not read GSM secret %s in order to censor it: %w", name, err)
		}
		censor.AddSecrets(string(payload))
	}
	return nil
}
