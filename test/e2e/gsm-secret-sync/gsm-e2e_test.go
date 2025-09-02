//go:build gsm_e2e
// +build gsm_e2e

package gsm_e2e

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	iamadmin "cloud.google.com/go/iam/admin/apiv1"
	"cloud.google.com/go/iam/admin/apiv1/adminpb"
	"cloud.google.com/go/iam/apiv1/iampb"
	resourcemanager "cloud.google.com/go/resourcemanager/apiv3"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/prow/pkg/logrusutil"

	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
	"github.com/openshift/ci-tools/pkg/secrets"
)

type GCPState struct {
	Secrets         map[string]gsm.GCPSecret
	IAMBindings     []*iampb.Binding
	ServiceAccounts []gsm.ServiceAccountInfo
}

type testRunner struct {
	ctx                   context.Context
	config                gsm.Config
	binaryPath            string
	secretsClient         *secretmanager.Client
	iamAdminClient        *iamadmin.IamClient
	resourceManagerClient *resourcemanager.ProjectsClient
}

var gcpStateCheckBackoff = wait.Backoff{
	Steps:    3,
	Duration: 10 * time.Second, // Start with 10s (give GCP time to propagate)
	Factor:   2.0,
	Jitter:   0.1,
	Cap:      60 * time.Second,
}

var (
	tr       *testRunner
	logLevel = flag.String("log-level", "info", "log level")
)

func getProjectConfigFromEnv() gsm.Config {
	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		logrus.Fatal("GCP_PROJECT_ID not set")
	}

	projectNumber := os.Getenv("GCP_PROJECT_NUMBER")
	if projectNumber == "" {
		logrus.Fatal("GCP_PROJECT_NUMBER not set")
	}

	return gsm.Config{
		ProjectIdString: projectID,
		ProjectIdNumber: projectNumber,
	}
}

func setupLogger(censor *secrets.DynamicCensor) error {
	level, err := logrus.ParseLevel(*logLevel)
	if err != nil {
		return fmt.Errorf("invalid log level specified: %w", err)
	}
	logrus.SetLevel(level)
	logrus.SetFormatter(logrusutil.NewFormatterWithCensor(&logrus.JSONFormatter{}, censor))
	return nil
}

func (tr *testRunner) cleanup() {
	ctx := context.Background()
	currentState := tr.getActualGCPState()

	if len(currentState.Secrets) > 0 {
		logrus.Debugf("Deleting %d secrets...", len(currentState.Secrets))
		for _, secret := range currentState.Secrets {
			err := tr.secretsClient.DeleteSecret(ctx, &secretmanagerpb.DeleteSecretRequest{
				Name: secret.ResourceName,
			})
			if err != nil {
				// Ignore "NotFound" errors - they indicate eventual consistency
				// (the secret is already deleted, it just takes time for the list API to reflect that)
				s, ok := status.FromError(err)
				if ok && (s.Code() == codes.NotFound) {
					logrus.Debugf("Secret %s already deleted", secret.Name)
				} else {
					logrus.Errorf("Failed to delete secret %s: %v", secret.Name, err)
				}
			}
		}
	}

	if len(currentState.ServiceAccounts) > 0 {
		logrus.Debugf("Deleting %d service accounts...", len(currentState.ServiceAccounts))
		for _, sa := range currentState.ServiceAccounts {
			err := tr.iamAdminClient.DeleteServiceAccount(ctx, &adminpb.DeleteServiceAccountRequest{
				Name: fmt.Sprintf("%s/serviceAccounts/%s", gsm.GetProjectResourceString(tr.config.ProjectIdString), sa.Email),
			})
			if err != nil {
				s, ok := status.FromError(err)
				if ok && (s.Code() == codes.NotFound) {
					logrus.Debugf("Service account %s already deleted", sa.Email)
				} else {
					logrus.Errorf("Failed to delete service account %s: %v", sa.Email, err)
				}
			}
		}
	}

	// Clean up IAM bindings by applying a policy that only contains bindings that are not managed by the reconciler
	if len(currentState.IAMBindings) > 0 {
		logrus.Debugf("Removing %d managed IAM bindings...", len(currentState.IAMBindings))

		policy, err := gsm.GetProjectIAMPolicy(ctx, tr.resourceManagerClient, tr.config.ProjectIdNumber)
		if err != nil {
			logrus.Errorf("Failed to get IAM policy for cleanup: %v", err)
			return
		}

		var unmanagedBindings []*iampb.Binding
		for _, binding := range policy.Bindings {
			if !gsm.IsManagedBinding(binding) {
				unmanagedBindings = append(unmanagedBindings, binding)
			}
		}
		unmanagedPolicy := &iampb.Policy{
			Bindings:     unmanagedBindings,
			Etag:         policy.Etag,
			Version:      3,
			AuditConfigs: policy.AuditConfigs,
		}

		_, err = tr.resourceManagerClient.SetIamPolicy(ctx, &iampb.SetIamPolicyRequest{
			Resource: gsm.GetProjectResourceIdNumber(tr.config.ProjectIdNumber),
			Policy:   unmanagedPolicy,
		})
		if err != nil {
			logrus.Errorf("Failed to clean IAM policy: %v", err)
		}
	}
}

