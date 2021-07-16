package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/bombsimon/logrusr"
	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/pjutil/pprof"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	ctrlruntimelog "sigs.k8s.io/controller-runtime/pkg/log"

	imagev1 "github.com/openshift/api/image/v1"

	promotionnamespacereconciler "github.com/openshift/ci-tools/pkg/controller/promotion_namespace_reconciler"
	"github.com/openshift/ci-tools/pkg/controller/promotionreconciler"
	serviceaccountsecretrefresher "github.com/openshift/ci-tools/pkg/controller/serviceaccount_secret_refresher"
	testimagesdistributor "github.com/openshift/ci-tools/pkg/controller/test-images-distributor"
	"github.com/openshift/ci-tools/pkg/controller/testimagestreamimportcleaner"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/util"
)

const (
	appCIContextName = "app.ci"
)

var allControllers = sets.NewString(
	promotionreconciler.ControllerName,
	testimagesdistributor.ControllerName,
	serviceaccountsecretrefresher.ControllerName,
	testimagestreamimportcleaner.ControllerName,
	promotionnamespacereconciler.ControllerName,
)

type options struct {
	leaderElectionNamespace              string
	ciOperatorconfigPath                 string
	stepConfigPath                       string
	prowconfig                           configflagutil.ConfigOptions
	kubeconfig                           string
	leaderElectionSuffix                 string
	enabledControllers                   flagutil.Strings
	enabledControllersSet                sets.String
	registryClusterName                  string
	dryRun                               bool
	blockProfileRate                     time.Duration
	testImagesDistributorOptions         testImagesDistributorOptions
	serviceAccountSecretRefresherOptions serviceAccountSecretRefresherOptions
	imagePusherOptions                   imagePusherOptions
	*flagutil.GitHubOptions
}

func (o *options) addDefaults() {
	o.enabledControllers = flagutil.NewStrings(promotionreconciler.ControllerName, testimagesdistributor.ControllerName)
}

type testImagesDistributorOptions struct {
	additionalImageStreamTagsRaw       flagutil.Strings
	additionalImageStreamTags          sets.String
	additionalImageStreamsRaw          flagutil.Strings
	additionalImageStreams             sets.String
	additionalImageStreamNamespacesRaw flagutil.Strings
	additionalImageStreamNamespaces    sets.String
	forbiddenRegistriesRaw             flagutil.Strings
	forbiddenRegistries                sets.String
}

type imagePusherOptions struct {
	imageStreamsRaw flagutil.Strings
	imageStreams    sets.String
}

type serviceAccountSecretRefresherOptions struct {
	enabledNamespaces flagutil.Strings
	removeOldSecrets  bool
}

