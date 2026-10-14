package csi_secrets

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

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
		addYAMLValuesToCensor(payload, censor)
	}
	return nil
}

// addYAMLValuesToCensor parses the payload as YAML and recursively adds every
// string value found in mappings and sequences as a separate censor pattern.
// This ensures that values extracted individually from a YAML secret (via yq,
// awk, sed, cut, etc.) are also censored in CI logs, not just the full file
// content. Non-YAML payloads are silently ignored.
func addYAMLValuesToCensor(payload []byte, censor *secrets.DynamicCensor) {
	var data interface{}
	if err := yaml.Unmarshal(payload, &data); err != nil || data == nil {
		return
	}
	collectStrings(data, censor)
}

func collectStrings(v interface{}, censor *secrets.DynamicCensor) {
	switch val := v.(type) {
	case string:
		if val != "" {
			censor.AddSecrets(val)
		}
	case map[string]interface{}:
		for _, child := range val {
			collectStrings(child, censor)
		}
	case []interface{}:
		for _, child := range val {
			collectStrings(child, censor)
		}
	}
}
