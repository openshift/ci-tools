package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowConfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/metrics"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/prow/pkg/simplifypath"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api/configresolver"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/html"
	"github.com/openshift/ci-tools/pkg/load/agents"
	registryserver "github.com/openshift/ci-tools/pkg/registry/server"
	"github.com/openshift/ci-tools/pkg/util"
	"github.com/openshift/ci-tools/pkg/webreg"
)

type options struct {
	configPath             string
	registryPath           string
	logLevel               string
	address                string
	releaseRepoGitSyncPath string
	port                   int
	uiAddress              string
	uiPort                 int
	gracePeriod            time.Duration
	validateOnly           bool
	flatRegistry           bool
	instrumentationOptions flagutil.InstrumentationOptions
}

var (
	configresolverMetrics = metrics.NewMetrics("configresolver")
)

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.configPath, "config", "", "Path to config dirs")
	fs.StringVar(&o.registryPath, "registry", "", "Path to registry dirs")
	fs.StringVar(&o.releaseRepoGitSyncPath, "release-repo-git-sync-path", "", "Path to release repository dir")
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.address, "address", ":8080", "DEPRECATED: Address to run server on")
	fs.StringVar(&o.uiAddress, "ui-address", ":8082", "DEPRECATED: Address to run the registry UI on")
	fs.IntVar(&o.port, "port", 8080, "Port to run server on")
	fs.IntVar(&o.uiPort, "ui-port", 8082, "Port to run the registry UI on")
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	_ = fs.Duration("cycle", time.Minute*2, "Legacy flag kept for compatibility. Does nothing")
	fs.BoolVar(&o.validateOnly, "validate-only", false, "Load the config and registry, validate them and exit.")
	fs.BoolVar(&o.flatRegistry, "flat-registry", false, "Disable directory structure based registry validation")
	o.instrumentationOptions.AddFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func validateOptions(o *options) error {
	_, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}

	if o.releaseRepoGitSyncPath != "" && (o.configPath != "" || o.registryPath != "") {
		return fmt.Errorf("--release-repo-path is mutually exclusive with --config and --registry")
	}

	if o.releaseRepoGitSyncPath == "" {
		if o.configPath == "" {
			return fmt.Errorf("--config is required")
		}

		if _, err := os.Stat(o.configPath); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("--config points to a nonexistent directory: %w", err)
			}
			return fmt.Errorf("error getting stat info for --config directory: %w", err)
		}

		if o.registryPath == "" {
			return fmt.Errorf("--registry is required")
		}

		if _, err := os.Stat(o.registryPath); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("--registry points to a nonexistent directory: %w", err)
			}
			return fmt.Errorf("error getting stat info for --registry directory: %w", err)
		}
	} else {
		if _, err := os.Stat(o.releaseRepoGitSyncPath); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("--release-repo-path points to a nonexistent directory: %w", err)
			}
			return fmt.Errorf("error getting stat info for --release-repo-path directory: %w", err)
		}

		o.configPath = filepath.Join(o.releaseRepoGitSyncPath, config.CiopConfigInRepoPath)
		o.registryPath = filepath.Join(o.releaseRepoGitSyncPath, config.RegistryPath)
	}

	if o.validateOnly && o.flatRegistry {
		return errors.New("--validate-only and --flat-registry flags cannot be set simultaneously")
	}
	return o.instrumentationOptions.Validate(false)
}

func getConfigGeneration(agent agents.ConfigAgent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%d", agent.GetGeneration())
	}
}

func getRegistryGeneration(agent agents.RegistryAgent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%d", agent.GetGeneration())
	}
}

type memoryCache struct {
	Client                 ctrlruntimeclient.Client
	IntegratedStreamsMutex sync.Mutex
	IntegratedStreams      map[string]integratedStreamRecord
	CacheDuration          time.Duration
}

