package main

import (
	"context"
	"flag"
	"os"
	"time"

	"github.com/bombsimon/logrusr/v3"
	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	ctrlruntimelog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type options struct {
	client           prowflagutil.KubernetesOptions
	github           prowflagutil.GitHubOptions
	githubEnablement prowflagutil.GitHubEnablementOptions
	config           configflagutil.ConfigOptions
	dryrun           bool
}

func (o *options) validate() error {
	for _, opt := range []interface{ Validate(bool) error }{&o.client, &o.githubEnablement, &o.config} {
		if err := opt.Validate(o.dryrun); err != nil {
			return err
		}
	}

	return nil
}

func (o *options) parseArgs(fs *flag.FlagSet, args []string) error {
	fs.BoolVar(&o.dryrun, "dry-run", false, "Run in dry-run mode")

	o.config.AddFlags(fs)
	o.github.AddFlags(fs)
	o.client.AddFlags(fs)
	o.githubEnablement.AddFlags(fs)

	if err := fs.Parse(args); err != nil {
		logrus.WithError(err).Fatal("Could not parse args.")
	}

	return o.validate()
}

func parseOptions() options {
	var o options

	if err := o.parseArgs(flag.CommandLine, os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("invalid flag options")
	}

	return o
}

func main() {
	logrusutil.ComponentInit()
	logger := logrus.WithField("component", "pipeline-controller")
	ctrlruntimelog.SetLogger(logrusr.New(logger))

	o := parseOptions()

	configAgent, err := o.config.ConfigAgent()
	if err != nil {
		logger.WithError(err).Fatal("error starting config agent")
	}
	cfg := configAgent.Config

	restCfg, err := o.client.InfrastructureClusterConfig(o.dryrun)
	if err != nil {
		logger.WithError(err).Fatal("failed to get kubeconfig")
	}
	mgr, err := manager.New(restCfg, manager.Options{
		Namespace:          cfg().ProwJobNamespace,
		MetricsBindAddress: "0",
	})
	if err != nil {
		logger.WithError(err).Fatal("failed to create manager")
	}

	if err := o.client.AddKubeconfigChangeCallback(func() {
		logger.Info("kubeconfig changed, exiting to trigger a restart")
		interrupts.Terminate()
	}); err != nil {
		logger.WithError(err).Fatal("failed to register kubeconfig callback")
	}

	if o.github.TokenPath != "" {
		if err := secret.Add(o.github.TokenPath); err != nil {
			logger.WithError(err).Fatal("error reading GitHub credentials")
		}
	}

	githubClient, err := o.github.GitHubClient(o.dryrun)
	if err != nil {
		logger.WithError(err).Fatal("error getting GitHub client")
	}

	configDataProvider := NewConfigDataProvider(cfg)
	go configDataProvider.Run()

	reconciler, err := NewReconciler(mgr, configDataProvider, githubClient, logger)
	if err != nil {
		logger.WithError(err).Fatal("failed to construct github reporter controller")
	}
	go reconciler.cleanOldIds(24 * time.Hour)

	interrupts.Run(func(ctx context.Context) {
		if err := mgr.Start(ctx); err != nil {
			logger.WithError(err).Fatal("controller manager exited with error")
		}
	})
	interrupts.WaitForGracefulShutdown()
}
