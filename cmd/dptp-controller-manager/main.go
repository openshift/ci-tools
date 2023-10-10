package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/bombsimon/logrusr/v3"
	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/pjutil/pprof"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/controller/promotionreconciler"
	serviceaccountsecretrefresher "github.com/openshift/ci-tools/pkg/controller/serviceaccount_secret_refresher"
	testimagesdistributor "github.com/openshift/ci-tools/pkg/controller/test-images-distributor"
	"github.com/openshift/ci-tools/pkg/controller/testimagestreamimportcleaner"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/prowconfigutils"
)

const (
	appCIContextName = "app.ci"
)

var allControllers = sets.New[string](
	promotionreconciler.ControllerName,
	testimagesdistributor.ControllerName,
	serviceaccountsecretrefresher.ControllerName,
	testimagestreamimportcleaner.ControllerName,
)

type options struct {
	leaderElectionNamespace              string
	ciOperatorconfigPath                 string
	stepConfigPath                       string
	prowconfig                           configflagutil.ConfigOptions
	kubernetesOptions                    flagutil.KubernetesOptions
	leaderElectionSuffix                 string
	enabledControllers                   flagutil.Strings
	enabledControllersSet                sets.Set[string]
	registryClusterName                  string
	dryRun                               bool
	blockProfileRate                     time.Duration
	testImagesDistributorOptions         testImagesDistributorOptions
	serviceAccountSecretRefresherOptions serviceAccountSecretRefresherOptions
	imagePusherOptions                   imagePusherOptions
	promotionReconcilerOptions           promotionReconcilerOptions
	*flagutil.GitHubOptions
	releaseRepoGitSyncPath string
}

func (o *options) addDefaults() {
	o.enabledControllers = flagutil.NewStrings(promotionreconciler.ControllerName, testimagesdistributor.ControllerName)
}

type testImagesDistributorOptions struct {
	additionalImageStreamTagsRaw       flagutil.Strings
	additionalImageStreamTags          sets.Set[string]
	additionalImageStreamsRaw          flagutil.Strings
	additionalImageStreams             sets.Set[string]
	additionalImageStreamNamespacesRaw flagutil.Strings
	additionalImageStreamNamespaces    sets.Set[string]
	forbiddenRegistriesRaw             flagutil.Strings
	forbiddenRegistries                sets.Set[string]
	ignoreClusterNamesRaw              flagutil.Strings
	ignoreClusterNames                 sets.Set[string]
}

type promotionReconcilerOptions struct {
	ignoreImageStreamsRaw flagutil.Strings
	ignoreImageStreams    []*regexp.Regexp
}

type imagePusherOptions struct {
	imageStreamsRaw flagutil.Strings
	imageStreams    sets.Set[string]
}

type serviceAccountSecretRefresherOptions struct {
	enabledNamespaces     flagutil.Strings
	removeOldSecrets      bool
	ignoreServiceAccounts flagutil.Strings
}

