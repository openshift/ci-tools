package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bombsimon/logrusr/v3"
	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/version"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	quayiociimagesdistributor "github.com/openshift/ci-tools/pkg/controller/quay_io_ci_images_distributor"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/steps/release"
	"github.com/openshift/ci-tools/pkg/util"
)

var allControllers = sets.New[string](
	quayiociimagesdistributor.ControllerName,
)

type options struct {
	leaderElectionNamespace          string
	leaderElectionSuffix             string
	enabledControllers               flagutil.Strings
	enabledControllersSet            sets.Set[string]
	dryRun                           bool
	releaseRepoGitSyncPath           string
	registryConfig                   string
	quayIOCIImagesDistributorOptions quayIOCIImagesDistributorOptions
	port                             int
	gracePeriod                      time.Duration
	onlyValidManifestV2Images        bool
	configFile                       string
	config                           *quayiociimagesdistributor.CIImagesMirrorConfig
	configMutex                      sync.Mutex
	configLastModifiedAt             time.Time
	validateConfigOnly               bool
}

type quayIOCIImagesDistributorOptions struct {
	additionalImageStreamTagsRaw       flagutil.Strings
	additionalImageStreamsRaw          flagutil.Strings
	additionalImageStreamNamespacesRaw flagutil.Strings

	ignoreImageStreamTagsRaw flagutil.Strings
}

func newOpts() *options {
	opts := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&opts.leaderElectionNamespace, "leader-election-namespace", "ci", "The namespace to use for leader election")
	fs.StringVar(&opts.leaderElectionSuffix, "leader-election-suffix", "", "Suffix for the leader election lock. Useful for local testing. If set, --dry-run must be set as well")
	fs.Var(&opts.enabledControllers, "enable-controller", fmt.Sprintf("Enabled controllers. Available controllers are: %v. Can be specified multiple times. Defaults to [].", allControllers.UnsortedList()))
	fs.BoolVar(&opts.dryRun, "dry-run", false, "Whether to run the controller-manager and the mirroring with dry-run")
	fs.StringVar(&opts.releaseRepoGitSyncPath, "release-repo-git-sync-path", "", "Path to release repository dir")
	fs.StringVar(&opts.configFile, "config", "", "Path to the config file")
	fs.StringVar(&opts.registryConfig, "registry-config", "", "Path to the file of registry credentials")
	fs.Var(&opts.quayIOCIImagesDistributorOptions.additionalImageStreamTagsRaw, "quayIOCIImagesDistributorOptions.additional-image-stream-tag", "An imagestreamtag that will be distributed even if no test explicitly references it. It must be in namespace/name:tag format (e.G `ci/clonerefs:latest`). Can be passed multiple times.")
	fs.Var(&opts.quayIOCIImagesDistributorOptions.additionalImageStreamsRaw, "quayIOCIImagesDistributorOptions.additional-image-stream", "An imagestream that will be distributed even if no test explicitly references it. It must be in namespace/name format (e.G `ci/clonerefs`). Can be passed multiple times.")
	fs.Var(&opts.quayIOCIImagesDistributorOptions.additionalImageStreamNamespacesRaw, "quayIOCIImagesDistributorOptions.additional-image-stream-namespace", "A namespace in which imagestreams will be distributed even if no test explicitly references them (e.G `ci`). Can be passed multiple times.")
	fs.Var(&opts.quayIOCIImagesDistributorOptions.ignoreImageStreamTagsRaw, "quayIOCIImagesDistributorOptions.ignore-image-stream-tag", "An imagestreamtag that will be ignored to mirror. It overrides --addition-* flags. It must be in namespace/name:tag format (e.G `ci/clonerefs:latest`). Can be passed multiple times.")
	fs.IntVar(&opts.port, "port", 8090, "Port to run the server on")
	fs.DurationVar(&opts.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	fs.BoolVar(&opts.onlyValidManifestV2Images, "only-valid-manifest-v2-images", true, "If set, source images with invalidate manifests of v2 will not be mirrored")
	fs.BoolVar(&opts.validateConfigOnly, "validate-config-only", false, "If set, only validate the config file and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse args")
	}
	return opts
}