func (c *memoryCache) Get(ctx context.Context, ns, name string) (*configresolver.IntegratedStream, error) {
	key := fmt.Sprintf("%s/%s", ns, name)
	if c.IntegratedStreams == nil {
		c.IntegratedStreams = map[string]integratedStreamRecord{}
	}
	if r, ok := c.IntegratedStreams[key]; ok {
		if time.Now().Before(r.LastUpdatedAt.Add(c.CacheDuration)) {
			logrus.WithField("namespace", ns).WithField("name", name).Debug("Getting info for integrated stream from cache")
			return r.Stream, nil
		}
	}
	c.IntegratedStreamsMutex.Lock()
	defer c.IntegratedStreamsMutex.Unlock()
	s, err := configresolver.LocalIntegratedStream(ctx, c.Client, ns, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get information on image stream %s/%s: %w", ns, name, err)
	}
	c.IntegratedStreams[key] = integratedStreamRecord{Stream: s, LastUpdatedAt: time.Now()}
	return s, nil
}

type integratedStreamRecord struct {
	Stream        *configresolver.IntegratedStream
	LastUpdatedAt time.Time
}

type IntegratedStreamGetter interface {
	Get(ctx context.Context, ns, name string) (*configresolver.IntegratedStream, error)
}

func getIntegratedStream(ctx context.Context, g IntegratedStreamGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ns := q.Get("namespace")
		name := q.Get("name")
		if err := validateStream(ns, name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		stream, err := g.Get(ctx, ns, name)
		if err != nil {
			logrus.WithError(err).WithField("namespace", ns).WithField("name", name).Error("failed to get information of integrated stream")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(&stream); err != nil {
			logrus.WithError(err).WithField("namespace", ns).WithField("name", name).Error("failed to encode data")
		}
	}
}

var validStreams = []*regexp.Regexp{
	// https://issues.redhat.com/browse/DPTP-4005
	// There are some upgrade tests using the 4.1 or 4.3 stream in ocp
	regexp.MustCompile(`^ocp/(4\.(1[2-9]|2\d)|5\.\d+)`),
	regexp.MustCompile(`^origin/(4\.(1[2-9]|2\d)|5\.\d+)`),
	regexp.MustCompile(`^origin/scos-(4\.(1[2-9]|2\d)|5\.\d+)`),
	regexp.MustCompile(`^ocp-private/(4\.(1[2-9]|2\d)|5\.\d+)`),
	regexp.MustCompile(`^origin/sriov-(4\.(1[2-9]|2\d)|5\.\d+)`),
	regexp.MustCompile(`^origin/metallb-(4\.(1[2-9]|2\d)|5\.\d+)`),
	regexp.MustCompile(`^origin/ptp-(4\.(1[2-9]|2\d)|5\.\d+)`),
}

func validateStream(ns string, name string) error {
	if ns == "" {
		return errors.New("namespace cannot be empty")
	}
	if name == "" {
		return errors.New("name cannot be empty")
	}
	is := fmt.Sprintf("%s/%s", ns, name)
	for _, re := range validStreams {
		if re.MatchString(is) {
			return nil
		}
	}
	return fmt.Errorf("not a valid integrated stream: %s", is)
}

// l and v keep the tree legible
func l(fragment string, children ...simplifypath.Node) simplifypath.Node {
	return simplifypath.L(fragment, children...)
}

func addSchemes() error {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add imagev1 to scheme: %w", err)
	}
	return nil
}