func newOpts() (*options, error) {
	opts := &options{GitHubOptions: &flagutil.GitHubOptions{}}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	opts.prowconfig.AddFlags(fs)
	opts.addDefaults()
	opts.GitHubOptions.AddFlags(fs)
	opts.GitHubOptions.AllowAnonymous = true
	fs.StringVar(&opts.leaderElectionNamespace, "leader-election-namespace", "ci", "The namespace to use for leaderelection")
	opts.kubernetesOptions.AddFlags(fs)
	fs.StringVar(&opts.ciOperatorconfigPath, "ci-operator-config-path", "", "Path to the ci operator config")
	fs.StringVar(&opts.stepConfigPath, "step-config-path", "", "Path to the registries step configuration")
	fs.StringVar(&opts.leaderElectionSuffix, "leader-election-suffix", "", "Suffix for the leader election lock. Useful for local testing. If set, --dry-run must be set as well")
	fs.Var(&opts.enabledControllers, "enable-controller", fmt.Sprintf("Enabled controllers. Available controllers are: %v. Can be specified multiple times. Defaults to %v", sets.List(allControllers), opts.enabledControllers.Strings()))
	fs.Var(&opts.testImagesDistributorOptions.additionalImageStreamTagsRaw, "testImagesDistributorOptions.additional-image-stream-tag", "An imagestreamtag that will be distributed even if no test explicitly references it. It must be in namespace/name:tag format (e.G `ci/clonerefs:latest`). Can be passed multiple times.")
	fs.Var(&opts.testImagesDistributorOptions.additionalImageStreamsRaw, "testImagesDistributorOptions.additional-image-stream", "An imagestream that will be distributed even if no test explicitly references it. It must be in namespace/name format (e.G `ci/clonerefs`). Can be passed multiple times.")
	fs.Var(&opts.testImagesDistributorOptions.additionalImageStreamNamespacesRaw, "testImagesDistributorOptions.additional-image-stream-namespace", "A namespace in which imagestreams will be distributed even if no test explicitly references them (e.G `ci`). Can be passed multiple times.")
	fs.Var(&opts.testImagesDistributorOptions.forbiddenRegistriesRaw, "testImagesDistributorOptions.forbidden-registry", "The hostname of an image registry from which there is no synchronization of its images. Can be passed multiple times.")
	fs.Var(&opts.testImagesDistributorOptions.ignoreClusterNamesRaw, "testImagesDistributorOptions.ignore-cluster-name", "The cluster name to which there is no synchronization of test images. Can be passed multiple times.")
	fs.DurationVar(&opts.blockProfileRate, "block-profile-rate", time.Duration(0), "The block profile rate. Set to non-zero to enable.")
	fs.StringVar(&opts.registryClusterName, "registry-cluster-name", "app.ci", "the cluster name on which the CI central registry is running")
	fs.Var(&opts.serviceAccountSecretRefresherOptions.enabledNamespaces, "serviceAccountRefresherOptions.enabled-namespace", "A namespace for which the serviceaccount_secret_refresher should be enabled. Can be passed multiple times.")
	fs.BoolVar(&opts.serviceAccountSecretRefresherOptions.removeOldSecrets, "serviceAccountRefresherOptions.remove-old-secrets", false, "whether the serviceaccountsecretrefresher should delete secrets older than 30 days")
	fs.Var(&opts.serviceAccountSecretRefresherOptions.ignoreServiceAccounts, "serviceAccountRefresherOptions.ignore-service-account", "The service account to ignore. It must be in namespace/name format (e.G `ci/sync-rover-groups-updater`). Can be passed multiple times.")
	fs.Var(&opts.imagePusherOptions.imageStreamsRaw, "imagePusherOptions.image-stream", "An imagestream that will be synced. It must be in namespace/name format (e.G `ci/clonerefs`). Can be passed multiple times.")
	fs.Var(&opts.promotionReconcilerOptions.ignoreImageStreamsRaw, "promotionReconcilerOptions.ignore-image-stream", "The image stream to ignore. It is an regular expression (e.G ^openshift-priv/.+). Can be passed multiple times.")
	fs.BoolVar(&opts.dryRun, "dry-run", true, "Whether to run the controller-manager with dry-run")
	fs.StringVar(&opts.releaseRepoGitSyncPath, "release-repo-git-sync-path", "", "Path to release repository dir")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse args")
	}

	var errs []error
	if opts.releaseRepoGitSyncPath != "" {
		if opts.ciOperatorconfigPath != "" || opts.stepConfigPath != "" || opts.prowconfig.JobConfigPath != "" || opts.prowconfig.SupplementalProwConfigDirs.String() != "" {
			errs = append(errs, fmt.Errorf("--release-repo-path is mutually exclusive with --ci-operator-config-path and --step-config-path and --%s and --supplemental-prow-config-dir", opts.prowconfig.JobConfigPathFlagName))
		} else {
			if _, err := os.Stat(opts.releaseRepoGitSyncPath); err != nil {
				if os.IsNotExist(err) {
					errs = append(errs, fmt.Errorf("--release-repo-path points to a nonexistent directory: %w", err))
				}
				errs = append(errs, fmt.Errorf("error getting stat info for --release-repo-path directory: %w", err))
			}

			opts.ciOperatorconfigPath = filepath.Join(opts.releaseRepoGitSyncPath, config.CiopConfigInRepoPath)
			opts.stepConfigPath = filepath.Join(opts.releaseRepoGitSyncPath, config.RegistryPath)
			opts.prowconfig.JobConfigPath = filepath.Join(opts.releaseRepoGitSyncPath, config.JobConfigInRepoPath)
			opts.prowconfig.ConfigPath = filepath.Join(opts.releaseRepoGitSyncPath, config.ConfigInRepoPath)
			if err := opts.prowconfig.SupplementalProwConfigDirs.Set(filepath.Dir(filepath.Join(opts.releaseRepoGitSyncPath, config.ConfigInRepoPath))); err != nil {
				errs = append(errs, fmt.Errorf("could not set supplemental prow config dirs: %w", err))
			}

		}
	}
	if opts.leaderElectionNamespace == "" {
		errs = append(errs, errors.New("--leader-election-namespace must be set"))
	}
	if opts.ciOperatorconfigPath == "" {
		errs = append(errs, errors.New("--ci-operations-config-path must be set"))
	}
	if err := opts.prowconfig.Validate(false); err != nil {
		errs = append(errs, err)
	}
	if vals := opts.enabledControllers.Strings(); len(vals) > 0 {
		opts.enabledControllersSet = sets.New[string](vals...)
		if diff := opts.enabledControllersSet.Difference(allControllers); len(sets.List(diff)) > 0 {
			errs = append(errs, fmt.Errorf("the following controllers are unknown but were disabled via --disable-controller: %v", sets.List(diff)))
		}
	}

	isTags, isTagErrors := completeImageStreamTags("testImagesDistributorOptions.additional-image-stream-tag", opts.testImagesDistributorOptions.additionalImageStreamTagsRaw)
	errs = append(errs, isTagErrors...)
	opts.testImagesDistributorOptions.additionalImageStreamTags = isTags

	imageStreams, isErrors := completeImageStream("testImagesDistributorOptions.additional-image-stream", opts.testImagesDistributorOptions.additionalImageStreamsRaw)
	errs = append(errs, isErrors...)
	opts.testImagesDistributorOptions.additionalImageStreams = imageStreams

	opts.testImagesDistributorOptions.additionalImageStreamNamespaces = completeSet(opts.testImagesDistributorOptions.additionalImageStreamNamespacesRaw)
	opts.testImagesDistributorOptions.forbiddenRegistries = completeSet(opts.testImagesDistributorOptions.forbiddenRegistriesRaw)
	opts.testImagesDistributorOptions.ignoreClusterNames = completeSet(opts.testImagesDistributorOptions.ignoreClusterNamesRaw)

	imagePusherImageStreams, isErrors := completeImageStream("uniRegistrySyncerOptions.image-stream", opts.imagePusherOptions.imageStreamsRaw)
	errs = append(errs, isErrors...)
	opts.imagePusherOptions.imageStreams = imagePusherImageStreams

	if raws := opts.promotionReconcilerOptions.ignoreImageStreamsRaw.Strings(); len(raws) > 0 {
		for _, raw := range raws {
			re, err := regexp.Compile(raw)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to compile regex from %q: %w", raw, err))
				continue
			}
			opts.promotionReconcilerOptions.ignoreImageStreams = append(opts.promotionReconcilerOptions.ignoreImageStreams, re)
		}
	}

	if opts.enabledControllersSet.Has(testimagesdistributor.ControllerName) && opts.stepConfigPath == "" {
		errs = append(errs, fmt.Errorf("--step-config-path is required when the %s controller is enabled", testimagesdistributor.ControllerName))
	}

	if opts.enabledControllersSet.Has(serviceaccountsecretrefresher.ControllerName) {
		if len(opts.serviceAccountSecretRefresherOptions.enabledNamespaces.Strings()) == 0 {
			errs = append(errs, fmt.Errorf("--serviceAccountRefresherOptions.enabled-namespace must be set at least once when enabling the %s controller, otherwise it won't do anything", serviceaccountsecretrefresher.ControllerName))
		}
	}

	if err := opts.GitHubOptions.Validate(opts.dryRun); err != nil {
		errs = append(errs, err)
	}
	if err := opts.kubernetesOptions.Validate(false); err != nil {
		errs = append(errs, err)
	}
	return opts, utilerrors.NewAggregate(errs)
}

