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
	"time"

	"github.com/bombsimon/logrusr/v3"
	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/yaml"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	quayiociimagesdistributor "github.com/openshift/ci-tools/pkg/controller/quay_io_ci_images_distributor"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/util"
	"github.com/openshift/ci-tools/pkg/util/gzip"
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
	config                           *Config
}

func (o *options) addDefaults() {
	o.enabledControllers = flagutil.NewStrings(quayiociimagesdistributor.ControllerName)
}

type quayIOCIImagesDistributorOptions struct {
	additionalImageStreamTagsRaw       flagutil.Strings
	additionalImageStreamsRaw          flagutil.Strings
	additionalImageStreamNamespacesRaw flagutil.Strings

	ignoreImageStreamTagsRaw flagutil.Strings
}

func newOpts() *options {
	opts := &options{}
	opts.addDefaults()
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&opts.leaderElectionNamespace, "leader-election-namespace", "ci", "The namespace to use for leader election")
	fs.StringVar(&opts.leaderElectionSuffix, "leader-election-suffix", "", "Suffix for the leader election lock. Useful for local testing. If set, --dry-run must be set as well")
	fs.Var(&opts.enabledControllers, "enable-controller", fmt.Sprintf("Enabled controllers. Available controllers are: %v. Can be specified multiple times. Defaults to %v", allControllers.UnsortedList(), opts.enabledControllers.Strings()))
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
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse args")
	}
	return opts
}

func (o *options) validate() error {
	var errs []error
	if o.leaderElectionNamespace == "" {
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
		if c, err := loadConfig(o.configFile); err != nil {
			errs = append(errs, fmt.Errorf("failed to load config file %s: %w", o.configFile, err))
		} else {
			o.config = c
		}
	}
	return utilerrors.NewAggregate(errs)
}

func loadConfig(file string) (*Config, error) {
	bytes, err := gzip.ReadFileMaybeGZIP(file)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := yaml.UnmarshalStrict(bytes, c); err != nil {
		return nil, err
	}
	var errs []error
	for k, v := range c.SupplementalCIImages {
		splits := strings.Split(k, "/")
		if len(splits) != 2 || splits[0] == "" || splits[1] == "" {
			errs = append(errs, fmt.Errorf("invalid target: %s", k))
		} else {
			splits = strings.Split(splits[1], ":")
			if len(splits) != 2 || splits[0] == "" || splits[1] == "" {
				errs = append(errs, fmt.Errorf("invalid target: %s", k))
			}
		}
		if v.As != "" {
			errs = append(errs, errors.New("as cannot be set"))
		}
		if v.Image == "" {
			if v.Namespace == "" {
				errs = append(errs, errors.New("namespace for the source must be set"))
			}
			if v.Name == "" {
				errs = append(errs, errors.New("name for the source must be set"))
			}
			if v.Tag == "" {
				errs = append(errs, errors.New("tag for the source must be set"))
			}
		}
	}
	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}
	return c, nil
}

type Config struct {
	SupplementalCIImages map[string]Source `json:"supplementalCIImages"`
}

type Source struct {
	api.ImageStreamTagReference `json:",inline"`
	// Image is an image that can be pulled in either form of tag or digest.
	// When image is set, Tag will be ignored.
	Image string `json:"image"`
}

type supplementalCIImagesService interface {
	Mirror(m map[string]Source) error
}

type supplementalCIImagesServiceWithMirrorStore struct {
	mirrorStore quayiociimagesdistributor.MirrorStore
	logger      *logrus.Entry
}

func (s *supplementalCIImagesServiceWithMirrorStore) Mirror(m map[string]Source) error {
	s.logger.Info("Mirroring supplemental CI images ...")
	for k, v := range m {
		source := v.Image
		if source == "" {
			source = fmt.Sprintf("%s/%s", api.ServiceDomainAPPCIRegistry, v.ImageStreamTagReference.ISTagName())
		}
		if err := s.mirrorStore.Put(quayiociimagesdistributor.MirrorTask{
			Source:      source,
			Destination: fmt.Sprintf("%s:%s", api.QuayOpenShiftCIRepo, strings.Replace(strings.Replace(k, "/", "_", 1), ":", "_", 1)),
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
	logrusutil.ComponentInit()
	controllerruntime.SetLogger(logrusr.New(logrus.StandardLogger()))
	logrus.SetLevel(logrus.TraceLevel)
	opts := newOpts()
	if err := opts.validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to validate options")
	}

	ctx := controllerruntime.SetupSignalHandler()
	inClusterConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load in-cluster config")
	}

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
	ciOperatorConfigPath := filepath.Join(opts.releaseRepoGitSyncPath, config.CiopConfigInRepoPath)

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
	if opts.config != nil {
		supplementalCIImagesService := newSupplementalCIImagesServiceWithMirrorStore(mirrorStore, logrus.WithField("subcomponent", "supplementalCIImagesService"))
		interrupts.TickLiteral(func() {
			if err := supplementalCIImagesService.Mirror(opts.config.SupplementalCIImages); err != nil {
				logrus.WithError(err).Error("Failed to mirror supplemental CI images")
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

	if opts.enabledControllersSet.Has(quayiociimagesdistributor.ControllerName) {
		if err := quayiociimagesdistributor.RegisterMetrics(); err != nil {
			logrus.WithError(err).Fatal("failed to register metrics")
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
