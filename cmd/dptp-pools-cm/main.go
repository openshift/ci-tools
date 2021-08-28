package main

import (
	"context"
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
	ctrlruntimelog "sigs.k8s.io/controller-runtime/pkg/log"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	poolspullsecretprovider "github.com/openshift/ci-tools/pkg/controller/cluster_pools_pull_secret_provider"
	"github.com/openshift/ci-tools/pkg/util"
)

const (
	hiveContextName = string(api.HiveCluster)
)

var allControllers = sets.NewString(
	poolspullsecretprovider.ControllerName,
)

type options struct {
	leaderElectionNamespace        string
	leaderElectionSuffix           string
	kubernetesOptions              flagutil.KubernetesOptions
	enabledControllers             flagutil.Strings
	enabledControllersSet          sets.String
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
	opts := &options{kubernetesOptions: flagutil.KubernetesOptions{NOInClusterConfigDefault: true}}
	opts.addDefaults()
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&opts.leaderElectionNamespace, "leader-election-namespace", "ci", "The namespace to use for leaderelection")
	opts.kubernetesOptions.AddFlags(fs)
	fs.StringVar(&opts.leaderElectionSuffix, "leader-election-suffix", "", "Suffix for the leader election lock. Useful for local testing. If set, --dry-run must be set as well")
	fs.Var(&opts.enabledControllers, "enable-controller", fmt.Sprintf("Enabled controllers. Available controllers are: %v. Can be specified multiple times. Defaults to %v", allControllers.List(), opts.enabledControllers.Strings()))
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
		opts.enabledControllersSet = sets.NewString(vals...)
		if diff := opts.enabledControllersSet.Difference(allControllers); len(diff.UnsortedList()) > 0 {
			errs = append(errs, fmt.Errorf("the following controllers are unknown but were disabled via --disable-controller: %v", diff.List()))
		}
	}
	if err := opts.kubernetesOptions.Validate(false); err != nil {
		errs = append(errs, err)
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
	ctx, cancel := context.WithCancel(ctx)

	kubeconfigChangedCallBack := func() {
		logrus.Info("Kubeconfig changed, exiting to get restarted by Kubelet and pick up the changes")
		cancel()
	}

	kubeconfigs, err := util.LoadKubeConfigs(opts.kubernetesOptions, kubeconfigChangedCallBack)
	if err != nil {
		logrus.WithError(err).Fatal("failed to load kubeconfigs")
	}
	hiveConfig, hasHive := kubeconfigs[hiveContextName]
	if !hasHive {
		kubeconfigs[hiveContextName], err = rest.InClusterConfig()
		if err != nil {
			logrus.WithError(err).Fatalf("--kubeconfig had no context for '%s' and loading InClusterConfig failed", hiveContextName)
		}
		logrus.Infof("Loaded %q context from in-cluster config", hiveContextName)
		if err := util.WatchFiles([]string{"/var/run/secrets/kubernetes.io/serviceaccount/token"}, kubeconfigChangedCallBack); err != nil {
			logrus.WithError(err).Fatal("failed to watch in-cluster token")
		}
	}

	mgr, err := controllerruntime.NewManager(hiveConfig, controllerruntime.Options{
		DryRunClient:                  opts.dryRun,
		Logger:                        ctrlruntimelog.NullLogger{},
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
			logrus.WithError(err).Fatal("Failed to construct the testimagestreamimportcleaner controller")
		}
	}

	if err := mgr.Start(ctx); err != nil {
		logrus.WithError(err).Fatal("Manager ended with error")
	}

	logrus.Info("Process ended gracefully")
}
