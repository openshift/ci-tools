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
	"time"

	iamadmin "cloud.google.com/go/iam/admin/apiv1"
	"cloud.google.com/go/iam/admin/apiv1/adminpb"
	"cloud.google.com/go/iam/apiv1/iampb"
	resourcemanager "cloud.google.com/go/resourcemanager/apiv3"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/googleapis/gax-go/v2"
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

	return nil
}

func (o *options) setupLogger() error {
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid log level specified: %w", err)
	}
	logrus.SetLevel(level)
	formatter := new(logrus.TextFormatter)
	formatter.TimestampFormat = time.RFC3339
	formatter.FullTimestamp = true
	formatter.ForceColors = true
	logrus.SetFormatter(formatter)
	return nil
}

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

func main() {
	o := parseOptions()
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to validate options")
	}
	if err := o.setupLogger(); err != nil {
		logrus.WithError(err).Fatal("Failed to set up logging")
	}

	logrus.Info("Starting reconciliation")

	desiredSAs, desiredSecrets, desiredIAMBindings, desiredCollections, err := o.GetDesiredState()
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

	actions := ComputeDiff(desiredSAs, actualSAs, desiredSecrets, actualSecrets, desiredIAMBindings, policy, desiredCollections)

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
			logrus.Debugf("  - %s", secret.Name)
		}
	}

	if actions.ConsolidatedIAMPolicy != nil {
		logrus.Debugf("Updating IAM policy with %d bindings", len(actions.ConsolidatedIAMPolicy.Bindings))
		for _, binding := range actions.ConsolidatedIAMPolicy.Bindings {
			logrus.Debugf("  + Role: %s, Members: %s", binding.Role, binding.Members)
		}
	}
}

// GetDesiredState parses the configuration file and builds the desired state specifications.
// For each unique secret collection referenced by groups, it generates the required resource definitions.
// Returns desired service account specs, secret specs, IAM binding specs, and the set of active collections.
func (o *options) GetDesiredState() ([]ServiceAccountInfo, map[string]GCPSecret, []*iampb.Binding, map[string]bool, error) {
	config, err := group.LoadConfig(o.configFile)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to load file: %w", err)
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
	desiredCollections := make(map[string]bool)
	var desiredIAMBindings []*iampb.Binding

	var collectionNames []string
	for name := range collectionsMap {
		collectionNames = append(collectionNames, name)
	}
	sort.Strings(collectionNames)

	for _, collectionName := range collectionNames {
		collection := collectionsMap[collectionName]
		desiredCollections[collection.Name] = true

		desiredSAs = append(desiredSAs, ServiceAccountInfo{
			Email:       getUpdaterSAEmail(collection.Name),
			DisplayName: getUpdaterSAId(collection.Name),
			Collection:  collection.Name,
		})

		desiredSecrets[getUpdaterSASecretName(collection.Name)] = GCPSecret{
			Name:       getUpdaterSASecretName(collection.Name),
			Type:       SecretTypeSA,
			Collection: collection.Name,
		}

		desiredSecrets[getIndexSecretName(collection.Name)] = GCPSecret{
			Name:       getIndexSecretName(collection.Name),
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
				Title:       getSecretsViewerConditionTitle(collection.Name),
				Description: getSecretsViewerConditionDescription(collection.Name),
			},
		})
		desiredIAMBindings = append(desiredIAMBindings, &iampb.Binding{
			Role:    SecretUpdaterRole,
			Members: members,
			Condition: &expr.Expr{
				Expression:  buildSecretUpdaterRoleConditionExpression(collection.Name),
				Title:       getSecretsUpdaterConditionTitle(collection.Name),
				Description: getSecretsUpdaterConditionDescription(collection.Name),
			},
		})
	}

	return desiredSAs, desiredSecrets, desiredIAMBindings, desiredCollections, nil
}

// executeActions performs the actual resource changes in GCP based on the computed diff.
func (a *Actions) executeActions(ctx context.Context, iamClient IAMClient, secretsClient SecretManagerClient, projectsClient ResourceManagerClient) {
	if len(a.SAsToCreate) > 0 {
		logrus.Info("Creating service accounts")
		a.createServiceAccounts(ctx, iamClient)
	}

	if len(a.SecretsToCreate) > 0 {
		logrus.Info("Creating secrets")
		a.createSecrets(ctx, secretsClient, iamClient)
	}

	if a.ConsolidatedIAMPolicy != nil {
		logrus.Info("Applying IAM policy")
		if err := a.applyPolicy(ctx, projectsClient); err != nil {
			logrus.WithError(err).Fatal("Failed to apply IAM policy")
		}
	}

	if len(a.SAsToDelete) > 0 {
		logrus.Info("Revoking obsolete service account keys")
		a.revokeObsoleteServiceAccountKeys(ctx, iamClient)

		logrus.Info("Deleting obsolete service accounts")
		a.deleteObsoleteServiceAccounts(ctx, iamClient)
	}

	if len(a.SecretsToDelete) > 0 {
		logrus.Info("Deleting obsolete secrets")
		a.deleteObsoleteSecrets(ctx, secretsClient)
	}
}

