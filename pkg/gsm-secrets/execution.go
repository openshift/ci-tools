package gsmsecrets

import (
	"context"
	"fmt"
	"net/http"
	"time"

	iamadmin "cloud.google.com/go/iam/admin/apiv1"
	"cloud.google.com/go/iam/admin/apiv1/adminpb"
	"cloud.google.com/go/iam/apiv1/iampb"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/googleapis/gax-go/v2"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

// Client interfaces - these should be defined by the cmd package
type SecretManagerClient interface {
	ListSecrets(ctx context.Context, req *secretmanagerpb.ListSecretsRequest, opts ...gax.CallOption) *secretmanager.SecretIterator
	DeleteSecret(ctx context.Context, req *secretmanagerpb.DeleteSecretRequest, opts ...gax.CallOption) error
	CreateSecret(ctx context.Context, req *secretmanagerpb.CreateSecretRequest, opts ...gax.CallOption) (*secretmanagerpb.Secret, error)
	AddSecretVersion(ctx context.Context, req *secretmanagerpb.AddSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.SecretVersion, error)
	AccessSecretVersion(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error)
}

type ResourceManagerClient interface {
	SetIamPolicy(ctx context.Context, req *iampb.SetIamPolicyRequest, opts ...gax.CallOption) (*iampb.Policy, error)
	GetIamPolicy(ctx context.Context, req *iampb.GetIamPolicyRequest, opts ...gax.CallOption) (*iampb.Policy, error)
}

type IAMClient interface {
	CreateServiceAccountKey(ctx context.Context, req *adminpb.CreateServiceAccountKeyRequest, opts ...gax.CallOption) (*adminpb.ServiceAccountKey, error)
	CreateServiceAccount(ctx context.Context, req *adminpb.CreateServiceAccountRequest, opts ...gax.CallOption) (*adminpb.ServiceAccount, error)
	DeleteServiceAccount(ctx context.Context, req *adminpb.DeleteServiceAccountRequest, opts ...gax.CallOption) error
	GetServiceAccount(ctx context.Context, req *adminpb.GetServiceAccountRequest, opts ...gax.CallOption) (*adminpb.ServiceAccount, error)
	ListServiceAccounts(ctx context.Context, req *adminpb.ListServiceAccountsRequest, opts ...gax.CallOption) *iamadmin.ServiceAccountIterator
	ListServiceAccountKeys(ctx context.Context, req *adminpb.ListServiceAccountKeysRequest, opts ...gax.CallOption) (*adminpb.ListServiceAccountKeysResponse, error)
	DeleteServiceAccountKey(ctx context.Context, req *adminpb.DeleteServiceAccountKeyRequest, opts ...gax.CallOption) error
}

// ExecuteActions performs the actual resource changes in GCP based on the computed diff.
func (a *Actions) ExecuteActions(ctx context.Context, iamClient IAMClient, secretsClient SecretManagerClient, projectsClient ResourceManagerClient) {
	if len(a.SAsToCreate) > 0 {
		a.CreateServiceAccounts(ctx, iamClient)
	}

	if len(a.SecretsToCreate) > 0 {
		a.CreateSecrets(ctx, secretsClient, iamClient)
	}

	if a.ConsolidatedIAMPolicy != nil {
		if err := a.ApplyPolicy(ctx, projectsClient); err != nil {
			logrus.WithError(err).Fatal("Failed to apply IAM policy")
		}
	}

	if len(a.SAsToDelete) > 0 {
		a.RevokeObsoleteServiceAccountKeys(ctx, iamClient)
		a.DeleteObsoleteServiceAccounts(ctx, iamClient)
	}

	if len(a.SecretsToDelete) > 0 {
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
		logrus.Debugf("Service account created for collection %s", sa.Collection)
		keyData, err := GenerateServiceAccountKey(ctx, client, newSA.Email, a.Config.ProjectIdString)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to generate key for service account: %s", newSA.Email)
			delete(a.SecretsToCreate, secretName)
			continue
		}

		secret := a.SecretsToCreate[secretName]
		secret.Payload = keyData
		a.SecretsToCreate[secretName] = secret
	}
}

// gcpServiceAccountBackoff defines retry behavior for GCP service account operations
var gcpServiceAccountBackoff = wait.Backoff{
	Steps:    3,
	Duration: 8 * time.Second,
	Factor:   2.0,
	Jitter:   0.1,
	Cap:      30 * time.Second,
}

// isServiceAccountNotFoundError detects GCP service account "not found" errors indicating eventual consistency
func isServiceAccountNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	if gcpError, ok := err.(*googleapi.Error); ok {
		return gcpError.Code == http.StatusNotFound
	}

	if s, ok := status.FromError(err); ok {
		return s.Code() == codes.NotFound
	}

	return false
}

func GenerateServiceAccountKey(ctx context.Context, client IAMClient, saEmail string, projectID string) ([]byte, error) {
	return generateServiceAccountKeyWithBackoff(ctx, client, saEmail, projectID, gcpServiceAccountBackoff)
}

func generateServiceAccountKeyWithBackoff(ctx context.Context, client IAMClient, saEmail string, projectID string, backoff wait.Backoff) ([]byte, error) {
	name := fmt.Sprintf("%s/serviceAccounts/%s", GetProjectResourceString(projectID), saEmail)

	key, err := client.CreateServiceAccountKey(ctx, &adminpb.CreateServiceAccountKeyRequest{
		Name: name,
	})

	if err != nil && isServiceAccountNotFoundError(err) {
		// The reason for the service account not found may be due to eventual consistency, so we wait for it to become available
		logrus.Warnf("Service account %s not available, waiting for eventual consistency...", saEmail)

		attemptCount := 0
		retryErr := retry.OnError(backoff, isServiceAccountNotFoundError, func() error {
			attemptCount++
			logrus.WithField("service account", saEmail).Debugf("Checking availability (attempt #%d)...", attemptCount)

			_, err := client.GetServiceAccount(ctx, &adminpb.GetServiceAccountRequest{
				Name: name,
			})

			if err != nil {
				if isServiceAccountNotFoundError(err) {
					logrus.WithField("service account", saEmail).Warnf("Still not available (attempt #%d), retrying...", attemptCount)
				} else {
					logrus.WithField("service account", saEmail).Errorf("Non-retryable error while checking (attempt #%d): %v", attemptCount, err)
				}
			}
			return err
		})

		if retryErr != nil {
			return nil, fmt.Errorf("service account %s never became available after %d attempts: %w", saEmail, attemptCount, retryErr)
		}
		key, err = client.CreateServiceAccountKey(ctx, &adminpb.CreateServiceAccountKeyRequest{
			Name: name,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create service account key evenafter waiting for availability: %w", err)
		}
		logrus.WithField("service account", saEmail).Debugf("Successfully generated key after waiting for eventual consistency (attempts: %d)", attemptCount)
	} else if err != nil {
		return nil, fmt.Errorf("failed to create service account key: %w", err)
	} else {
		logrus.WithField("service account", saEmail).Debugf("Successfully generated key on first attempt")
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
			s.Payload = fmt.Appendf(nil, "- updater-service-account")
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

	logrus.Debug("Successfully applied IAM policy")
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
			logrus.Debugf("Deleted service account: %s", sa.Email)
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