func main() {
	logrusutil.ComponentInit()
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed go gather options")
	}
	if err := validateOptions(&o); err != nil {
		logrus.Fatalf("invalid options: %v", err)
	}
	if err := addSchemes(); err != nil {
		logrus.WithError(err).Fatal("failed to add schemes")
	}
	level, _ := logrus.ParseLevel(o.logLevel)
	logrus.SetLevel(level)

	configAgentOption := func(*agents.ConfigAgentOptions) {}
	registryAgentOption := func(*agents.RegistryAgentOptions) {}
	if o.releaseRepoGitSyncPath != "" {
		eventCh := make(chan fsnotify.Event)
		errCh := make(chan error)

		universalSymlinkWatcher := &agents.UniversalSymlinkWatcher{
			EventCh:   eventCh,
			ErrCh:     errCh,
			WatchPath: o.releaseRepoGitSyncPath,
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
	configAgent, err := agents.NewConfigAgent(o.configPath, configErrCh, agents.WithConfigMetrics(configresolverMetrics.ErrorRate), configAgentOption)
	if err != nil {
		logrus.Fatalf("Failed to get config agent: %v", err)
	}
	go func() { logrus.Fatal(<-configErrCh) }()

	registryErrCh := make(chan error)
	registryAgent, err := agents.NewRegistryAgent(o.registryPath, registryErrCh, agents.WithRegistryMetrics(configresolverMetrics.ErrorRate), agents.WithRegistryFlat(o.flatRegistry), registryAgentOption)
	if err != nil {
		logrus.Fatalf("Failed to get registry agent: %v", err)
	}
	go func() { logrus.Fatal(<-registryErrCh) }()

	inClusterConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load in-cluster config")
	}
	ocClient, err := ctrlruntimeclient.New(inClusterConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create oc client")
	}

	if o.validateOnly {
		os.Exit(0)
	}
	static, err := fs.Sub(html.StaticFS, html.StaticSubdir)
	if err != nil {
		logrus.WithError(err).Fatal("failed to open static subdirectory")
	}
	health := pjutil.NewHealthOnPort(o.instrumentationOptions.HealthPort)
	metrics.ExposeMetrics("ci-operator-configresolver", prowConfig.PushGateway{}, flagutil.DefaultMetricsPort)
	simplifier := simplifypath.NewSimplifier(l("", // shadow element mimicing the root
		l("config"),
		l("resolve"),
		l("clusterProfile"),
		l("configGeneration"),
		l("registryGeneration"),
		l("integratedStream"),
	))

	uisimplifier := simplifypath.NewSimplifier(l("", // shadow element mimicing the root
		l(""),
		l("search"),
		l("job"),
		l("reference"),
		l("chain"),
		l("workflow"),
	))
	handler := metrics.TraceHandler(simplifier, configresolverMetrics.HTTPRequestDuration, configresolverMetrics.HTTPResponseSize)
	uihandler := metrics.TraceHandler(uisimplifier, configresolverMetrics.HTTPRequestDuration, configresolverMetrics.HTTPResponseSize)
	// add handler func for incorrect paths as well; can help with identifying errors/404s caused by incorrect paths
	http.HandleFunc("/", handler(http.HandlerFunc(http.NotFound)).ServeHTTP)
	http.HandleFunc("/config", handler(registryserver.ResolveConfig(configAgent, registryAgent, configresolverMetrics)).ServeHTTP)
	http.HandleFunc("/mergeConfigsWithInjectedTest", handler(registryserver.ResolveAndMergeConfigsAndInjectTest(configAgent, registryAgent, configresolverMetrics)).ServeHTTP)
	http.HandleFunc("/resolve", handler(registryserver.ResolveLiteralConfig(registryAgent, configresolverMetrics)).ServeHTTP)
	http.HandleFunc("/clusterProfile", handler(registryserver.ResolveClusterProfile(registryAgent, configresolverMetrics)).ServeHTTP)
	http.HandleFunc("/configGeneration", handler(getConfigGeneration(configAgent)).ServeHTTP)
	http.HandleFunc("/registryGeneration", handler(getRegistryGeneration(registryAgent)).ServeHTTP)
	cache := memoryCache{Client: ocClient, CacheDuration: time.Minute}
	http.HandleFunc("/integratedStream", handler(getIntegratedStream(context.Background(), &cache)).ServeHTTP)
	http.HandleFunc("/readyz", func(_ http.ResponseWriter, _ *http.Request) {})
	interrupts.ListenAndServe(&http.Server{Addr: ":" + strconv.Itoa(o.port)}, o.gracePeriod)
	uiMux := http.NewServeMux()
	uiMux.HandleFunc(html.StaticURL, handler(http.StripPrefix(html.StaticURL, http.FileServer(http.FS(static)))).ServeHTTP)
	uiMux.Handle("/", uihandler(webreg.WebRegHandler(registryAgent, configAgent)))
	uiServer := &http.Server{
		Addr:    ":" + strconv.Itoa(o.uiPort),
		Handler: uiMux,
	}
	interrupts.ListenAndServe(uiServer, o.gracePeriod)
	health.ServeReady(func() bool {
		resp, err := http.DefaultClient.Get("http://127.0.0.1:" + strconv.Itoa(o.port) + "/readyz")
		if resp != nil {
			resp.Body.Close()
		}
		return err == nil && resp.StatusCode == 200
	})
	interrupts.WaitForGracefulShutdown()
}