func (o *options) loadConfig(caller string) error {
	o.configMutex.Lock()
	defer o.configMutex.Unlock()
	now := time.Now()
	if o.configFile == "" {
		return fmt.Errorf("cannot load config when the config file is empty")
	}
	if o.config != nil && now.Before(o.configLastModifiedAt.Add(30*time.Minute)) {
		logrus.WithField("caller", caller).WithField("lastModifiedAt", o.configLastModifiedAt).Info("Skip loading config as it was loaded recently")
		return nil
	}
	logrus.WithField("caller", caller).Info("Loading config ...")
	c, err := quayiociimagesdistributor.LoadConfigFromFile(o.configFile)
	if err != nil {
		return fmt.Errorf("failed to load config file %s: %w", o.configFile, err)
	}
	o.config = c
	o.configLastModifiedAt = now
	return nil
}

func (o *options) validate() error {
	var errs []error
	if o.leaderElectionNamespace == "" && !o.validateConfigOnly {
		errs = append(errs, errors.New("--leader-election-namespace must be set"))
	}
	if o.leaderElectionSuffix != "" && !o.dryRun {
		errs = append(errs, errors.New("dry-run must be set if --leader-election-suffix is set"))
	}
	if values := o.enabledControllers.Strings(); len(values) > 0 {
		o.enabledControllersSet = sets.New[string](values...)
		if diff := o.enabledControllersSet.Difference(allControllers); diff.Len() > 0 {
			errs = append(errs, fmt.Errorf("the following controllers are unknown: %v", diff.UnsortedList()))
		}
	}
	if o.releaseRepoGitSyncPath == "" {
		errs = append(errs, errors.New("--release-repo-git-sync-path must be set"))
	}
	if o.registryConfig == "" {
		errs = append(errs, errors.New("--registry-config must be set"))
	}
	if _, err := os.Stat(o.registryConfig); errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("file %s does not exist", o.registryConfig))
	}
	if o.configFile != "" {
		if err := o.loadConfig("validate"); err != nil {
			errs = append(errs, fmt.Errorf("failed to load config file %s: %w", o.configFile, err))
		}
	}
	return utilerrors.NewAggregate(errs)
}

type supplementalCIImagesService interface {
	Mirror(m map[string]quayiociimagesdistributor.Source) error
}

type supplementalCIImagesServiceWithMirrorStore struct {
	mirrorStore quayiociimagesdistributor.MirrorStore
	logger      *logrus.Entry
}

func targetToQuayImage(target string) string {
	return fmt.Sprintf("%s:%s", api.QuayOpenShiftCIRepo, strings.Replace(strings.Replace(target, "/", "_", 1), ":", "_", 1))
}

func (s *supplementalCIImagesServiceWithMirrorStore) Mirror(m map[string]quayiociimagesdistributor.Source) error {
	s.logger.Info("Mirroring supplemental CI images ...")
	for k, v := range m {
		source := v.Image
		if source == "" {
			source = fmt.Sprintf("%s/%s", api.ServiceDomainAPPCIRegistry, v.ImageStreamTagReference.ISTagName())
		}
		timeStr := time.Now().Format("20060102150405")
		if err := s.mirrorStore.Put(quayiociimagesdistributor.MirrorTask{
			Source:      targetToQuayImage(k),
			Destination: targetToQuayImage(timeStr + "_prune_" + k),
			Owner:       "supplementalCIImagesService",
		}); err != nil {
			return fmt.Errorf("failed to put mirror task: %w", err)
		}
		if err := s.mirrorStore.Put(quayiociimagesdistributor.MirrorTask{
			Source:      source,
			Destination: targetToQuayImage(k),
			Owner:       "supplementalCIImagesService",
		}); err != nil {
			return fmt.Errorf("failed to put mirror task: %w", err)
		}
	}
	return nil
}

func newSupplementalCIImagesServiceWithMirrorStore(mirrorStore quayiociimagesdistributor.MirrorStore, logger *logrus.Entry) supplementalCIImagesService {
	return &supplementalCIImagesServiceWithMirrorStore{mirrorStore: mirrorStore, logger: logger}
}

