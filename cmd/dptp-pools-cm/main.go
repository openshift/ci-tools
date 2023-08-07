package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/logrusutil"
	controllerruntime "sigs.k8s.io/controller-runtime"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	poolspullsecretprovider "github.com/openshift/ci-tools/pkg/controller/cluster_pools_pull_secret_provider"
	hypershiftnamespacereconciler "github.com/openshift/ci-tools/pkg/controller/hypershift_namespace_reconciler"
)

var allControllers = sets.New[string](
	poolspullsecretprovider.ControllerName,
	hypershiftnamespacereconciler.ControllerName,
)

type options struct {
	leaderElectionNamespace        string
	leaderElectionSuffix           string
	enabledControllers             flagutil.Strings
	enabledControllersSet          sets.Set[string]
	dryRun                         bool
	poolsPullSecretProviderOptions poolsPullSecretProviderOptions
}

func (o *options) addDefaults() {
	o.enabledControllers = flagutil.NewStrings(poolspullsecretprovider.ControllerName)
}

type poolsPullSecretProviderOptions struct {
	sourcePullSecretNamespace string
	sourcePullSecretName      string
}

func newOpts() (*options, error) {
	opts := &options{}
	opts.addDefaults()
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&opts.leaderElectionNamespace, "leader-election-namespace", "ci", "The namespace to use for leaderelection")
	fs.StringVar(&opts.leaderElectionSuffix, "leader-election-suffix", "", "Suffix for the leader election lock. Useful for local testing. If set, --dry-run must be set as well")
	fs.Var(&opts.enabledControllers, "enable-controller", fmt.Sprintf("Enabled controllers. Available controllers are: %v. Can be specified multiple times. Defaults to %v", sets.List(allControllers), opts.enabledControllers.Strings()))
	fs.StringVar(&opts.poolsPullSecretProviderOptions.sourcePullSecretNamespace, "poolsPullSecretProviderOptions.sourcePullSecretNamespace", "ci-cluster-pool", "The namespace where the source pull secret is")
	fs.StringVar(&opts.poolsPullSecretProviderOptions.sourcePullSecretName, "poolsPullSecretProviderOptions.sourcePullSecretName", "pull-secret", "The name of the source pull secret")
	fs.BoolVar(&opts.dryRun, "dry-run", true, "Whether to run the controller-manager with dry-run")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse args")
	}

	var errs []error
	if opts.leaderElectionNamespace == "" {
		errs = append(errs, errors.New("--leader-election-namespace must be set"))
	}
	if vals := opts.enabledControllers.Strings(); len(vals) > 0 {
		opts.enabledControllersSet = sets.New[string](vals...)
		if diff := opts.enabledControllersSet.Difference(allControllers); len(sets.List(diff)) > 0 {
			errs = append(errs, fmt.Errorf("the following controllers are unknown but were disabled via --disable-controller: %v", sets.List(diff)))
		}
	}
	return opts, utilerrors.NewAggregate(errs)
}

func main() {
	logrusutil.ComponentInit()

	opts, err := newOpts()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get options")
	}

	ctx := controllerruntime.SetupSignalHandler()

	inClusterConfig, err := rest.InClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load in-cluster config")
	}

	mgr, err := controllerruntime.NewManager(inClusterConfig, controllerruntime.Options{
		DryRunClient:                  opts.dryRun,
		LeaderElection:                true,
		LeaderElectionReleaseOnCancel: true,
		LeaderElectionNamespace:       opts.leaderElectionNamespace,
		LeaderElectionID:              fmt.Sprintf("dptp-pools-cm%s", opts.leaderElectionSuffix),
	})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct manager for the hive cluster")
	}

	if err := hivev1.AddToScheme(mgr.GetScheme()); err != nil {
		logrus.WithError(err).Fatal("Failed to add hivev1 to scheme")
	}

	if opts.enabledControllersSet.Has(poolspullsecretprovider.ControllerName) {
		if err := poolspullsecretprovider.AddToManager(mgr, opts.poolsPullSecretProviderOptions.sourcePullSecretNamespace, opts.poolsPullSecretProviderOptions.sourcePullSecretName); err != nil {
			logrus.WithField("name", poolspullsecretprovider.ControllerName).WithError(err).Fatal("Failed to construct the controller")
		}
	}

	if opts.enabledControllersSet.Has(hypershiftnamespacereconciler.ControllerName) {
		if err := hypershiftnamespacereconciler.AddToManager(mgr); err != nil {
			logrus.WithField("name", hypershiftnamespacereconciler.ControllerName).WithError(err).Fatal("Failed to construct the controller")
		}
	}

	if err := mgr.Start(ctx); err != nil {
		logrus.WithError(err).Fatal("Manager ended with error")
	}

	logrus.Info("Process ended gracefully")
}
