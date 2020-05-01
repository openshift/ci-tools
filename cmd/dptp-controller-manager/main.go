package main

import (
	"errors"
	"flag"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/pjutil"
	"sigs.k8s.io/controller-runtime"

	"github.com/openshift/ci-tools/pkg/controller/image-stream-tag-reconciler"
	"github.com/openshift/ci-tools/pkg/load/agents"
)

type options struct {
	leaderElectionNamespace      string
	ciOperatorconfigPath         string
	configPath                   string
	jobConfigPath                string
	dryRun                       bool
	imageStreamTagReconcilerOpts imageStreamTagReconcilerOptions
	*flagutil.GitHubOptions
}

type imageStreamTagReconcilerOptions struct {
	IgnoredGitHubOrganizations flagutil.Strings
}

func newOpts() (*options, error) {
	opts := &options{GitHubOptions: &flagutil.GitHubOptions{}}
	opts.AddFlags(flag.CommandLine)
	flag.StringVar(&opts.leaderElectionNamespace, "leader-election-namespace", "ci", "The namespace to use for leaderelection")
	flag.StringVar(&opts.ciOperatorconfigPath, "ci-operator-config-path", "", "Path to the ci operator config")
	flag.StringVar(&opts.configPath, "config-path", "", "Path to the prow config")
	flag.StringVar(&opts.jobConfigPath, "job-config-path", "", "Path to the job config")
	flag.Var(&opts.imageStreamTagReconcilerOpts.IgnoredGitHubOrganizations, "imagestreamtagreconciler.ignored-github-organization", "GitHub organization to ignore in the imagestreamtagreconciler. Can be specified multiple times")
	// TODO: rather than relying on humans implementing dry-run properly, we should switch
	// to just do it on client-level once it becomes available: https://github.com/kubernetes-sigs/controller-runtime/pull/839
	flag.BoolVar(&opts.dryRun, "dry-run", true, "Whether to run the controller-manager with dry-run")
	flag.Parse()

	var errs []error
	if opts.leaderElectionNamespace == "" {
		errs = append(errs, errors.New("--leader-election-namespace must be set"))
	}
	if opts.ciOperatorconfigPath == "" {
		errs = append(errs, errors.New("--ci-operations-config-path must be set"))
	}
	if opts.configPath == "" {
		errs = append(errs, errors.New("--config-path must be set"))
	}
	if opts.jobConfigPath == "" {
		errs = append(errs, errors.New("--job-config-path must be set"))
	}

	if err := opts.GitHubOptions.Validate(opts.dryRun); err != nil {
		errs = append(errs, err)
	}

	return opts, utilerrors.NewAggregate(errs)
}

func main() {
	opts, err := newOpts()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get options")
	}

	cfg, err := controllerruntime.GetConfig()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get kubeconfig")
	}

	ciOPConfigAgent, err := agents.NewConfigAgent(opts.ciOperatorconfigPath, 2*time.Minute, prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"error"}))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct ci-opeartor config agent")
	}
	configAgent := &config.Agent{}
	if err := configAgent.Start(opts.configPath, opts.jobConfigPath); err != nil {
		logrus.WithError(err).Fatal("Failed to start config agent")
	}

	secretAgent := &secret.Agent{}
	if err := secretAgent.Start([]string{opts.GitHubOptions.TokenPath}); err != nil {
		logrus.WithError(err).Fatal("Failed to start secrets agent.")
	}
	gitHubClient, err := opts.GitHubClient(secretAgent, opts.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get gitHubClient")
	}

	// Needed by the ImageStreamTagReconciler. This is a setting on the SharedInformer
	// so its applied for all watches for all controller in this manager. If needed,
	// we can move this to a custom sigs.k8s.io/controller-runtime/pkg/source.Source
	// so its only applied for the ImageStreamTagReconciler.
	resyncInterval := 24 * time.Hour
	mgr, err := controllerruntime.NewManager(cfg, controllerruntime.Options{
		LeaderElection:          true,
		LeaderElectionNamespace: opts.leaderElectionNamespace,
		LeaderElectionID:        "dptp-controller-manager",
		SyncPeriod:              &resyncInterval,
	})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct manager")
	}
	pjutil.ServePProf()

	imageStreamTagReconcilerOpts := imagestreamtagreconciler.Options{
		DryRun:                     opts.dryRun,
		CIOperatorConfigAgent:      ciOPConfigAgent,
		ConfigGetter:               configAgent.Config,
		GitHubClient:               gitHubClient,
		IgnoredGitHubOrganizations: opts.imageStreamTagReconcilerOpts.IgnoredGitHubOrganizations.Strings(),
	}
	if err := imagestreamtagreconciler.AddToManager(mgr, imageStreamTagReconcilerOpts); err != nil {
		logrus.WithError(err).Fatal("Failed to add imagestreamtagreconciler")
	}

	stopCh := controllerruntime.SetupSignalHandler()
	if err := mgr.Start(stopCh); err != nil {
		logrus.WithError(err).Fatal("Manager ended with error")
	}

	logrus.Info("Process ended gracefully")
}
