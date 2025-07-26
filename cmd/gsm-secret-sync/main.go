package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	iamadmin "cloud.google.com/go/iam/admin/apiv1"
	"cloud.google.com/go/iam/admin/apiv1/adminpb"
	"cloud.google.com/go/iam/apiv1/iampb"
	resourcemanager "cloud.google.com/go/resourcemanager/apiv3"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/genproto/googleapis/type/expr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/openshift/ci-tools/pkg/group"
)

type options struct {
	configFile string
	dryRun     bool
	logLevel   string
}

func parseOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.configFile, "config", "", "path to config file")
	fs.StringVar(&o.logLevel, "log-level", "info", "log level")
	fs.BoolVar(&o.dryRun, "dry-run", false, "dry run mode")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse args")
	}
	return o
}

func (o *options) Validate() error {
	if o.configFile == "" {
		return fmt.Errorf("--config is required")
	}
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid log level specified: %w", err)
	}
	logrus.SetLevel(level)
	return nil
}

func main() {
	o := parseOptions()
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to validate options")
	}
	logrus.Info("Starting reconciliation")

	desiredSAs, desiredSecrets, desiredIAMBindings, err := o.GetDesiredState()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to parse configuration file")
	}

	ctx := context.Background()

	projectsClient, err := resourcemanager.NewProjectsClient(ctx, option.WithQuotaProject(ProjectIdNumber))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create resource manager client")
	}
	defer projectsClient.Close()
	policy, err := getProjectIAMPolicy(ctx, projectsClient)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get project IAM policy")
	}

	iamClient, err := iamadmin.NewIamClient(ctx, option.WithQuotaProject(ProjectIdNumber))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create IAM client")
	}
	actualSAs, err := getUpdaterServiceAccounts(ctx, iamClient)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get service accounts")
	}

	secretsClient, err := secretmanager.NewClient(ctx, option.WithQuotaProject(ProjectIdNumber))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create secrets client")
	}
	defer secretsClient.Close()
	actualSecrets, err := getActualSecrets(ctx, secretsClient)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get current secrets")
	}

	actions := ComputeDiff(desiredSAs, actualSAs, desiredSecrets, actualSecrets, desiredIAMBindings, policy)

	logChangeSummary(actions)
	if !o.dryRun {
		actions.executeActions(ctx, iamClient, secretsClient, projectsClient)
		logrus.Info("Reconciliation completed successfully")
	} else {
		logrus.Info("Dry run mode - no changes applied")
	}
}

func logChangeSummary(actions Actions) {
	totalChanges := len(actions.SAsToCreate) + len(actions.SAsToDelete) + len(actions.SecretsToCreate) + len(actions.SecretsToDelete)

	if actions.ConsolidatedIAMPolicy != nil {
		totalChanges++
	}

	if totalChanges == 0 {
		logrus.Info("No changes required")
		return
	}

	logrus.Infof("Found (%d) changes to apply", totalChanges)

	if len(actions.SAsToCreate) > 0 {
		logrus.Infof("Creating (%d) service accounts", len(actions.SAsToCreate))
		for _, sa := range actions.SAsToCreate {
			logrus.Debugf("  + SA: %s", sa.Collection)
		}
	}

	if len(actions.SecretsToCreate) > 0 {
		logrus.Infof("Creating (%d) secrets", len(actions.SecretsToCreate))
		for _, secret := range actions.SecretsToCreate {
			logrus.Debugf("  + Secret: %s", secret.Name)
		}
	}

	if len(actions.SAsToDelete) > 0 {
		logrus.Infof("Deleting (%d) service accounts", len(actions.SAsToDelete))
		for _, sa := range actions.SAsToDelete {
			logrus.Debugf("  - SA: %s", sa.Collection)
		}
	}

	if len(actions.SecretsToDelete) > 0 {
		logrus.Infof("Deleting (%d) secrets", len(actions.SecretsToDelete))
		for _, secret := range actions.SecretsToDelete {
			logrus.Debugf("  - Secret: %s", secret.Name)
		}
	}

	if actions.ConsolidatedIAMPolicy != nil {
		logrus.Debugf("Updating IAM policy with %d bindings", len(actions.ConsolidatedIAMPolicy.Bindings))
	}
}

