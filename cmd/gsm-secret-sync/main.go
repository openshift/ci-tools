package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	iamadmin "cloud.google.com/go/iam/admin/apiv1"
	resourcemanager "cloud.google.com/go/resourcemanager/apiv3"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"

	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
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

func main() {
	o := parseOptions()
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to validate options")
	}
	if err := o.setupLogger(); err != nil {
		logrus.WithError(err).Fatal("Failed to set up logging")
	}

	logrus.Info("Starting reconciliation")

	config := gsm.Production

	desiredSAs, desiredSecrets, desiredIAMBindings, desiredCollections, err := gsm.GetDesiredState(o.configFile, config)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to parse configuration file")
	}

	ctx := context.Background()

	projectsClient, err := resourcemanager.NewProjectsClient(ctx, option.WithQuotaProject(config.ProjectIdNumber))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create resource manager client")
	}
	defer projectsClient.Close()
	policy, err := gsm.GetProjectIAMPolicy(ctx, projectsClient, config.ProjectIdNumber)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get project IAM policy")
	}

	iamClient, err := iamadmin.NewIamClient(ctx, option.WithQuotaProject(config.ProjectIdNumber))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create IAM client")
	}
	actualSAs, err := gsm.GetUpdaterServiceAccounts(ctx, iamClient, config)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get service accounts")
	}

	secretsClient, err := secretmanager.NewClient(ctx, option.WithQuotaProject(config.ProjectIdNumber))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create secrets client")
	}
	defer secretsClient.Close()
	actualSecrets, err := gsm.GetAllSecrets(ctx, secretsClient, config)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get current secrets")
	}

	actions := gsm.ComputeDiff(config, desiredSAs, actualSAs, desiredSecrets, actualSecrets, desiredIAMBindings, policy, desiredCollections)

	logChangeSummary(actions)
	if !o.dryRun {
		actions.ExecuteActions(ctx, iamClient, secretsClient, projectsClient)
		logrus.Info("Reconciliation completed successfully")
	} else {
		logrus.Info("Dry run mode - no changes applied")
	}
}

func logChangeSummary(actions gsm.Actions) {
	totalChanges := len(actions.SAsToCreate) + len(actions.SAsToDelete) + len(actions.SecretsToCreate) + len(actions.SecretsToDelete)

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
