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

const (
	TestPlatformCollection = "test-platform-infra"
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

// SetFieldOnItem syncs a secret field to both Vault and GSM.
// In the 3-level GSM hierarchy (collection__group__field):
//   - collection: TestPlatformCollection constant ("test-platform-infra")
//   - group: itemName parameter (e.g., "cluster-init", "build-farm")
//   - field: fieldName parameter (e.g., "sa.ci-operator.app.ci.config")
//
// Example: SetFieldOnItem("build-farm", "token.txt", data)
//
//	-> GSM secret: test-platform-infra__build-farm__token--dot--txt
func (g *gsmSyncDecorator) SetFieldOnItem(itemName, fieldName string, fieldValue []byte) error {
	// Call the original client (Vault)
	if err := g.Client.SetFieldOnItem(itemName, fieldName, fieldValue); err != nil {
		return err
	}

	group := gsmvalidation.NormalizeName(itemName)
	field := gsmvalidation.NormalizeName(fieldName)
	secretName := gsm.GetGSMSecretName(TestPlatformCollection, group, field)

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

func (g *gsmSyncDecorator) UpdateIndexSecret(itemName string, payload []byte) error {
	annotations := make(map[string]string)
	annotations["request-information"] = "Created by periodic-ci-secret-generator."
	if err := gsm.CreateOrUpdateSecret(g.ctx, g.gsmClient, g.config.ProjectIdNumber, gsm.GetIndexSecretName(itemName), payload, nil, annotations); err != nil {
		return err
	}
	return nil
}
