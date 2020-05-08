package main

import (
	"errors"
	"flag"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/pjutil"
	"sigs.k8s.io/controller-runtime"

	"github.com/openshift/ci-tools/pkg/controller/promotionreconciler"
	"github.com/openshift/ci-tools/pkg/load/agents"
)

type options struct {
	leaderElectionNamespace       string
	ciOperatorconfigPath          string
	configPath                    string
	jobConfigPath                 string
	registryClusterKubeconfigPath string
	dryRun                        bool
	promotionreconcilerOptions    promotionreconcilerOptions
	*flagutil.GitHubOptions
}

type promotionreconcilerOptions struct {
	IgnoredGitHubOrganizations flagutil.Strings
}

func newOpts() (*options, error) {
	opts := &options{GitHubOptions: &flagutil.GitHubOptions{}}
	opts.AddFlags(flag.CommandLine)
	flag.StringVar(&opts.leaderElectionNamespace, "leader-election-namespace", "ci", "The namespace to use for leaderelection")
	flag.StringVar(&opts.ciOperatorconfigPath, "ci-operator-config-path", "", "Path to the ci operator config")
	flag.StringVar(&opts.configPath, "config-path", "", "Path to the prow config")
	flag.StringVar(&opts.jobConfigPath, "job-config-path", "", "Path to the job config")
	flag.StringVar(&opts.registryClusterKubeconfigPath, "registry-cluster-kubeconfig", "", "If set, this kubeconfig will be used to access registry-related resources like Images and ImageStreams. Defaults to the global kubeconfig")
	flag.Var(&opts.promotionreconcilerOptions.IgnoredGitHubOrganizations, "promotionreconcilerOptions.ignored-github-organization", "GitHub organization to ignore in the imagestreamtagreconciler. Can be specified multiple times")
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

	mgr, err := controllerruntime.NewManager(cfg, controllerruntime.Options{
		LeaderElection:          true,
		LeaderElectionNamespace: opts.leaderElectionNamespace,
		LeaderElectionID:        "dptp-controller-manager",
	})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct manager")
	}
	pjutil.ServePProf()

	var registryMgr controllerruntime.Manager
	if opts.registryClusterKubeconfigPath == "" {
		registryMgr = mgr
	} else {
		logrus.WithField("path", opts.registryClusterKubeconfigPath).Info("Using dedicated kubeconfig for interacting with registry resources")
		registryCFG, err := clientcmd.BuildConfigFromFlags("", opts.registryClusterKubeconfigPath)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to load registry kubeconfig")
		}
		// Needed by the ImageStreamTagReconciler. This is a setting on the SharedInformer
		// so its applied for all watches for all controllers in this manager. If needed,
		// we can move this to a custom sigs.k8s.io/controller-runtime/pkg/source.Source
		// so its only applied for the ImageStreamTagReconciler.
		resyncInterval := 24 * time.Hour
		registryMgr, err = controllerruntime.NewManager(registryCFG, controllerruntime.Options{
			LeaderElection: false,
			// The normal manager already serves these metrics and we must disable it here to not
			// get an error when attempting to create the second listener on the same address.
			MetricsBindAddress: "0",
			SyncPeriod:         &resyncInterval,
		})
		if err != nil {
			logrus.WithError(err).Fatal("Failed to construct manager for registry")
		}
		if err := mgr.Add(registryMgr); err != nil {
			logrus.WithError(err).Fatal("Failed to add registry manager to main manager.")
		}
	}

	imageStreamTagReconcilerOpts := promotionreconciler.Options{
		DryRun:                     opts.dryRun,
		CIOperatorConfigAgent:      ciOPConfigAgent,
		ConfigGetter:               configAgent.Config,
		GitHubClient:               gitHubClient,
		IgnoredGitHubOrganizations: opts.promotionreconcilerOptions.IgnoredGitHubOrganizations.Strings(),
		RegistryManager:            registryMgr,
	}
	if err := promotionreconciler.AddToManager(mgr, imageStreamTagReconcilerOpts); err != nil {
		logrus.WithError(err).Fatal("Failed to add imagestreamtagreconciler")
	}

	stopCh := controllerruntime.SetupSignalHandler()
	if err := mgr.Start(stopCh); err != nil {
		logrus.WithError(err).Fatal("Manager ended with error")
	}

	logrus.Info("Process ended gracefully")
}