func (tr *testRunner) verifyProjectIsClean() bool {
	logrus.Info("Verifying project contains no leftover resources")

	emptyExpectedState := GCPState{
		Secrets:         make(map[string]gsm.GCPSecret),
		ServiceAccounts: []gsm.ServiceAccountInfo{},
		IAMBindings:     []*iampb.Binding{},
	}

	currentState := tr.getActualGCPStateWithRetry(emptyExpectedState)

	totalResources := len(currentState.Secrets) + len(currentState.ServiceAccounts) + len(currentState.IAMBindings)

	if totalResources > 0 {
		logrus.Errorf("Project is not clean: found %d secrets, %d service accounts, %d IAM bindings",
			len(currentState.Secrets), len(currentState.ServiceAccounts), len(currentState.IAMBindings))
		return false
	}
	return true
}

func TestMain(m *testing.M) {
	flag.Parse()

	censor := secrets.NewDynamicCensor()
	if err := setupLogger(&censor); err != nil {
		logrus.WithError(err).Fatal("Failed to setup logger")
	}

	credFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if credFile == "" {
		logrus.Fatal("Missing GOOGLE_APPLICATION_CREDENTIALS")
	}
	gcpCredentials, err := secrets.ReadFromFile(credFile, &censor)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to read GCP credentials")
	}
	gcpCreds := []byte(gcpCredentials)

	config := getProjectConfigFromEnv()
	logrus.Infof("config: %+v", config)
	ctx := context.Background()

	secretsClient, err := secretmanager.NewClient(ctx, option.WithQuotaProject(config.ProjectIdNumber), option.WithCredentialsJSON(gcpCreds))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create secrets client")
	}
	defer secretsClient.Close()

	iamAdminClient, err := iamadmin.NewIamClient(ctx, option.WithQuotaProject(config.ProjectIdNumber), option.WithCredentialsJSON(gcpCreds))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create IAM client")
	}
	defer iamAdminClient.Close()

	resourceManagerClient, err := resourcemanager.NewProjectsClient(ctx, option.WithQuotaProject(config.ProjectIdNumber), option.WithCredentialsJSON(gcpCreds))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create resource manager client")
	}
	defer resourceManagerClient.Close()

	tr = &testRunner{
		ctx:                   ctx,
		config:                config,
		binaryPath:            "/go/bin/gsm-secret-sync",
		secretsClient:         secretsClient,
		iamAdminClient:        iamAdminClient,
		resourceManagerClient: resourceManagerClient,
	}

	tr.cleanup()
	if !tr.verifyProjectIsClean() {
		logrus.Error("gcp project is not clean; skipping tests")
		tr.cleanup()
		os.Exit(1)
	}

	code := m.Run()

	tr.cleanup()
	os.Exit(code)
}

func TestInitialCreate(t *testing.T) {
	configPath := "testdata/config-create.yaml"
	expectedState, err := getExpectedState(configPath, tr.config)
	if err != nil {
		t.Fatalf("failed to get expected state from config %s: %v", configPath, err)
	}

	err = tr.runReconcilerTool(configPath)
	if err != nil {
		t.Fatalf("failed to run reconciler tool: %v", err)
	}
	actualState := tr.getActualGCPStateWithRetry(expectedState)

	if !tr.compareStates(actualState, expectedState) {
		t.Fatal("initial create test failed")
	}
}