func main() {
	version.Name = "ci-images-mirror"
	logrusutil.ComponentInit()
	controllerruntime.SetLogger(logrusr.New(logrus.StandardLogger()))
	logrus.SetLevel(logrus.TraceLevel)
	opts := newOpts()
	if err := opts.validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to validate options")
	}

	ciOperatorConfigPath := filepath.Join(opts.releaseRepoGitSyncPath, config.CiopConfigInRepoPath)
	if opts.validateConfigOnly {
		configFilePathInReleaseRepo := strings.Replace(opts.configFile, opts.releaseRepoGitSyncPath, "", 1)
		err := validateConfig(opts.config, opts.registryConfig, ciOperatorConfigPath, configFilePathInReleaseRepo)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to validate config")
		}
		logrus.WithField("configFile", opts.configFile).Info("The config file is valid")
		os.Exit(0)
	}

	ctx := controllerruntime.SetupSignalHandler()
	inClusterConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load in-cluster config")
	}
	client, err := ctrlruntimeclient.New(inClusterConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create client")
	}

	clientOptions := ctrlruntimeclient.Options{}
	clientOptions.DryRun = &opts.dryRun
	mgr, err := controllerruntime.NewManager(inClusterConfig, controllerruntime.Options{
		Client:                        clientOptions,
		LeaderElection:                true,
		LeaderElectionReleaseOnCancel: true,
		LeaderElectionNamespace:       opts.leaderElectionNamespace,
		LeaderElectionID:              fmt.Sprintf("ci-image-mirror%s", opts.leaderElectionSuffix),
	})

	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct manager for the hive cluster")
	}

	if err := imagev1.AddToScheme(mgr.GetScheme()); err != nil {
		logrus.WithError(err).Fatal("Failed to add imagev1 to scheme")
	}
	// The image api is implemented via the Openshift Extension APIServer, so contrary
	// to CRD-Based resources it supports protobuf.
	if err := apiutil.AddToProtobufScheme(imagev1.AddToScheme); err != nil {
		logrus.WithError(err).Fatal("Failed to add imagev1 api to protobuf scheme")
	}

	mirrorStore := quayiociimagesdistributor.NewMirrorStore()
	if opts.configFile != "" {
		supplementalCIImagesService := newSupplementalCIImagesServiceWithMirrorStore(mirrorStore, logrus.WithField("subcomponent", "supplementalCIImagesService"))
		interrupts.TickLiteral(func() {
			if err := opts.loadConfig("supplementalCIImagesService"); err != nil {
				logrus.WithError(err).Error("Failed to reload config")
				return
			}
			if err := supplementalCIImagesService.Mirror(opts.config.SupplementalCIImages); err != nil {
				logrus.WithError(err).Error("Failed to mirror supplemental CI images")
			}
		}, time.Hour)

		artImagesService := newSupplementalCIImagesServiceWithMirrorStore(mirrorStore, logrus.WithField("subcomponent", "artImagesService"))
		interrupts.TickLiteral(func() {
			if err := opts.loadConfig("artImagesService"); err != nil {
				logrus.WithError(err).Error("Failed to reload config")
				return
			}
			m, err := quayiociimagesdistributor.ARTImages(ctx, client, opts.config.ArtImages, opts.config.IgnoredSources)
			if err != nil {
				logrus.WithError(err).Error("Failed to get ART images")
				return
			}
			if err := artImagesService.Mirror(m); err != nil {
				logrus.WithError(err).Error("Failed to mirror ART images")
			}
		}, time.Hour)
	}

	server := &http.Server{
		Addr:    ":" + strconv.Itoa(opts.port),
		Handler: getRouter(interrupts.Context(), mirrorStore),
	}
	interrupts.ListenAndServe(server, opts.gracePeriod)

	ocClientFactory := quayiociimagesdistributor.NewClientFactory()
	quayIOImageHelper, err := ocClientFactory.NewClient()
	if err != nil {
		logrus.WithError(err).Fatal("failed to create QuayIOImageHelper")
	}

	mirrorConsumerController := quayiociimagesdistributor.NewMirrorConsumer(mirrorStore, quayIOImageHelper, opts.registryConfig, opts.dryRun)
	interrupts.Run(func(ctx context.Context) { execute(ctx, mirrorConsumerController) })

	if err := quayiociimagesdistributor.RegisterMetrics(); err != nil {
		logrus.WithError(err).Fatal("failed to register metrics")
	}
	if opts.enabledControllersSet.Has(quayiociimagesdistributor.ControllerName) {
		eventCh := make(chan fsnotify.Event)
		errCh := make(chan error)
		go func() { logrus.Fatal(<-errCh) }()
		universalSymlinkWatcher := &agents.UniversalSymlinkWatcher{
			EventCh:   eventCh,
			ErrCh:     errCh,
			WatchPath: opts.releaseRepoGitSyncPath,
		}
		configAgentOption := func(opt *agents.ConfigAgentOptions) {
			opt.UniversalSymlinkWatcher = universalSymlinkWatcher
		}

		ciOPConfigAgent, err := agents.NewConfigAgent(ciOperatorConfigPath, errCh, configAgentOption)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to construct ci-operator config agent")
		}

		registryAgentOption := func(opt *agents.RegistryAgentOptions) {
			opt.UniversalSymlinkWatcher = universalSymlinkWatcher
		}
		stepConfigPath := filepath.Join(opts.releaseRepoGitSyncPath, config.RegistryPath)
		registryConfigAgent, err := agents.NewRegistryAgent(stepConfigPath, errCh, registryAgentOption)
		if err != nil {
			logrus.WithError(err).Fatal("failed to construct registryAgent")
		}

		ignoreImageStreamTags := sets.New[string](opts.quayIOCIImagesDistributorOptions.ignoreImageStreamTagsRaw.Strings()...)
		if opts.config != nil {
			for k := range opts.config.SupplementalCIImages {
				logrus.WithField("target", k).Debug("Ignore target of supplemental CI images on mirroring")
				ignoreImageStreamTags.Insert(k)
			}
		}
		logrus.WithField("tags", ignoreImageStreamTags.UnsortedList()).Infof("%s will ignore mirroring those tags", quayiociimagesdistributor.ControllerName)
		if err := quayiociimagesdistributor.AddToManager(mgr,
			ciOPConfigAgent,
			registryConfigAgent,
			sets.New[string](opts.quayIOCIImagesDistributorOptions.additionalImageStreamTagsRaw.Strings()...),
			sets.New[string](opts.quayIOCIImagesDistributorOptions.additionalImageStreamsRaw.Strings()...),
			sets.New[string](opts.quayIOCIImagesDistributorOptions.additionalImageStreamNamespacesRaw.Strings()...),
			ignoreImageStreamTags,
			quayIOImageHelper,
			mirrorStore,
			opts.registryConfig,
			opts.onlyValidManifestV2Images); err != nil {
			logrus.WithField("name", quayiociimagesdistributor.ControllerName).WithError(err).Fatal("Failed to construct the controller")
		}
	}

	if err := mgr.Start(ctx); err != nil {
		logrus.WithError(err).Fatal("Manager ended with error")
	}

	logrus.Info("Process ended gracefully")
}

