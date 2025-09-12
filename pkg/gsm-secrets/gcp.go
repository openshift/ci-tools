package gsmsecrets

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"cloud.google.com/go/iam/admin/apiv1/adminpb"
	"cloud.google.com/go/iam/apiv1/iampb"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/iterator"
)

func GetProjectIAMPolicy(ctx context.Context, client ResourceManagerClient, projectIdNumber string) (*iampb.Policy, error) {
	policyRequest := &iampb.GetIamPolicyRequest{
		Resource: GetProjectResourceIdNumber(projectIdNumber),
		Options: &iampb.GetPolicyOptions{
			RequestedPolicyVersion: 3, // necessary for IAM Conditions support
		},
	}

	policy, err := client.GetIamPolicy(ctx, policyRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to get IAM policy for project: %w", err)
	}

	return policy, nil
}

func GetUpdaterServiceAccounts(ctx context.Context, client IAMClient, config Config) ([]ServiceAccountInfo, error) {
	var actualServiceAccounts []ServiceAccountInfo

	accountIterator := client.ListServiceAccounts(ctx, &adminpb.ListServiceAccountsRequest{Name: GetProjectResourceString(config.ProjectIdString)})
	for {
		serviceAccount, err := accountIterator.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		// Only add updater service accounts
		if matched, _ := regexp.MatchString(GetUpdaterSAFormat(config), serviceAccount.Email); matched {
			collection := strings.TrimSuffix(serviceAccount.Email, config.GetUpdaterSAEmailSuffix())
			actualServiceAccounts = append(actualServiceAccounts, ServiceAccountInfo{
				Email:       serviceAccount.Email,
				DisplayName: serviceAccount.DisplayName,
				Collection:  collection,
			})
		}
	}
	return actualServiceAccounts, nil
}

// GetAllSecrets returns all secrets in the gcp project, as a map of secret IDs to secrets
func GetAllSecrets(ctx context.Context, client SecretManagerClient, config Config) (map[string]GCPSecret, error) {
	it := client.ListSecrets(ctx, &secretmanagerpb.ListSecretsRequest{
		Parent: GetProjectResourceString(config.ProjectIdString),
	})

	actualSecrets := make(map[string]GCPSecret)
	for {
		secret, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		secretID := GetSecretID(secret.Name)
		collection := ExtractCollectionFromSecretName(secretID)
		if collection == "" {
			logrus.Warnf("Couldn't extract collection from secret name: %s from secret %s", secretID, secret.Name)
			continue
		}

		actualSecrets[secretID] = GCPSecret{
			Name:         secretID,
			ResourceName: secret.Name,
			Labels:       secret.Labels,
			Annotations:  secret.Annotations,
			Type:         ClassifySecret(secretID),
			Collection:   collection,
		}
	}
	return actualSecrets, nil
}

func GetSecretPayload(ctx context.Context, client SecretManagerClient, secretResourceName string) ([]byte, error) {
	accessReq := &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretResourceName + "/versions/latest",
	}
	accessResp, err := client.AccessSecretVersion(ctx, accessReq)
	if err != nil {
		return nil, fmt.Errorf("failed to access secret version: %w", err)
	}
	return accessResp.Payload.Data, nil
}
