package main

import (
	"context"
	"flag"
	"os"

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
		logrus.WithError(err).Fatal("Invalid flag options")
	}

	return o
}

func main() {
	logrusutil.ComponentInit()
	ctrlruntimelog.SetLogger(logrusr.New(logrus.StandardLogger()))

	o := parseOptions()

	configAgent, err := o.config.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}
	cfg := configAgent.Config

	restCfg, err := o.client.InfrastructureClusterConfig(o.dryrun)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get kubeconfig")
	}
	mgr, err := manager.New(restCfg, manager.Options{
		Namespace:          cfg().ProwJobNamespace,
		MetricsBindAddress: "0",
	})
	if err != nil {
		logrus.WithError(err).Fatal("failed to create manager")
	}

	if err := o.client.AddKubeconfigChangeCallback(func() {
		logrus.Info("Kubeconfig changed, exiting to trigger a restart")
		interrupts.Terminate()
	}); err != nil {
		logrus.WithError(err).Fatal("Failed to register kubeconfig callback")
	}

	if o.github.TokenPath != "" {
		if err := secret.Add(o.github.TokenPath); err != nil {
			logrus.WithError(err).Fatal("Error reading GitHub credentials")
		}
	}

	githubClient, err := o.github.GitHubClient(o.dryrun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting GitHub client.")
	}

	configDataProvider := NewConfigDataProvider(cfg)
	go configDataProvider.Run()

	if err := NewReconciler(mgr, configDataProvider, githubClient); err != nil {
		logrus.WithError(err).Fatal("failed to construct github reporter controller")
	}

	interrupts.Run(func(ctx context.Context) {
		if err := mgr.Start(ctx); err != nil {
			logrus.WithError(err).Fatal("Controller manager exited with error.")
		}
	})
	interrupts.WaitForGracefulShutdown()
}
