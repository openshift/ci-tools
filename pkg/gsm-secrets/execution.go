package gsmsecrets

import (
	"context"
	"fmt"

	iamadmin "cloud.google.com/go/iam/admin/apiv1"
	"cloud.google.com/go/iam/admin/apiv1/adminpb"
	"cloud.google.com/go/iam/apiv1/iampb"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/googleapis/gax-go/v2"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Client interfaces - these should be defined by the cmd package
type SecretManagerClient interface {
	ListSecrets(ctx context.Context, req *secretmanagerpb.ListSecretsRequest, opts ...gax.CallOption) *secretmanager.SecretIterator
	DeleteSecret(ctx context.Context, req *secretmanagerpb.DeleteSecretRequest, opts ...gax.CallOption) error
	CreateSecret(ctx context.Context, req *secretmanagerpb.CreateSecretRequest, opts ...gax.CallOption) (*secretmanagerpb.Secret, error)
	AddSecretVersion(ctx context.Context, req *secretmanagerpb.AddSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.SecretVersion, error)
}

type ResourceManagerClient interface {
	SetIamPolicy(ctx context.Context, req *iampb.SetIamPolicyRequest, opts ...gax.CallOption) (*iampb.Policy, error)
	GetIamPolicy(ctx context.Context, req *iampb.GetIamPolicyRequest, opts ...gax.CallOption) (*iampb.Policy, error)
}

type IAMClient interface {
	CreateServiceAccountKey(ctx context.Context, req *adminpb.CreateServiceAccountKeyRequest, opts ...gax.CallOption) (*adminpb.ServiceAccountKey, error)
	CreateServiceAccount(ctx context.Context, req *adminpb.CreateServiceAccountRequest, opts ...gax.CallOption) (*adminpb.ServiceAccount, error)
	DeleteServiceAccount(ctx context.Context, req *adminpb.DeleteServiceAccountRequest, opts ...gax.CallOption) error
	ListServiceAccounts(ctx context.Context, req *adminpb.ListServiceAccountsRequest, opts ...gax.CallOption) *iamadmin.ServiceAccountIterator
	ListServiceAccountKeys(ctx context.Context, req *adminpb.ListServiceAccountKeysRequest, opts ...gax.CallOption) (*adminpb.ListServiceAccountKeysResponse, error)
	DeleteServiceAccountKey(ctx context.Context, req *adminpb.DeleteServiceAccountKeyRequest, opts ...gax.CallOption) error
}

// ExecuteActions performs the actual resource changes in GCP based on the computed diff.
func (a *Actions) ExecuteActions(ctx context.Context, iamClient IAMClient, secretsClient SecretManagerClient, projectsClient ResourceManagerClient) {
	if len(a.SAsToCreate) > 0 {
		logrus.Info("Creating service accounts")
		a.CreateServiceAccounts(ctx, iamClient)
	}

	if len(a.SecretsToCreate) > 0 {
		logrus.Info("Creating secrets")
		a.CreateSecrets(ctx, secretsClient, iamClient)
	}

	if a.ConsolidatedIAMPolicy != nil {
		logrus.Info("Applying IAM policy")
		if err := a.ApplyPolicy(ctx, projectsClient); err != nil {
			logrus.WithError(err).Fatal("Failed to apply IAM policy")
		}
	}

	if len(a.SAsToDelete) > 0 {
		logrus.Info("Revoking obsolete service account keys")
		a.RevokeObsoleteServiceAccountKeys(ctx, iamClient)

		logrus.Info("Deleting obsolete service accounts")
		a.DeleteObsoleteServiceAccounts(ctx, iamClient)
	}

	if len(a.SecretsToDelete) > 0 {
		logrus.Info("Deleting obsolete secrets")
		a.DeleteObsoleteSecrets(ctx, secretsClient)
	}
}

func (a *Actions) CreateServiceAccounts(ctx context.Context, client IAMClient) {
	for _, sa := range a.SAsToCreate {
		request := &adminpb.CreateServiceAccountRequest{
			Name:      GetProjectResourceString(a.Config.ProjectIdString),
			AccountId: sa.DisplayName,
			ServiceAccount: &adminpb.ServiceAccount{
				DisplayName: sa.DisplayName,
			},
		}
		secretName := GetUpdaterSASecretName(sa.Collection)
		newSA, err := client.CreateServiceAccount(ctx, request)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to create service account: %s", sa.DisplayName)
			delete(a.SecretsToCreate, secretName)
			continue
		}
		logrus.Debugf("Service account created: %s", newSA.Email)
		logrus.Debugf("Generating key for service account: %s", newSA.Email)
		keyData, err := GenerateServiceAccountKey(ctx, client, newSA.Email, a.Config.ProjectIdString)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to generate key for service account: %s", newSA.Email)
			delete(a.SecretsToCreate, secretName)
			continue
		}

		secret := a.SecretsToCreate[secretName]
		secret.Payload = keyData
		a.SecretsToCreate[secretName] = secret

		logrus.Infof("Created service account: %s", sa.Email)
	}
}