func TestIdempotency(t *testing.T) {
	configPath := "testdata/config-create.yaml"
	expectedState, err := getExpectedState(configPath, tr.config)
	if err != nil {
		t.Fatalf("failed to get expected state from config %s: %v", configPath, err)
	}

	err = tr.runReconcilerTool(configPath)
	if err != nil {
		t.Fatalf("failed to run reconciler tool (idempotency test #1): %v", err)
	}

	beforeState := tr.getActualGCPStateWithRetry(expectedState)
	err = tr.runReconcilerTool(configPath)
	if err != nil {
		t.Fatalf("failed to run reconciler tool (idempotency test #2): %v", err)
	}
	afterState := tr.getActualGCPStateWithRetry(expectedState)

	if !tr.compareStates(afterState, beforeState) {
		t.Fatal("idempotency test failed")
	}
}

func TestUpdate(t *testing.T) {
	configPath := "testdata/config-update.yaml"
	expectedState, err := getExpectedState(configPath, tr.config)
	if err != nil {
		t.Fatalf("failed to get expected state from config %s: %v", configPath, err)
	}
	err = tr.runReconcilerTool(configPath)
	if err != nil {
		t.Fatalf("failed to run reconciler tool: %v", err)
	}
	actualState := tr.getActualGCPStateWithRetry(expectedState)

	if !tr.compareStates(actualState, expectedState) {
		t.Fatal("update test failed")
	}
}

func TestDeletion(t *testing.T) {
	configPath := "testdata/config-delete.yaml"
	expectedState, err := getExpectedState(configPath, tr.config)
	if err != nil {
		t.Fatalf("failed to get expected state from config %s: %v", configPath, err)
	}
	err = tr.runReconcilerTool(configPath)
	if err != nil {
		t.Fatalf("failed to run reconciler tool: %v", err)
	}
	actualState := tr.getActualGCPStateWithRetry(expectedState)

	if !tr.compareStates(actualState, expectedState) {
		t.Fatal("deletion test failed")
	}
}

// runReconcilerTool runs the reconciler tool's binary with the given config path
func (tr *testRunner) runReconcilerTool(configPath string) error {
	credFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	cmd := exec.Command(tr.binaryPath,
		"--config", configPath,
		"--log-level", "info",
		"--gcp-service-account-key-file", credFile)

	cmd.Env = append(os.Environ(),
		fmt.Sprintf("GCP_PROJECT_ID=%s", tr.config.ProjectIdString),
		fmt.Sprintf("GCP_PROJECT_NUMBER=%s", tr.config.ProjectIdNumber),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("google secret manager sync failed: %w\nOutput:\n%s", err, string(output))
	}
	logrus.Info("Running reconciler tool")
	if *logLevel == "debug" {
		fmt.Print(string(output))
	}
	return nil
}

// getActualGCPState returns the current state of resources in the GCP project
func (tr *testRunner) getActualGCPState() GCPState {
	secrets, err := gsm.GetAllSecrets(tr.ctx, tr.secretsClient, tr.config)
	if err != nil {
		logrus.Errorf("Error while fetching secrets: %v", err)
		secrets = make(map[string]gsm.GCPSecret)
	}

	serviceAccounts, err := gsm.GetUpdaterServiceAccounts(tr.ctx, tr.iamAdminClient, tr.config)
	if err != nil {
		logrus.Errorf("Failed to fetch service accounts: %v", err)
		serviceAccounts = []gsm.ServiceAccountInfo{}
	}

	var iamBindings []*iampb.Binding
	policy, err := gsm.GetProjectIAMPolicy(tr.ctx, tr.resourceManagerClient, tr.config.ProjectIdNumber)
	if err != nil {
		logrus.Errorf("Failed to fetch IAM policy: %v", err)
	} else {
		for _, binding := range policy.Bindings {
			if gsm.IsManagedBinding(binding) {
				iamBindings = append(iamBindings, binding)
			}
		}
	}

	return GCPState{
		Secrets:         secrets,
		IAMBindings:     iamBindings,
		ServiceAccounts: serviceAccounts,
	}
}