// GetDesiredState parses the configuration file and builds the desired state specifications.
// For each unique secret collection referenced by groups, it generates the required resource definitions.
// Returns desired service account specs, secret specs, and IAM binding specs to reconcile against actual state.
func (o *options) GetDesiredState() ([]ServiceAccountInfo, map[string]GCPSecret, []*iampb.Binding, error) {
	config, err := group.LoadConfig(o.configFile)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to load file: %w", err)
	}
	collectionsMap := make(map[string]DesiredCollection)

	for name, groupConfig := range config.Groups {
		email := fmt.Sprintf("%s@redhat.com", name)

		for _, collection := range groupConfig.SecretCollections {
			if _, found := collectionsMap[collection]; !found {
				collectionsMap[collection] = DesiredCollection{
					Name:             collection,
					GroupsWithAccess: []string{email},
				}
			} else {
				col := collectionsMap[collection]
				col.GroupsWithAccess = append(col.GroupsWithAccess, email)
				collectionsMap[collection] = col
			}
		}
	}

	var desiredSAs []ServiceAccountInfo
	desiredSecrets := make(map[string]GCPSecret)

	var desiredIAMBindings []*iampb.Binding

	for _, collection := range collectionsMap {
		desiredSAs = append(desiredSAs, ServiceAccountInfo{
			Email:       getUpdaterSAEmail(collection.Name),
			DisplayName: getUpdaterSAId(collection.Name),
			Collection:  collection.Name,
		})

		desiredSecrets[getUpdaterSASecretName(collection.Name)] = GCPSecret{
			Name: getUpdaterSASecretName(collection.Name),
			Labels: map[string]string{
				"managed-by": SecretSyncLabel,
			},
			Type:       SecretTypeSA,
			Collection: collection.Name,
		}

		desiredSecrets[getIndexSecretName(collection.Name)] = GCPSecret{
			Name: getIndexSecretName(collection.Name),
			Labels: map[string]string{
				"managed-by": SecretSyncLabel,
			},
			Type:       SecretTypeIndex,
			Collection: collection.Name,
		}

		var members []string
		for _, group := range collection.GroupsWithAccess {
			members = append(members, fmt.Sprintf("group:%s", group))
		}
		members = append(members, fmt.Sprintf("serviceAccount:%s", getUpdaterSAEmail(collection.Name)))
		sort.Strings(members)

		desiredIAMBindings = append(desiredIAMBindings, &iampb.Binding{
			Role:    SecretAccessorRole,
			Members: members,
			Condition: &expr.Expr{
				Expression:  buildSecretAccessorRoleConditionExpression(collection.Name),
				Title:       fmt.Sprintf("SA/Index access for %s", collection.Name),
				Description: fmt.Sprintf("Grants read access to specific secrets in %s", collection.Name),
			},
		})
		desiredIAMBindings = append(desiredIAMBindings, &iampb.Binding{
			Role:    SecretUpdaterRole,
			Members: members,
			Condition: &expr.Expr{
				Expression:  buildSecretUpdaterRoleConditionExpression(collection.Name),
				Title:       fmt.Sprintf("Access for %s", collection.Name),
				Description: fmt.Sprintf("Grants create, update, list, and delete access to secrets in collection: %s", collection.Name),
			},
		})
	}

	return desiredSAs, desiredSecrets, desiredIAMBindings, nil
}

// executeActions performs the actual resource changes in GCP based on the computed diff.
func (a *Actions) executeActions(ctx context.Context, iamAdminClient *iamadmin.IamClient, secretsClient *secretmanager.Client, projectsClient *resourcemanager.ProjectsClient) {
	if len(a.SAsToCreate) > 0 {
		logrus.Info("Creating service accounts")
		a.createServiceAccounts(ctx, iamAdminClient)
	}

	if len(a.SecretsToCreate) > 0 {
		logrus.Info("Creating secrets")
		a.createSecrets(ctx, secretsClient, iamAdminClient)
	}

	if a.ConsolidatedIAMPolicy != nil {
		logrus.Info("Applying IAM policy")
		if err := a.applyPolicy(ctx, projectsClient); err != nil {
			logrus.WithError(err).Fatal("Failed to apply IAM policy")
		}
	}

	if len(a.SAsToDelete) > 0 {
		logrus.Info("Revoking obsolete service account keys")
		a.revokeObsoleteServiceAccountKeys(ctx, iamAdminClient)

		logrus.Info("Deleting obsolete service accounts")
		a.deleteObsoleteServiceAccounts(ctx, iamAdminClient)
	}

	if len(a.SecretsToDelete) > 0 {
		logrus.Info("Deleting obsolete secrets")
		a.deleteObsoleteSecrets(ctx, secretsClient)
	}
}