func GenerateServiceAccountKey(ctx context.Context, client IAMClient, saEmail string, projectID string) ([]byte, error) {
	name := fmt.Sprintf("%s/serviceAccounts/%s", GetProjectResourceString(projectID), saEmail)
	key, err := client.CreateServiceAccountKey(ctx, &adminpb.CreateServiceAccountKeyRequest{
		Name: name,
	})
	if err != nil {
		return nil, err
	}
	return key.GetPrivateKeyData(), nil
}

func (a *Actions) CreateSecrets(ctx context.Context, secretsClient SecretManagerClient, iamClient IAMClient) {
	for name, s := range a.SecretsToCreate {
		if s.Type == SecretTypeSA && len(s.Payload) == 0 {
			logrus.Debugf("Generating missing key for service account for collection '%s'", s.Collection)
			email := GetUpdaterSAEmail(s.Collection, a.Config)
			keyData, err := GenerateServiceAccountKey(ctx, iamClient, email, a.Config.ProjectIdString)
			if err != nil {
				logrus.WithError(err).Errorf("Failed to generate key for service account: %s", email)
				continue
			}
			s.Payload = keyData
			a.SecretsToCreate[name] = s
		}

		if s.Type == SecretTypeIndex {
			s.Payload = fmt.Appendf(nil, "- %s", GetUpdaterSASecretName(s.Collection))
			a.SecretsToCreate[name] = s
		}

		createRequest := &secretmanagerpb.CreateSecretRequest{
			Parent:   GetProjectResourceIdNumber(a.Config.ProjectIdNumber),
			SecretId: s.Name,
			Secret: &secretmanagerpb.Secret{
				Labels:      s.Labels,
				Annotations: s.Annotations,
				Replication: &secretmanagerpb.Replication{
					Replication: &secretmanagerpb.Replication_Automatic_{
						Automatic: &secretmanagerpb.Replication_Automatic{},
					},
				},
			},
		}

		gcpSecret, err := secretsClient.CreateSecret(ctx, createRequest)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to create secret: %s", s.Name)
			continue
		}

		_, err = secretsClient.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
			Parent: gcpSecret.Name,
			Payload: &secretmanagerpb.SecretPayload{
				Data: s.Payload,
			},
		})
		if err != nil {
			logrus.WithError(err).Errorf("Failed to add version to secret: %s", gcpSecret.Name)
			continue
		}

		logrus.Debugf("Created secret: %s", s.Name)
	}
}

func (a *Actions) ApplyPolicy(ctx context.Context, client ResourceManagerClient) error {
	req := &iampb.SetIamPolicyRequest{
		Resource: GetProjectResourceIdNumber(a.Config.ProjectIdNumber),
		Policy:   a.ConsolidatedIAMPolicy,
	}
	_, err := client.SetIamPolicy(ctx, req)
	if err != nil {
		if s, ok := status.FromError(err); ok && s.Code() == codes.FailedPrecondition {
			return fmt.Errorf("IAM policy update failed due to concurrent changes: %w", err)
		}
		return fmt.Errorf("failed to apply IAM policy: %w", err)
	}

	logrus.Info("Successfully applied IAM policy")
	return nil
}

func (a *Actions) DeleteObsoleteSecrets(ctx context.Context, client SecretManagerClient) {
	for _, secret := range a.SecretsToDelete {
		err := client.DeleteSecret(ctx, &secretmanagerpb.DeleteSecretRequest{
			Name: secret.ResourceName,
		})
		if err != nil {
			logrus.WithError(err).Errorf("Failed to delete secret: %s", secret.Name)
		} else {
			logrus.Debugf("Deleted secret: %s", secret.Name)
		}
	}
}

func (a *Actions) DeleteObsoleteServiceAccounts(ctx context.Context, client IAMClient) {
	for _, sa := range a.SAsToDelete {
		request := &adminpb.DeleteServiceAccountRequest{
			Name: fmt.Sprintf("%s/serviceAccounts/%s", GetProjectResourceString(a.Config.ProjectIdString), sa.Email),
		}
		err := client.DeleteServiceAccount(ctx, request)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to delete service account: %s", sa.Email)
		} else {
			logrus.Infof("Deleted service account: %s", sa.Email)
		}
	}
}

func (a *Actions) RevokeObsoleteServiceAccountKeys(ctx context.Context, client IAMClient) {
	for _, sa := range a.SAsToDelete {
		listRequest := &adminpb.ListServiceAccountKeysRequest{
			Name: fmt.Sprintf("%s/serviceAccounts/%s", GetProjectResourceString(a.Config.ProjectIdString), sa.Email),
		}

		resp, err := client.ListServiceAccountKeys(ctx, listRequest)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to list keys for service account: %s", sa.Email)
			continue
		}

		for _, key := range resp.Keys {
			if key.KeyType == adminpb.ListServiceAccountKeysRequest_USER_MANAGED {
				deleteKeyRequest := &adminpb.DeleteServiceAccountKeyRequest{
					Name: key.Name,
				}
				err := client.DeleteServiceAccountKey(ctx, deleteKeyRequest)
				if err != nil {
					logrus.WithError(err).Errorf("Failed to revoke key for service account: %s", sa.Email)
				} else {
					logrus.Debugf("Revoked key for service account: %s", sa.Email)
				}
			}
		}
	}
}
