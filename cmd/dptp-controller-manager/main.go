package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	imagev1 "github.com/openshift/api/image/v1"
	secretsyncerconfig "github.com/openshift/ci-tools/pkg/controller/secretsyncer/config"
	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/pjutil"
	controllerruntime "sigs.k8s.io/controller-runtime"

	"github.com/openshift/ci-tools/pkg/controller/promotionreconciler"
	"github.com/openshift/ci-tools/pkg/controller/secretsyncer"
	testimagesdistributor "github.com/openshift/ci-tools/pkg/controller/test-images-distributor"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/util"
)

const (
	apiCIContextName = "api.ci"
	appCIContextName = "app.ci"
)

var allControllers = sets.NewString(
	promotionreconciler.ControllerName,
	testimagesdistributor.ControllerName,
	secretsyncer.ControllerName,
)

type options struct {
	leaderElectionNamespace      string
	ciOperatorconfigPath         string
	stepConfigPath               string
	configPath                   string
	jobConfigPath                string
	kubeconfig                   string
	leaderElectionSuffix         string
	enabledControllers           flagutil.Strings
	enabledControllersSet        sets.String
	dryRun                       bool
	testImagesDistributorOptions testImagesDistributorOptions
	secretSyncerConfigOptions    secretSyncerConfigOptions
	*flagutil.GitHubOptions
}

func (o *options) addDefaults() {
	o.enabledControllers = flagutil.NewStrings(promotionreconciler.ControllerName, testimagesdistributor.ControllerName, secretsyncer.ControllerName)
}

type testImagesDistributorOptions struct {
	imagePullSecretPath                string
	additionalImageStreamTagsRaw       flagutil.Strings
	additionalImageStreamTags          sets.String
	additionalImageStreamsRaw          flagutil.Strings
	additionalImageStreams             sets.String
	additionalImageStreamNamespacesRaw flagutil.Strings
	additionalImageStreamNamespaces    sets.String
}

type secretSyncerConfigOptions struct {
	configFile string
}