func validateConfig(config *quayiociimagesdistributor.CIImagesMirrorConfig, registryConfig string, ciOperatorConfigPath string, configFilePathInReleaseRepo string) error {
	diff, err := getNewSupplementalCIImageTargets(configFilePathInReleaseRepo, config)
	if err != nil {
		logrus.WithError(err).Fatal("failed to get config diff")
	}
	errs := []error{}
	promotedTags := getPromotedTags(ciOperatorConfigPath)
	for target := range diff {
		source := config.SupplementalCIImages[target]
		if source.Image != "" {
			if strings.HasPrefix(source.Image, "docker.io/") {
				errs = append(errs, fmt.Errorf("source image %s is from docker.io", source.Image))
			}
			if !isAccessible(source.Image, registryConfig) {
				errs = append(errs, fmt.Errorf("source image %s is not accessible", source.Image))
			}
		}
		if promotedTags.Has(target) {
			errs = append(errs, fmt.Errorf("target %s would overwrite a promoted tag", target))
		}
		if isAccessible(targetToQuayImage(target), registryConfig) {
			errs = append(errs, fmt.Errorf("target %s already exists in quay", target))
		}
	}
	return utilerrors.NewAggregate(errs)
}

func getNewSupplementalCIImageTargets(configFilePathInReleaseRepo string, config *quayiociimagesdistributor.CIImagesMirrorConfig) (sets.Set[string], error) {
	bytes, err := quayiociimagesdistributor.LoadConfigFromReleaseRepo(configFilePathInReleaseRepo)
	if err != nil {
		return sets.Set[string]{}, fmt.Errorf("failed to load config from release repo: %w", err)
	}
	masterConfig, err := quayiociimagesdistributor.LoadConfig(bytes)
	if err != nil {
		return sets.Set[string]{}, fmt.Errorf("failed to load config file %s: %w", configFilePathInReleaseRepo, err)
	}

	diff := sets.New[string]()
	for k := range config.SupplementalCIImages {
		if _, ok := masterConfig.SupplementalCIImages[k]; !ok {
			diff.Insert(k)
		}
	}
	return diff, nil
}

