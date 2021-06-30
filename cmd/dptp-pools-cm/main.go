package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

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
	kubeconfig                     string
	leaderElectionSuffix           string
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
	opts := &options{}
	opts.addDefaults()
	flag.StringVar(&opts.leaderElectionNamespace, "leader-election-namespace", "ci", "The namespace to use for leaderelection")
	// Controller-Runtimes root package imports the package that sets this flag
	kubeconfigFlagDescription := "The kubeconfig to use. All contexts in it will be considered a build cluster. If it does not have a context named 'hive', loading in-cluster config will be attempted."
	if f := flag.Lookup("kubeconfig"); f != nil {
		f.Usage = kubeconfigFlagDescription
		// https://i.kym-cdn.com/entries/icons/original/000/018/012/this_is_fine.jpeg
		defer func() { opts.kubeconfig = f.Value.String() }()
	} else {
		flag.StringVar(&opts.kubeconfig, "kubeconfig", "", kubeconfigFlagDescription)
	}
	flag.StringVar(&opts.leaderElectionSuffix, "leader-election-suffix", "", "Suffix for the leader election lock. Useful for local testing. If set, --dry-run must be set as well")
	flag.Var(&opts.enabledControllers, "enable-controller", fmt.Sprintf("Enabled controllers. Available controllers are: %v. Can be specified multiple times. Defaults to %v", allControllers.List(), opts.enabledControllers.Strings()))
	flag.StringVar(&opts.poolsPullSecretProviderOptions.sourcePullSecretNamespace, "poolsPullSecretProviderOptions.sourcePullSecretNamespace", "ci-cluster-pool", "The namespace where the source pull secret is")
	flag.StringVar(&opts.poolsPullSecretProviderOptions.sourcePullSecretName, "poolsPullSecretProviderOptions.sourcePullSecretName", "pull-secret", "The name of the source pull secret")
	flag.BoolVar(&opts.dryRun, "dry-run", true, "Whether to run the controller-manager with dry-run")
	flag.Parse()

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

	kubeconfigChangedCallBack := func(e fsnotify.Event) {
		logrus.WithField("event", e.String()).Info("Kubeconfig changed, exiting to get restarted by Kubelet and pick up the changes")
		cancel()
	}

	kubeconfigs, _, err := util.LoadKubeConfigs(opts.kubeconfig, kubeconfigChangedCallBack)
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