func (a *Actions) createServiceAccounts(ctx context.Context, iamClient *iamadmin.IamClient) {
	for _, sa := range a.SAsToCreate {
		request := &adminpb.CreateServiceAccountRequest{
			Name:      getProjectResourceString(),
			AccountId: sa.DisplayName,
			ServiceAccount: &adminpb.ServiceAccount{
				DisplayName: sa.DisplayName,
			},
		}
		secretName := getUpdaterSASecretName(sa.Collection)
		newSA, err := iamClient.CreateServiceAccount(ctx, request)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to create service account: %s", sa.DisplayName)
			delete(a.SecretsToCreate, secretName)
			continue
		}

		logrus.Debugf("Generating key for service account: %s", newSA.Email)
		keyData, err := a.generateServiceAccountKey(ctx, iamClient, newSA.Email)
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

func (a *Actions) generateServiceAccountKey(ctx context.Context, iamClient *iamadmin.IamClient, saEmail string) ([]byte, error) {
	name := fmt.Sprintf("%s/serviceAccounts/%s", getProjectResourceString(), saEmail)
	key, err := iamClient.CreateServiceAccountKey(ctx, &adminpb.CreateServiceAccountKeyRequest{
		Name: name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate service account key: %w", err)
	}
	return key.GetPrivateKeyData(), nil
}

func (a *Actions) createSecrets(ctx context.Context, client *secretmanager.Client, iamAdminClient *iamadmin.IamClient) {
	for name, s := range a.SecretsToCreate {
		if s.Type == SecretTypeSA && len(s.Payload) == 0 {
			logrus.Debugf("Generating missing key for service account for collection '%s'", s.Collection)
			email := getUpdaterSAEmail(s.Collection)
			keyData, err := a.generateServiceAccountKey(ctx, iamAdminClient, email)
			if err != nil {
				logrus.WithError(err).Errorf("Failed to generate key for service account: %s", email)
				continue
			}
			s.Payload = keyData
			a.SecretsToCreate[name] = s
		}

		createRequest := &secretmanagerpb.CreateSecretRequest{
			Parent:   getProjectResourceIdNumber(),
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

		gcpSecret, err := client.CreateSecret(ctx, createRequest)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to create secret: %s", s.Name)
			continue
		}

		if len(s.Payload) > 0 {
			_, err = client.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
				Parent: gcpSecret.Name,
				Payload: &secretmanagerpb.SecretPayload{
					Data: s.Payload,
				},
			})
			if err != nil {
				logrus.WithError(err).Errorf("Failed to add version to secret: %s", gcpSecret.Name)
				continue
			}
		}

		logrus.Debugf("Created secret: %s", s.Name)
	}
}

func (a *Actions) applyPolicy(ctx context.Context, client *resourcemanager.ProjectsClient) error {
	req := &iampb.SetIamPolicyRequest{
		Resource: getProjectResourceIdNumber(),
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

func (a *Actions) deleteObsoleteSecrets(ctx context.Context, client *secretmanager.Client) {
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

func (a *Actions) deleteObsoleteServiceAccounts(ctx context.Context, client *iamadmin.IamClient) {
	for _, sa := range a.SAsToDelete {
		request := &adminpb.DeleteServiceAccountRequest{
			Name: fmt.Sprintf("%s/serviceAccounts/%s", getProjectResourceString(), sa.Email),
		}
		err := client.DeleteServiceAccount(ctx, request)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to delete service account: %s", sa.Email)
		} else {
			logrus.Infof("Deleted service account: %s", sa.Email)
		}
	}
}

func (a *Actions) revokeObsoleteServiceAccountKeys(ctx context.Context, client *iamadmin.IamClient) {
	for _, sa := range a.SAsToDelete {
		listRequest := &adminpb.ListServiceAccountKeysRequest{
			Name: fmt.Sprintf("%s/serviceAccounts/%s", getProjectResourceString(), sa.Email),
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

func (a *Actions) deleteObsoleteCollections() {
	// TODO: implement this
}

func getProjectIAMPolicy(ctx context.Context, client *resourcemanager.ProjectsClient) (*iampb.Policy, error) {
	policyRequest := &iampb.GetIamPolicyRequest{
		Resource: getProjectResourceIdNumber(),
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

func getUpdaterServiceAccounts(ctx context.Context, iamClient *iamadmin.IamClient) ([]ServiceAccountInfo, error) {
	var actualServiceAccounts []ServiceAccountInfo

	accountIterator := iamClient.ListServiceAccounts(ctx, &adminpb.ListServiceAccountsRequest{Name: getProjectResourceString()})
	for {
		serviceAccount, err := accountIterator.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		// Only add updater service accounts
		if matched, _ := regexp.MatchString(updaterSAFormat, serviceAccount.Email); matched {
			collection := strings.TrimSuffix(serviceAccount.Email, UpdaterSAEmailSuffix)
			actualServiceAccounts = append(actualServiceAccounts, ServiceAccountInfo{
				Email:       serviceAccount.Email,
				DisplayName: serviceAccount.DisplayName,
				Collection:  collection,
			})
		}
	}
	return actualServiceAccounts, nil
}

func getActualSecrets(ctx context.Context, client *secretmanager.Client) (map[string]GCPSecret, error) {
	it := client.ListSecrets(ctx, &secretmanagerpb.ListSecretsRequest{
		Parent: getProjectResourceIdNumber(),
		//only fetch secrets that are managed by this tool
		Filter: fmt.Sprintf("labels.managed-by=%s", SecretSyncLabel),
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
		secretID := strings.Split(secret.Name, "/")[len(strings.Split(secret.Name, "/"))-1] // Extract just the secret ID
		if strings.HasSuffix(secretID, updaterSASecretSuffix) || strings.HasSuffix(secretID, indexSecretSuffix) {
			actualSecrets[secretID] = GCPSecret{
				Name:         secretID,
				ResourceName: secret.Name,
				Labels:       secret.Labels,
				Annotations:  secret.Annotations,
				Type:         classifySecret(secretID),
				Collection:   extractCollectionFromSecretName(secretID),
			}
		}
	}
	return actualSecrets, nil
}