func newOpts() (*options, error) {
	opts := &options{GitHubOptions: &flagutil.GitHubOptions{}}
	opts.addDefaults()
	opts.GitHubOptions.AddFlags(flag.CommandLine)
	opts.GitHubOptions.AllowAnonymous = true
	opts.prowconfig.AddFlags(flag.CommandLine)
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
	flag.StringVar(&opts.leaderElectionSuffix, "leader-election-suffix", "", "Suffix for the leader election lock. Useful for local testing. If set, --dry-run must be set as well")
	flag.Var(&opts.enabledControllers, "enable-controller", fmt.Sprintf("Enabled controllers. Available controllers are: %v. Can be specified multiple times. Defaults to %v", allControllers.List(), opts.enabledControllers.Strings()))
	flag.Var(&opts.testImagesDistributorOptions.additionalImageStreamTagsRaw, "testImagesDistributorOptions.additional-image-stream-tag", "An imagestreamtag that will be distributed even if no test explicitly references it. It must be in namespace/name:tag format (e.G `ci/clonerefs:latest`). Can be passed multiple times.")
	flag.Var(&opts.testImagesDistributorOptions.additionalImageStreamsRaw, "testImagesDistributorOptions.additional-image-stream", "An imagestream that will be distributed even if no test explicitly references it. It must be in namespace/name format (e.G `ci/clonerefs`). Can be passed multiple times.")
	flag.Var(&opts.testImagesDistributorOptions.additionalImageStreamNamespacesRaw, "testImagesDistributorOptions.additional-image-stream-namespace", "A namespace in which imagestreams will be distributed even if no test explicitly references them (e.G `ci`). Can be passed multiple times.")
	flag.Var(&opts.testImagesDistributorOptions.forbiddenRegistriesRaw, "testImagesDistributorOptions.forbidden-registry", "The hostname of an image registry from which there is no synchronization of its images. Can be passed multiple times.")
	flag.DurationVar(&opts.blockProfileRate, "block-profile-rate", time.Duration(0), "The block profile rate. Set to non-zero to enable.")
	flag.StringVar(&opts.registryClusterName, "registry-cluster-name", "app.ci", "the cluster name on which the CI central registry is running")
	flag.Var(&opts.serviceAccountSecretRefresherOptions.enabledNamespaces, "serviceAccountRefresherOptions.enabled-namespace", "A namespace for which the serviceaccount_secret_refresher should be enabled. Can be passed multiple times.")
	flag.BoolVar(&opts.serviceAccountSecretRefresherOptions.removeOldSecrets, "serviceAccountRefresherOptions.remove-old-secrets", false, "whether the serviceaccountsecretrefresher should delete secrets older than 30 days")
	flag.Var(&opts.imagePusherOptions.imageStreamsRaw, "imagePusherOptions.image-stream", "An imagestream that will be synced. It must be in namespace/name format (e.G `ci/clonerefs`). Can be passed multiple times.")
	flag.BoolVar(&opts.dryRun, "dry-run", true, "Whether to run the controller-manager with dry-run")
	flag.Parse()

	var errs []error
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
		opts.enabledControllersSet = sets.NewString(vals...)
		if diff := opts.enabledControllersSet.Difference(allControllers); len(diff.UnsortedList()) > 0 {
			errs = append(errs, fmt.Errorf("the following controllers are unknown but were disabled via --disable-controller: %v", diff.List()))
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

	imagePusherImageStreams, isErrors := completeImageStream("uniRegistrySyncerOptions.image-stream", opts.imagePusherOptions.imageStreamsRaw)
	errs = append(errs, isErrors...)
	opts.imagePusherOptions.imageStreams = imagePusherImageStreams

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

	return opts, utilerrors.NewAggregate(errs)
}

func completeImageStreamTags(name string, raw flagutil.Strings) (sets.String, []error) {
	isTags := sets.String{}
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

func completeImageStream(name string, raw flagutil.Strings) (sets.String, []error) {
	imageStreams := sets.String{}
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

func completeSet(raw flagutil.Strings) sets.String {
	result := sets.String{}
	if vals := raw.Strings(); len(vals) > 0 {
		for _, val := range vals {
			result.Insert(val)
		}
	}
	return result
}

func main() {
	logrusutil.ComponentInit()
	controllerruntime.SetLogger(logrusr.NewLogger(logrus.StandardLogger()))

	opts, err := newOpts()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get options")
	}
	if val := int(opts.blockProfileRate.Nanoseconds()); val != 0 {
		logrus.WithField("rate", opts.blockProfileRate.String()).Info("Setting block profile rate")
		runtime.SetBlockProfileRate(val)
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
	if _, hasAppCi := kubeconfigs[appCIContextName]; !hasAppCi {
		kubeconfigs[appCIContextName], err = rest.InClusterConfig()
		if err != nil {
			logrus.WithError(err).Fatalf("--kubeconfig had no context for '%s' and loading InClusterConfig failed", appCIContextName)
		}
		logrus.Infof("Loaded %q context from in-cluster config", appCIContextName)
		if err := util.WatchFiles([]string{"/var/run/secrets/kubernetes.io/serviceaccount/token"}, kubeconfigChangedCallBack); err != nil {
			logrus.WithError(err).Fatal("faild to watch in-cluster token")
		}
	}

	if _, hasRegistryCluster := kubeconfigs[opts.registryClusterName]; !hasRegistryCluster {
		logrus.Fatalf("--kubeconfig must include a context named `%s`", opts.registryClusterName)
	}

	ciOPConfigAgent, err := agents.NewConfigAgent(opts.ciOperatorconfigPath)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct ci-operator config agent")
	}
	configAgent, err := opts.prowconfig.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to start config agent")
	}

	allManagers := map[string]controllerruntime.Manager{}
	allClustersExceptRegistryCluster := map[string]controllerruntime.Manager{}
	var registryMgr controllerruntime.Manager

	var errs []error
	for cluster, cfg := range kubeconfigs {
		if _, alreadyExists := allManagers[cluster]; alreadyExists {
			logrus.Fatalf("attempted duplicate creation of manager for cluster %s", cluster)
		}

		options := controllerruntime.Options{
			DryRunClient: opts.dryRun,
			Logger:       ctrlruntimelog.NullLogger{},
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
		mgr, err := controllerruntime.NewManager(cfg, options)
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

	var secretPaths []string
	if opts.GitHubOptions.TokenPath != "" {
		secretPaths = append(secretPaths, opts.GitHubOptions.TokenPath)
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
		if err := gitHubClient.Throttle(600, 300); err != nil {
			logrus.WithError(err).Fatal("Failed to throttle")
		}
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

	if opts.enabledControllersSet.Has(testimagesdistributor.ControllerName) {
		if err := controllerutil.RegisterMetrics(); err != nil {
			logrus.WithError(err).Fatal("failed to register metrics")
		}
	}

	if opts.enabledControllersSet.Has(testimagesdistributor.ControllerName) {
		registryConfigAgent, err := agents.NewRegistryAgent(opts.stepConfigPath)
		if err != nil {
			logrus.WithError(err).Fatal("failed to construct registryAgent")
		}

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
		); err != nil {
			logrus.WithError(err).Fatal("failed to add testimagesdistributor")
		}
	}

	if opts.enabledControllersSet.Has(serviceaccountsecretrefresher.ControllerName) {
		for clusterName, clusterMgr := range allManagers {
			if err := serviceaccountsecretrefresher.AddToManager(clusterName, clusterMgr, opts.serviceAccountSecretRefresherOptions.enabledNamespaces.StringSet(), opts.serviceAccountSecretRefresherOptions.removeOldSecrets); err != nil {
				logrus.WithError(err).Fatalf("Failed to add the %s controller to the %s cluster", serviceaccountsecretrefresher.ControllerName, clusterName)
			}
		}
	}

	if opts.enabledControllersSet.Has(testimagestreamimportcleaner.ControllerName) {
		if err := testimagestreamimportcleaner.AddToManager(mgr, allManagers); err != nil {
			logrus.WithError(err).Fatal("Failed to construct the testimagestreamimportcleaner controller")
		}
	}

	if opts.enabledControllersSet.Has(promotionnamespacereconciler.ControllerName) {
		if err := promotionnamespacereconciler.AddToManager(mgr, ciOPConfigAgent); err != nil {
			logrus.WithError(err).Fatalf("Failed to construct the %s controller", promotionnamespacereconciler.ControllerName)
		}
	}

	if err := mgr.Start(ctx); err != nil {
		logrus.WithError(err).Fatal("Manager ended with error")
	}

	logrus.Info("Process ended gracefully")
}