func completeImageStreamTags(name string, raw flagutil.Strings) (sets.Set[string], []error) {
	isTags := sets.Set[string]{}
	var errs []error
	if vals := raw.Strings(); len(vals) > 0 {
		for _, val := range vals {
			slashSplit := strings.Split(val, "/")
			if len(slashSplit) != 2 {
				errs = append(errs, fmt.Errorf("--%s value %s was not in namespace/name:tag format", name, val))
				continue
			}
			if dotSplit := strings.Split(slashSplit[1], ":"); len(dotSplit) != 2 {
				errs = append(errs, fmt.Errorf("name in --%s must be of imagestreamname:tag format, wasn't the case for %s", name, slashSplit[1]))
				continue
			}
			isTags.Insert(val)
		}
	}
	return isTags, errs
}

func completeImageStream(name string, raw flagutil.Strings) (sets.Set[string], []error) {
	imageStreams := sets.Set[string]{}
	var errs []error
	if vals := raw.Strings(); len(vals) > 0 {
		for _, val := range vals {
			slashSplit := strings.Split(val, "/")
			if len(slashSplit) != 2 {
				errs = append(errs, fmt.Errorf("--%s value %s was not in namespace/name format", name, val))
				continue
			}
			imageStreams.Insert(val)
		}
	}
	return imageStreams, errs
}