func newOpts() (*options, error) {
	opts := &options{GitHubOptions: &flagutil.GitHubOptions{}}
	opts.addDefaults()
	opts.GitHubOptions.AddFlags(flag.CommandLine)
	opts.GitHubOptions.AllowAnonymous = true
	flag.StringVar(&opts.leaderElectionNamespace, "leader-election-namespace", "ci", "The namespace to use for leaderelection")
	// Controller-Runtimes root package imports the package that sets this flag
	kubeconfigFlagDescription := "The kubeconfig to use. All contexts in it will be considered a build cluster. If it does not have a context named 'app.ci', loading in-cluster config will be attempted."
	if f := flag.Lookup("kubeconfig"); f != nil {
		f.Usage = kubeconfigFlagDescription
		// https://i.kym-cdn.com/entries/icons/original/000/018/012/this_is_fine.jpeg
		defer func() { opts.kubeconfig = f.Value.String() }()
	} else {
		flag.StringVar(&opts.kubeconfig, "kubeconfig", "", kubeconfigFlagDescription)
	}
	flag.StringVar(&opts.ciOperatorconfigPath, "ci-operator-config-path", "", "Path to the ci operator config")
	flag.StringVar(&opts.stepConfigPath, "step-config-path", "", "Path to the registries step configuration")
	flag.StringVar(&opts.configPath, "config-path", "", "Path to the prow config")
	flag.StringVar(&opts.jobConfigPath, "job-config-path", "", "Path to the job config")
	flag.StringVar(&opts.leaderElectionSuffix, "leader-election-suffix", "", "Suffix for the leader election lock. Useful for local testing. If set, --dry-run must be set as well")
	flag.Var(&opts.enabledControllers, "enable-controller", fmt.Sprintf("Enabled controllers. Available controllers are: %v. Can be specified multiple times. Defaults to %v", allControllers.List(), opts.enabledControllers.Strings()))
	flag.StringVar(&opts.testImagesDistributorOptions.imagePullSecretPath, "testImagesDistributorOptions.imagePullSecretPath", "", "A file to use for reading an ImagePullSecret that will be bound to all `default` ServiceAccounts in all namespaces that have a test ImageStream on all build clusters")
	flag.Var(&opts.testImagesDistributorOptions.additionalImageStreamTagsRaw, "testImagesDistributorOptions.additional-image-stream-tag", "An imagestreamtag that will be distributed even if no test explicitly references it. It must be in namespace/name:tag format (e.G `ci/clonerefs:latest`). Can be passed multiple times.")
	flag.Var(&opts.testImagesDistributorOptions.additionalImageStreamsRaw, "testImagesDistributorOptions.additional-image-stream", "An imagestream that will be distributed even if no test explicitly references it. It must be in namespace/name format (e.G `ci/clonerefs`). Can be passed multiple times.")
	flag.Var(&opts.testImagesDistributorOptions.additionalImageStreamNamespacesRaw, "testImagesDistributorOptions.additional-image-stream-namespace", "A namespace in which imagestreams will be distributed even if no test explicitly references them (e.G `ci`). Can be passed multiple times.")
	flag.StringVar(&opts.secretSyncerConfigOptions.configFile, "secretSyncerConfigOptions.config", "", "The config file for the secret syncer controller")
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
	if vals := opts.enabledControllers.Strings(); len(vals) > 0 {
		opts.enabledControllersSet = sets.NewString(vals...)
		if diff := opts.enabledControllersSet.Difference(allControllers); len(diff.UnsortedList()) > 0 {
			errs = append(errs, fmt.Errorf("the following controllers are unknown but were disabled via --disable-controller: %v", diff.List()))
		}
	}

	opts.testImagesDistributorOptions.additionalImageStreamTags = sets.String{}
	if vals := opts.testImagesDistributorOptions.additionalImageStreamTagsRaw.Strings(); len(vals) > 0 {
		for _, val := range vals {
			slashSplit := strings.Split(val, "/")
			if len(slashSplit) != 2 {
				errs = append(errs, fmt.Errorf("--testImagesDistributorOptions.additional-image-stream-tag value %s was not in namespace/name:tag format", val))
				continue
			}
			if dotSplit := strings.Split(slashSplit[1], ":"); len(dotSplit) != 2 {
				errs = append(errs, fmt.Errorf("name in --testImagesDistributorOptions.additional-image-stream-tag must be of imagestreamname:tag format, wasn't the case for %s", slashSplit[1]))
				continue
			}
			opts.testImagesDistributorOptions.additionalImageStreamTags.Insert(val)
		}
	}
	opts.testImagesDistributorOptions.additionalImageStreams = sets.String{}
	if vals := opts.testImagesDistributorOptions.additionalImageStreamTagsRaw.Strings(); len(vals) > 0 {
		for _, val := range vals {
			slashSplit := strings.Split(val, "/")
			if len(slashSplit) != 2 {
				errs = append(errs, fmt.Errorf("--testImagesDistributorOptions.additional-image-stream value %s was not in namespace/name format", val))
				continue
			}
			opts.testImagesDistributorOptions.additionalImageStreams.Insert(val)
		}
	}
	opts.testImagesDistributorOptions.additionalImageStreamNamespaces = sets.String{}
	if vals := opts.testImagesDistributorOptions.additionalImageStreamNamespacesRaw.Strings(); len(vals) > 0 {
		for _, val := range vals {
			opts.testImagesDistributorOptions.additionalImageStreamNamespaces.Insert(val)
		}
	}

	if opts.enabledControllersSet.Has(testimagesdistributor.ControllerName) && opts.stepConfigPath == "" {
		errs = append(errs, fmt.Errorf("--step-config-path is required when the %s controller is enabled", testimagesdistributor.ControllerName))
	}

	if opts.enabledControllersSet.Has(secretsyncer.ControllerName) && opts.secretSyncerConfigOptions.configFile == "" {
		errs = append(errs, fmt.Errorf("--secretSyncerConfigOptions.config is required when the %s controller is enabled", secretsyncer.ControllerName))
	}

	if err := opts.GitHubOptions.Validate(opts.dryRun); err != nil {
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

	kubeconfigs, _, err := util.LoadKubeConfigs(opts.kubeconfig)
	if err != nil {
		logrus.WithError(err).Fatal("failed to load kubeconfigs")
	}
	if _, hasApiCI := kubeconfigs[apiCIContextName]; !hasApiCI {
		logrus.Fatalf("--kubeconfig must include a context named `%s`", apiCIContextName)
	}
	if _, hasAppCi := kubeconfigs[appCIContextName]; !hasAppCi {
		kubeconfigs[appCIContextName], err = rest.InClusterConfig()
		if err != nil {
			logrus.WithError(err).Fatalf("--kubeconfig had no context for '%s' and loading InClusterConfig failed", appCIContextName)
		}
		logrus.Infof("Loaded %q context from in-cluster config", appCIContextName)
	}

	ciOPConfigAgent, err := agents.NewConfigAgent(opts.ciOperatorconfigPath)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct ci-opeartor config agent")
	}
	configAgent := &config.Agent{}
	if err := configAgent.Start(opts.configPath, opts.jobConfigPath); err != nil {
		logrus.WithError(err).Fatal("Failed to start config agent")
	}

	mgr, err := controllerruntime.NewManager(kubeconfigs[appCIContextName], controllerruntime.Options{
		LeaderElection:          true,
		LeaderElectionNamespace: opts.leaderElectionNamespace,
		LeaderElectionID:        fmt.Sprintf("dptp-controller-manager%s", opts.leaderElectionSuffix),
		DryRunClient:            opts.dryRun,
	})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct manager")
	}

	if err := imagev1.AddToScheme(mgr.GetScheme()); err != nil {
		logrus.WithError(err).Fatal("Failed to add imagev1 to scheme")
	}
	if err := prowv1.AddToScheme(mgr.GetScheme()); err != nil {
		logrus.WithError(err).Fatal("Failed to add prowv1 to scheme")
	}
	pjutil.ServePProf(flagutil.DefaultPProfPort)

	// Needed by the ImageStreamTagReconciler. This is a setting on the SharedInformer
	// so its applied for all watches for all controllers in this manager. If needed,
	// we can move this to a custom sigs.k8s.io/controller-runtime/pkg/source.Source
	// so its only applied for the ImageStreamTagReconciler.
	// TODO alvaroalmean: This is crap. Add a proper time-based trigger on controller-level,
	// not a global one for everything because one controller happens to need it.
	resyncInterval := 24 * time.Hour
	registryMgr, err := controllerruntime.NewManager(kubeconfigs[apiCIContextName], controllerruntime.Options{
		LeaderElection: false,
		// The normal manager already serves these metrics and we must disable it here to not
		// get an error when attempting to create the second listener on the same address.
		MetricsBindAddress: "0",
		SyncPeriod:         &resyncInterval,
		DryRunClient:       opts.dryRun,
	})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct manager for registry")
	}
	if err := mgr.Add(registryMgr); err != nil {
		logrus.WithError(err).Fatal("Failed to add registry manager to main manager.")
	}

	var secretPaths []string
	if opts.GitHubOptions.TokenPath != "" {
		secretPaths = append(secretPaths, opts.GitHubOptions.TokenPath)
	}
	if opts.testImagesDistributorOptions.imagePullSecretPath != "" {
		secretPaths = append(secretPaths, opts.testImagesDistributorOptions.imagePullSecretPath)
	}
	secretAgent := &secret.Agent{}
	if err := secretAgent.Start(secretPaths); err != nil {
		logrus.WithError(err).Fatal("Failed to start secret agent")
	}

	if opts.enabledControllersSet.Has(promotionreconciler.ControllerName) {
		gitHubClient, err := opts.GitHubClient(secretAgent, opts.dryRun)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to get gitHubClient")
		}
		// Use a very low limit, reconciling promotions slow is not much of a problem, running out of tokens is.
		// Also we have to keep in mind that we might end up multiple budgets per period, because the client-side
		// reset is not synchronized with the github reset and we may get upgraded in which case we lose the bucket
		// state.
		gitHubClient.Throttle(300, 300)
		promotionreconcilerOptions := promotionreconciler.Options{
			DryRun:                opts.dryRun,
			CIOperatorConfigAgent: ciOPConfigAgent,
			ConfigGetter:          configAgent.Config,
			GitHubClient:          gitHubClient,
			RegistryManager:       registryMgr,
		}
		if err := promotionreconciler.AddToManager(mgr, promotionreconcilerOptions); err != nil {
			logrus.WithError(err).Fatal("Failed to add imagestreamtagreconciler")
		}
	}

	allManagers := map[string]controllerruntime.Manager{
		appCIContextName: mgr,
		apiCIContextName: registryMgr,
	}
	var errs []error
	for cluster, cfg := range kubeconfigs {
		if cluster == appCIContextName || cluster == apiCIContextName {
			continue
		}
		if _, alreadyExists := allManagers[cluster]; alreadyExists {
			logrus.Fatalf("attempted duplicate creation of manager for cluster %s", cluster)
		}
		buildClusterMgr, err := controllerruntime.NewManager(cfg, controllerruntime.Options{MetricsBindAddress: "0", LeaderElection: false, DryRunClient: opts.dryRun})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to construct manager for cluster %s: %w", cluster, err))
			continue
		}
		if err := mgr.Add(buildClusterMgr); err != nil {
			errs = append(errs, fmt.Errorf("failed to add buildClusterMgr for cluster %s to main mgr: %w", cluster, err))
			continue
		}
		allManagers[cluster] = buildClusterMgr
	}
	if err := utilerrors.NewAggregate(errs); err != nil {
		logrus.WithError(err).Fatal("Failed to construct build cluster managers")
	}

	allClustersExceptAPICI := map[string]controllerruntime.Manager{}
	for cluster, manager := range allManagers {
		if cluster == apiCIContextName {
			continue
		}
		allClustersExceptAPICI[cluster] = manager
	}

	if opts.enabledControllersSet.Has(testimagesdistributor.ControllerName) {
		if opts.testImagesDistributorOptions.imagePullSecretPath == "" {
			logrus.Fatal("The testImagesDistributor requires the --testImagesDistributorOptions.imagePullSecretPath flag to be set ")
		}
		registryConfigAgent, err := agents.NewRegistryAgent(opts.stepConfigPath)
		if err != nil {
			logrus.WithError(err).Fatal("failed to construct registryAgent")
		}

		if err := testimagesdistributor.AddToManager(
			mgr,
			allManagers[apiCIContextName],
			allClustersExceptAPICI,
			ciOPConfigAgent,
			secretAgent.GetTokenGenerator(opts.testImagesDistributorOptions.imagePullSecretPath),
			registryConfigAgent,
			opts.testImagesDistributorOptions.additionalImageStreamTags,
			opts.testImagesDistributorOptions.additionalImageStreams,
			opts.testImagesDistributorOptions.additionalImageStreamNamespaces,
		); err != nil {
			logrus.WithError(err).Fatal("failed to add testimagesdistributor")
		}
	}

	if opts.enabledControllersSet.Has(secretsyncer.ControllerName) {
		secretSyncerConfigAgent := &secretsyncerconfig.Agent{}
		if err := secretSyncerConfigAgent.Start(opts.secretSyncerConfigOptions.configFile); err != nil {
			logrus.WithError(err).Fatal("failed to start secretSyncerConfigAgent")
		}
		if err := secretsyncer.AddToManager(mgr, allManagers[apiCIContextName], allClustersExceptAPICI, secretSyncerConfigAgent.Config); err != nil {
			logrus.WithError(err).Fatal("failed to add secret syncer controller")
		}
	}

	stopCh := controllerruntime.SetupSignalHandler()
	if err := mgr.Start(stopCh); err != nil {
		logrus.WithError(err).Fatal("Manager ended with error")
	}

	logrus.Info("Process ended gracefully")
}
