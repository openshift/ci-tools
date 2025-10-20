package secrets

import (
	"context"
	"fmt"
	"regexp"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"

	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
)

type gsmSyncDecorator struct {
	Client
	gsmClient           *secretmanager.Client
	config              gsm.Config
	ctx                 context.Context
	pattern             *regexp.Regexp
	secretsInCollection map[string]bool // individual secrets in the cluster-init collection
}

func NewGSMSyncDecorator(wrappedVaultClient Client, gcpProjectConfig gsm.Config, credentialsFile string) (Client, error) {
	ctx := context.Background()

	var opts []option.ClientOption
	if credentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(credentialsFile))
	}

	gsmClient, err := secretmanager.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GSM client: %w", err)
	}

	// Hardcoded pattern to match only the fields created for cluster-init secret
	pattern := regexp.MustCompile(`^sa\.cluster-init\..*`)

	return &gsmSyncDecorator{
		Client:              wrappedVaultClient,
		gsmClient:           gsmClient,
		config:              gcpProjectConfig,
		pattern:             pattern,
		ctx:                 ctx,
		secretsInCollection: make(map[string]bool),
	}, nil
}

func (g *gsmSyncDecorator) SetFieldOnItem(itemName, fieldName string, fieldValue []byte) error {
	// Call the original client (Vault)
	if err := g.Client.SetFieldOnItem(itemName, fieldName, fieldValue); err != nil {
		return err
	}

	// Check if this field should sync to GSM (only cluster-init secrets)
	if !g.pattern.MatchString(fieldName) {
		return nil
	}

	// replace forbidden characters:
	// e.g., "sa.cluster-init.build01.config" -> "sa--dot--cluster-init--dot--build01--dot--config"
	fieldNameNormalized := gsm.NormalizeSecretName(fieldName)
	secretName := fmt.Sprintf("%s__%s", "cluster-init", fieldNameNormalized)

	labels := make(map[string]string)
	labels["jira-project"] = "dptp"

	annotations := make(map[string]string)
	annotations["request-information"] = "Created by periodic-ci-secret-generator."

	if err := gsm.CreateOrUpdateSecret(g.ctx, g.gsmClient, g.config.ProjectIdNumber, secretName, fieldValue, labels, annotations); err != nil {
		logrus.WithError(err).Errorf("Failed to sync to GSM: %s", secretName)
		// Don't fail the Vault write
	} else {
		logrus.Debugf("Successfully synced cluster-init secret to GSM: %s", secretName)
		g.secretsInCollection[fieldNameNormalized] = true
	}

	return nil
}

func (g *gsmSyncDecorator) UpdateIndexSecret() error {
	indexSecretName := gsm.GetIndexSecretName("cluster-init")

	var payload []byte
	for secretName := range g.secretsInCollection {
		payload = fmt.Appendf(payload, "- %s\n", secretName)
	}

	labels := make(map[string]string)
	labels["jira-project"] = "dptp"

	annotations := make(map[string]string)
	annotations["request-information"] = "Created by periodic-ci-secret-generator."

	if err := gsm.CreateOrUpdateSecret(g.ctx, g.gsmClient, g.config.ProjectIdNumber, indexSecretName, payload, labels, annotations); err != nil {
		return fmt.Errorf("failed to update index secret %s: %w", indexSecretName, err)
	}

	logrus.Infof("Successfully updated index secret: %s with %d entries", indexSecretName, len(g.secretsInCollection))
	return nil
}