func completeSet(raw flagutil.Strings) sets.Set[string] {
	result := sets.Set[string]{}
	if vals := raw.Strings(); len(vals) > 0 {
		for _, val := range vals {
			result.Insert(val)
		}
	}
	return result
}

func main() {
	logrusutil.ComponentInit()
	controllerruntime.SetLogger(logrusr.New(logrus.StandardLogger()))

	opts, err := newOpts()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get options")
	}
	if val := int(opts.blockProfileRate.Nanoseconds()); val != 0 {
		logrus.WithField("rate", opts.blockProfileRate.String()).Info("Setting block profile rate")
		runtime.SetBlockProfileRate(val)
	}

	_, err = prowconfigutils.ProwDisabledClusters(&opts.kubernetesOptions)
	if err != nil {
		logrus.WithError(err).Warn("Failed to get Prow disable clusters")
	}

	ctx := controllerruntime.SetupSignalHandler()
	ctx, cancel := context.WithCancel(ctx)

	kubeconfigChangedCallBack := func() {
		logrus.Info("Kubeconfig changed, exiting to get restarted by Kubelet and pick up the changes")
		cancel()
	}

	kubeconfigs, err := opts.kubernetesOptions.LoadClusterConfigs(kubeconfigChangedCallBack)
	if err != nil {
		logrus.WithError(err).Fatal("failed to load kubeconfigs")
	}

	inClusterConfig, hasInClusterConfig := kubeconfigs[kube.InClusterContext]
	delete(kubeconfigs, kube.InClusterContext)
	delete(kubeconfigs, kube.DefaultClusterAlias)

	if _, hasAppCi := kubeconfigs[appCIContextName]; !hasAppCi {
		if !hasInClusterConfig {
			logrus.WithError(err).Fatalf("had no context for '%s' and loading InClusterConfig failed", appCIContextName)
		}
		logrus.Infof("use InClusterConfig for %s", appCIContextName)
		kubeconfigs[appCIContextName] = inClusterConfig
	}

	if _, hasRegistryCluster := kubeconfigs[opts.registryClusterName]; !hasRegistryCluster {
		logrus.Fatalf("--kubeconfig must include a context named `%s`", opts.registryClusterName)
	}
	configAgentOption := func(*agents.ConfigAgentOptions) {}
	registryAgentOption := func(*agents.RegistryAgentOptions) {}
	if opts.releaseRepoGitSyncPath != "" {
		eventCh := make(chan fsnotify.Event)
		errCh := make(chan error)

		universalSymlinkWatcher := &agents.UniversalSymlinkWatcher{
			EventCh:   eventCh,
			ErrCh:     errCh,
			WatchPath: opts.releaseRepoGitSyncPath,
		}

		configAgentOption = func(opt *agents.ConfigAgentOptions) {
			opt.UniversalSymlinkWatcher = universalSymlinkWatcher
		}
		registryAgentOption = func(opt *agents.RegistryAgentOptions) {
			opt.UniversalSymlinkWatcher = universalSymlinkWatcher
		}

		watcher, err := universalSymlinkWatcher.GetWatcher()
		if err != nil {
			logrus.Fatalf("Failed to get the universal symlink watcher: %v", err)
		}
		interrupts.Run(watcher)
	}
	configErrCh := make(chan error)
	ciOPConfigAgent, err := agents.NewConfigAgent(opts.ciOperatorconfigPath, configErrCh, configAgentOption)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct ci-operator config agent")
	}
	go func() { logrus.Fatal(<-configErrCh) }()
	configAgent, err := opts.prowconfig.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to start config agent")
	}

	allManagers := map[string]controllerruntime.Manager{}
	allClustersExceptRegistryCluster := map[string]controllerruntime.Manager{}
	var registryMgr controllerruntime.Manager

	var errs []error
	for cluster, cfg := range kubeconfigs {
		cluster, cfg := cluster, cfg
		if _, alreadyExists := allManagers[cluster]; alreadyExists {
			logrus.Fatalf("attempted duplicate creation of manager for cluster %s", cluster)
		}

		options := controllerruntime.Options{
			DryRunClient: opts.dryRun,
		}
		if cluster == appCIContextName {
			options.LeaderElection = true
			options.LeaderElectionReleaseOnCancel = true
			options.LeaderElectionNamespace = opts.leaderElectionNamespace
			options.LeaderElectionID = fmt.Sprintf("dptp-controller-manager%s", opts.leaderElectionSuffix)
		} else {
			options.MetricsBindAddress = "0"
		}
		if cluster == opts.registryClusterName {
			syncPeriod := 24 * time.Hour
			options.SyncPeriod = &syncPeriod
		}
		logrus.WithField("cluster", cluster).Info("Creating manager ...")
		mgr, err := controllerruntime.NewManager(&cfg, options)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to construct manager for cluster %s: %w", cluster, err))
			continue
		}
		allManagers[cluster] = mgr
		if cluster == opts.registryClusterName {
			registryMgr = mgr
		} else {
			allClustersExceptRegistryCluster[cluster] = mgr
		}
	}
	if err := utilerrors.NewAggregate(errs); err != nil {
		logrus.WithError(err).Fatal("Failed to construct cluster managers")
	}

	mgr := allManagers[appCIContextName]
	if err := imagev1.AddToScheme(mgr.GetScheme()); err != nil {
		logrus.WithError(err).Fatal("Failed to add imagev1 to scheme")
	}
	// The image api is implemented via the Openshift Extension APIServer, so contrary
	// to CRD-Based resources it supports protobuf.
	if err := apiutil.AddToProtobufScheme(imagev1.AddToScheme); err != nil {
		logrus.WithError(err).Fatal("Failed to add imagev1 api to protobuf scheme")
	}
	if err := prowv1.AddToScheme(mgr.GetScheme()); err != nil {
		logrus.WithError(err).Fatal("Failed to add prowv1 to scheme")
	}
	pprof.Serve(flagutil.DefaultPProfPort)

	for cluster, buildClusterMgr := range allManagers {
		if cluster == appCIContextName {
			continue
		}
		if err := mgr.Add(buildClusterMgr); err != nil {
			errs = append(errs, fmt.Errorf("failed to add buildClusterMgr for cluster %s to main mgr: %w", cluster, err))
			continue
		}
	}
	if err := utilerrors.NewAggregate(errs); err != nil {
		logrus.WithError(err).Fatal("Failed to add build cluster managers")
	}

	if opts.GitHubOptions.TokenPath != "" {
		if err := secret.Add(opts.GitHubOptions.TokenPath); err != nil {
			logrus.WithError(err).Fatal("Failed to start secret agent")
		}
	}

	if opts.enabledControllersSet.Has(promotionreconciler.ControllerName) {
		gitHubClient, err := opts.GitHubClient(opts.dryRun)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to get gitHubClient")
		}
		// Use a very low limit, reconciling promotions slow is not much of a problem, running out of tokens is.
		// Also we have to keep in mind that we might end up multiple budgets per period, because the client-side
		// reset is not synchronized with the github reset and we may get upgraded in which case we lose the bucket
		// state.
		if err := gitHubClient.Throttle(600, 300); err != nil {
			logrus.WithError(err).Fatal("Failed to throttle")
		}
		promotionreconcilerOptions := promotionreconciler.Options{
			DryRun:                opts.dryRun,
			CIOperatorConfigAgent: ciOPConfigAgent,
			ConfigGetter:          configAgent.Config,
			GitHubClient:          gitHubClient,
			RegistryManager:       registryMgr,
			IgnoredImageStreams:   opts.promotionReconcilerOptions.ignoreImageStreams,
		}
		if err := promotionreconciler.AddToManager(mgr, promotionreconcilerOptions); err != nil {
			logrus.WithError(err).Fatal("Failed to add imagestreamtagreconciler")
		}
	}

	if opts.enabledControllersSet.Has(testimagesdistributor.ControllerName) {
		if err := controllerutil.RegisterMetrics(); err != nil {
			logrus.WithError(err).Fatal("failed to register metrics")
		}
	}

	if opts.enabledControllersSet.Has(testimagesdistributor.ControllerName) {
		registryErrCh := make(chan error)
		registryConfigAgent, err := agents.NewRegistryAgent(opts.stepConfigPath, registryErrCh, registryAgentOption)
		if err != nil {
			logrus.WithError(err).Fatal("failed to construct registryAgent")
		}
		go func() { logrus.Fatal(<-registryErrCh) }()

		registriesExceptAppCI := sets.New[string]()
		for cluster := range allClustersExceptRegistryCluster {
			domain, err := api.RegistryDomainForClusterName(cluster)
			if err != nil {
				logrus.WithError(err).WithField("cluster", cluster).Fatal("failed to get the registry domain")
			}
			registriesExceptAppCI.Insert(domain)
		}
		logrus.WithField("registriesExceptAppCI", sets.List(registriesExceptAppCI)).Info("forbidden registries from build-farm clusters")
		opts.testImagesDistributorOptions.forbiddenRegistries = opts.testImagesDistributorOptions.forbiddenRegistries.Union(registriesExceptAppCI)

		if err := testimagesdistributor.AddToManager(
			mgr,
			opts.registryClusterName,
			registryMgr,
			allClustersExceptRegistryCluster,
			ciOPConfigAgent,
			registryConfigAgent,
			opts.testImagesDistributorOptions.additionalImageStreamTags,
			opts.testImagesDistributorOptions.additionalImageStreams,
			opts.testImagesDistributorOptions.additionalImageStreamNamespaces,
			opts.testImagesDistributorOptions.forbiddenRegistries,
			opts.testImagesDistributorOptions.ignoreClusterNames,
		); err != nil {
			logrus.WithError(err).Fatal("failed to add testimagesdistributor")
		}
	}

	if opts.enabledControllersSet.Has(serviceaccountsecretrefresher.ControllerName) {
		for clusterName, clusterMgr := range allManagers {
			if err := serviceaccountsecretrefresher.AddToManager(clusterName, clusterMgr, opts.serviceAccountSecretRefresherOptions.enabledNamespaces.StringSet(), opts.serviceAccountSecretRefresherOptions.ignoreServiceAccounts.StringSet(), opts.serviceAccountSecretRefresherOptions.removeOldSecrets); err != nil {
				logrus.WithError(err).Fatalf("Failed to add the %s controller to the %s cluster", serviceaccountsecretrefresher.ControllerName, clusterName)
			}
		}
	}

	if opts.enabledControllersSet.Has(testimagestreamimportcleaner.ControllerName) {
		if err := testimagestreamimportcleaner.AddToManager(mgr, allManagers); err != nil {
			logrus.WithError(err).Fatal("Failed to construct the testimagestreamimportcleaner controller")
		}
	}

	if err := mgr.Start(ctx); err != nil {
		logrus.WithError(err).Fatal("Manager ended with error")
	}

	logrus.Info("Process ended gracefully")
}