func getPromotedTags(ciOperatorConfigPath string) sets.Set[string] {
	promotedTags := sets.New[string]()
	abs, err := filepath.Abs(ciOperatorConfigPath)
	if err != nil {
		logrus.WithError(err).Fatal("failed to determine absolute CI Operator configuration path")
	}
	err = config.OperateOnCIOperatorConfigDir(abs, func(cfg *api.ReleaseBuildConfiguration, metadata *config.Info) error {
		for _, isTagRef := range release.PromotedTags(cfg) {
			promotedTags.Insert(isTagRef.ISTagName())
		}
		return nil
	})
	if err != nil {
		logrus.WithError(err).Fatal("failed to get promoted tags")
	}
	return promotedTags
}

func isAccessible(image string, registryConfig string) bool {
	ocClientFactory := quayiociimagesdistributor.NewClientFactory()
	quayIOImageHelper, err := ocClientFactory.NewClient()
	if err != nil {
		logrus.WithError(err).Fatal("failed to create QuayIOImageHelper")
	}
	opts := quayiociimagesdistributor.OCImageInfoOptions{
		RegistryConfig: registryConfig,
		//TODO add multiarch support
		FilterByOS: "linux/amd64",
	}
	info, _ := quayIOImageHelper.ImageInfo(image, opts)
	return info.Digest != ""
}

func getRouter(_ context.Context, ms quayiociimagesdistributor.MirrorStore) *http.ServeMux {
	handler := http.NewServeMux()

	handler.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		page := map[string]bool{"ok": true}
		if err := json.NewEncoder(w).Encode(page); err != nil {
			logrus.WithError(err).WithField("page", page).Error("failed to encode page")
		}
	})

	writeRespond := func(t string, w http.ResponseWriter, r *http.Request) {
		var page any
		var err error
		switch t {
		case "mirrors":
			action := r.URL.Query().Get("action")
			if action == "" {
				action = "summarize"
			}
			limit := r.URL.Query().Get("limit")
			if limit == "" {
				limit = "1"
			}
			if lInt, err1 := strconv.Atoi(limit); err1 != nil {
				err = err1
			} else {
				page, err = mirrors(action, lInt, ms)
			}
		default:
			http.Error(w, fmt.Sprintf("Unknown type: %s", t), http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(page); err != nil {
			logrus.WithError(err).WithField("page", page).Error("failed to encode page")
		}
	}

	handler.HandleFunc("/api/v1/mirrors", func(w http.ResponseWriter, r *http.Request) {
		logrus.WithField("path", "/api/v1/mirrors").Info("serving")
		writeRespond("mirrors", w, r)
	})

	return handler
}

func mirrors(action string, n int, ms quayiociimagesdistributor.MirrorStore) (any, error) {
	switch action {
	case "show":
		mirrors, n, err := ms.Show(n)
		if err != nil {
			return nil, fmt.Errorf("failed to get mirrors: %w", err)
		}
		return map[string]any{"mirrors": mirrors, "total": n}, nil
	case "summarize":
		summarize, err := ms.Summarize()
		if err != nil {
			return nil, fmt.Errorf("failed to get all mirrors: %w", err)
		}
		return summarize, nil
	default:
		return nil, fmt.Errorf("invalid action: %s", action)
	}
}

func execute(ctx context.Context, c *quayiociimagesdistributor.MirrorConsumerController) {
	if err := c.Run(ctx); err != nil {
		logrus.WithError(err).Error("Error running")
	}
}