// getActualGCPStateWithRetry gets the actual state with retry for eventual consistency
func (tr *testRunner) getActualGCPStateWithRetry(expectedState GCPState) GCPState {
	var state GCPState

	expectedSACount := len(expectedState.ServiceAccounts)
	expectedSecretCount := len(expectedState.Secrets)

	// Retry until both service accounts and secrets match expectations
	// (GCP sometimes takes a while to propagate changes, resulting in false negatives)
	err := retry.OnError(gcpStateCheckBackoff, func(err error) bool {
		return true
	}, func() error {
		state = tr.getActualGCPState()
		actualSACount := len(state.ServiceAccounts)
		actualSecretCount := len(state.Secrets)

		if actualSACount != expectedSACount {
			logrus.Debugf("Expected %d service accounts, got %d, retrying...",
				expectedSACount, actualSACount)
			return fmt.Errorf("service account count mismatch: expected %d, got %d", expectedSACount, actualSACount)
		}

		if actualSecretCount != expectedSecretCount {
			logrus.Debugf("Expected %d secrets, got %d, retrying...",
				expectedSecretCount, actualSecretCount)
			return fmt.Errorf("secret count mismatch: expected %d, got %d", expectedSecretCount, actualSecretCount)
		}
		return nil
	})

	if err != nil {
		logrus.Errorf("Failures while waiting for GCP state to match expectations: %v", err)
	}

	return state
}

// getExpectedState reads a test config file and derives the expected state
func getExpectedState(configPath string, config gsm.Config) (GCPState, error) {
	desiredSAs, desiredSecrets, desiredIAMBindings, _, err := gsm.GetDesiredState(configPath, config)
	if err != nil {
		return GCPState{}, fmt.Errorf("failed to get desired state from config %s: %w", configPath, err)
	}

	desiredState := GCPState{
		Secrets:         desiredSecrets,
		IAMBindings:     desiredIAMBindings,
		ServiceAccounts: desiredSAs,
	}
	return desiredState, nil
}

func (tr *testRunner) compareStates(actualState GCPState, expectedState GCPState) bool {
	if len(expectedState.Secrets) != len(actualState.Secrets) {
		logrus.Errorf("Secret count mismatch: expected %d, got %d", len(expectedState.Secrets), len(actualState.Secrets))
		return false
	}

	for secretName, expectedSecret := range expectedState.Secrets {
		actualSecret, exists := actualState.Secrets[secretName]
		if !exists {
			logrus.Errorf("Expected secret %s not found in actual state", secretName)
			return false
		}
		if expectedSecret.Type != actualSecret.Type {
			logrus.Errorf("Secret %s type mismatch: expected %v, got %v", secretName, expectedSecret.Type, actualSecret.Type)
			return false
		}
		if actualSecret.Type == gsm.SecretTypeIndex {
			payload, err := gsm.GetSecretPayload(tr.ctx, tr.secretsClient, actualSecret.ResourceName)
			if err != nil {
				logrus.Errorf("Failed to fetch payload for index secret %s: %v", secretName, err)
				return false
			}
			err = gsm.VerifyIndexSecretContent(payload)
			if err != nil {
				logrus.Errorf("Failed to verify index secret content: %v", err)
				return false
			}
		}
		if expectedSecret.Collection != actualSecret.Collection {
			logrus.Errorf("Secret %s collection mismatch: expected %s, got %s", secretName, expectedSecret.Collection, actualSecret.Collection)
			return false
		}
	}

	if len(expectedState.ServiceAccounts) != len(actualState.ServiceAccounts) {
		logrus.Errorf("Service account count mismatch: expected %d, got %d", len(expectedState.ServiceAccounts), len(actualState.ServiceAccounts))
		return false
	}
	for _, expectedSA := range expectedState.ServiceAccounts {
		if !slices.ContainsFunc(actualState.ServiceAccounts, func(sa gsm.ServiceAccountInfo) bool {
			return sa.Email == expectedSA.Email &&
				sa.DisplayName == expectedSA.DisplayName &&
				sa.Collection == expectedSA.Collection
		}) {
			logrus.Errorf("Expected service account %s not found in actual state", expectedSA.Email)
			return false
		}
	}

	if len(expectedState.IAMBindings) != len(actualState.IAMBindings) {
		logrus.Errorf("IAM binding count mismatch: expected %d, got %d", len(expectedState.IAMBindings), len(actualState.IAMBindings))
		return false
	}
	for _, expectedBinding := range expectedState.IAMBindings {
		if !slices.ContainsFunc(actualState.IAMBindings, func(binding *iampb.Binding) bool {
			return gsm.ToCanonicalIAMBinding(binding) == gsm.ToCanonicalIAMBinding(expectedBinding)
		}) {
			logrus.Errorf("Expected IAM binding %s not found in actual state", expectedBinding.Role)
			return false
		}
	}

	logrus.Info("Actual secret manager state matches expected state")
	return true
}