func (a *Actions) createServiceAccounts(ctx context.Context, client IAMClient) {
	for _, sa := range a.SAsToCreate {
		request := &adminpb.CreateServiceAccountRequest{
			Name:      getProjectResourceString(),
			AccountId: sa.DisplayName,
			ServiceAccount: &adminpb.ServiceAccount{
				DisplayName: sa.DisplayName,
			},
		}
		secretName := getUpdaterSASecretName(sa.Collection)
		newSA, err := client.CreateServiceAccount(ctx, request)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to create service account: %s", sa.DisplayName)
			delete(a.SecretsToCreate, secretName)
			continue
		}
		logrus.Debugf("Service account created: %s", newSA.Email)
		logrus.Debugf("Generating key for service account: %s", newSA.Email)
		keyData, err := generateServiceAccountKey(ctx, client, newSA.Email)
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

func generateServiceAccountKey(ctx context.Context, client IAMClient, saEmail string) ([]byte, error) {
	name := fmt.Sprintf("%s/serviceAccounts/%s", getProjectResourceString(), saEmail)
	key, err := client.CreateServiceAccountKey(ctx, &adminpb.CreateServiceAccountKeyRequest{
		Name: name,
	})
	if err != nil {
		return nil, err
	}
	return key.GetPrivateKeyData(), nil
}

func (a *Actions) createSecrets(ctx context.Context, secretsClient SecretManagerClient, iamClient IAMClient) {
	for name, s := range a.SecretsToCreate {
		if s.Type == SecretTypeSA && len(s.Payload) == 0 {
			logrus.Debugf("Generating missing key for service account for collection '%s'", s.Collection)
			email := getUpdaterSAEmail(s.Collection)
			keyData, err := generateServiceAccountKey(ctx, iamClient, email)
			if err != nil {
				logrus.WithError(err).Errorf("Failed to generate key for service account: %s", email)
				continue
			}
			s.Payload = keyData
			a.SecretsToCreate[name] = s
		}

		if s.Type == SecretTypeIndex {
			s.Payload = fmt.Appendf(nil, "- %s", getUpdaterSASecretName(s.Collection))
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

func (a *Actions) applyPolicy(ctx context.Context, client ResourceManagerClient) error {
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

func (a *Actions) deleteObsoleteSecrets(ctx context.Context, client SecretManagerClient) {
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

func (a *Actions) deleteObsoleteServiceAccounts(ctx context.Context, client IAMClient) {
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

func (a *Actions) revokeObsoleteServiceAccountKeys(ctx context.Context, client IAMClient) {
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

func getProjectIAMPolicy(ctx context.Context, client ResourceManagerClient) (*iampb.Policy, error) {
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

func getUpdaterServiceAccounts(ctx context.Context, client IAMClient) ([]ServiceAccountInfo, error) {
	var actualServiceAccounts []ServiceAccountInfo

	accountIterator := client.ListServiceAccounts(ctx, &adminpb.ListServiceAccountsRequest{Name: getProjectResourceString()})
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

func getActualSecrets(ctx context.Context, client SecretManagerClient) (map[string]GCPSecret, error) {
	it := client.ListSecrets(ctx, &secretmanagerpb.ListSecretsRequest{
		Parent: getProjectResourceIdNumber(),
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

		if extractCollectionFromSecretName(secretID) == "" {
			logrus.Warnf("Couldn't extract collection from secret name: %s from secret %s", secretID, secret.Name)
			continue
		}

		actualSecrets[secretID] = GCPSecret{
			Name:         secretID,
			ResourceName: secret.Name,
			Labels:       secret.Labels,
			Annotations:  secret.Annotations,
			Type:         classifySecret(secretID),
			Collection:   extractCollectionFromSecretName(secretID),
		}
	}
	return actualSecrets, nil
}

// extractCollectionFromSecretName returns the substring before the first "__" in a secret name.
func extractCollectionFromSecretName(secretName string) string {
	if strings.HasSuffix(secretName, indexSecretSuffix) {
		collection := strings.TrimSuffix(secretName, indexSecretSuffix)
		if collection != "" && validateCollectionName(collection) {
			return collection
		}
		return ""
	}

	parts := strings.Split(secretName, "__")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		if !validateCollectionName(parts[0]) {
			return ""
		}
		if !validateSecretName(parts[1]) {
			return ""
		}
		return parts[0]
	}

	return ""
}

func validateCollectionName(collection string) bool {
	return regexp.MustCompile(colectionRegex).MatchString(collection)
}

func validateSecretName(secretName string) bool {
	return regexp.MustCompile(secretNameRegex).MatchString(secretName)
}
