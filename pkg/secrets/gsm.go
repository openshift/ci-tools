package secrets

import (
	"context"
	"fmt"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"

	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
	"github.com/openshift/ci-tools/pkg/vaultclient"
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
	opts := []option.ClientOption{option.WithCredentialsFile(credentialsFile)}

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
	group := gsmvalidation.NormalizeName(itemName)
	field := gsmvalidation.NormalizeName(fieldName)
	secretName := gsm.GetGSMSecretName(TestPlatformCollection, group, field)

	vaultErr := g.Client.SetFieldOnItem(itemName, fieldName, fieldValue)
	if err := gsm.CreateOrUpdateSecretDestroyingPreviousVersions(g.ctx, g.gsmClient, g.config.ProjectIdNumber, secretName, fieldValue,
		map[string]string{"jira-project": "dptp"},
		map[string]string{"request-information": "Created by periodic-ci-secret-generator."},
	); err != nil {
		logrus.WithError(err).Errorf("Failed to sync to GSM: %s", secretName)
		if vaultErr != nil {
			return vaultErr
		}
		return err
	}
	if vaultErr != nil && !vaultclient.IsWriteForbidden(vaultErr) {
		return vaultErr
	}
	if vaultErr != nil {
		logrus.WithError(vaultErr).Warnf("Vault write skipped for %s/%s (read-only); GSM sync succeeded", itemName, fieldName)
	}
	return nil
}

func (g *gsmSyncDecorator) UpdateIndexSecret(itemName string, payload []byte) error {
	annotations := make(map[string]string)
	annotations["request-information"] = "Created by periodic-ci-secret-generator."
	if err := gsm.CreateOrUpdateSecretDestroyingPreviousVersions(g.ctx, g.gsmClient, g.config.ProjectIdNumber, gsm.GetIndexSecretName(itemName), payload, nil, annotations); err != nil {
		return err
	}
	return nil
}
