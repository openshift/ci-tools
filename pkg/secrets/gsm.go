package secrets

import (
	"context"
	"fmt"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"

	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
)

type gsmSyncDecorator struct {
	Client
	gsmClient *secretmanager.Client
	config    gsm.Config
	ctx       context.Context
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

	return &gsmSyncDecorator{
		Client:    wrappedVaultClient,
		gsmClient: gsmClient,
		config:    gcpProjectConfig,
		ctx:       ctx,
	}, nil
}

func (g *gsmSyncDecorator) SetFieldOnItem(itemName, fieldName string, fieldValue []byte) error {
	// Call the original client (Vault)
	if err := g.Client.SetFieldOnItem(itemName, fieldName, fieldValue); err != nil {
		return err
	}

	// replace forbidden characters:
	// e.g., "sa.cluster-init.build01.config" -> "sa--dot--cluster-init--dot--build01--dot--config"
	fieldNameNormalized := gsmvalidation.NormalizeName(fieldName)
	secretName := fmt.Sprintf("%s__%s", "cluster-init", fieldNameNormalized)
	fieldNameNormalized := gsm.NormalizeSecretName(fieldName)

	// item name will become the collection name:
	secretName := fmt.Sprintf("%s__%s", itemName, fieldNameNormalized)

	labels := make(map[string]string)
	labels["jira-project"] = "dptp"

	annotations := make(map[string]string)
	annotations["request-information"] = "Created by periodic-ci-secret-generator."

	if err := gsm.CreateOrUpdateSecret(g.ctx, g.gsmClient, g.config.ProjectIdNumber, secretName, fieldValue, labels, annotations); err != nil {
		logrus.WithError(err).Errorf("Failed to sync to GSM: %s", secretName)
		// Don't fail the Vault write
	} else {
		logrus.Debugf("Successfully synced secret '%s' to GSM", secretName)
	}

	return nil
}
