package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	prowConfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/simplifypath"

	"github.com/openshift/ci-tools/pkg/html"
	"github.com/openshift/ci-tools/pkg/load/agents"
	registryserver "github.com/openshift/ci-tools/pkg/registry/server"
	"github.com/openshift/ci-tools/pkg/webreg"
)

type options struct {
	configPath             string
	registryPath           string
	logLevel               string
	address                string
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

func validateOptions(o options) error {
	_, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	if o.configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if _, err := os.Stat(o.configPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("--config points to a nonexistent directory: %w", err)
		}
		return fmt.Errorf("Error getting stat info for --config directory: %w", err)
	}
	if o.registryPath == "" {
		return fmt.Errorf("--registry is required")
	}
	if _, err := os.Stat(o.registryPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("--registry points to a nonexistent directory: %w", err)
		}
		return fmt.Errorf("Error getting stat info for --registry directory: %w", err)
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

// l and v keep the tree legible
func l(fragment string, children ...simplifypath.Node) simplifypath.Node {
	return simplifypath.L(fragment, children...)
}

func main() {
	logrusutil.ComponentInit()
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed go gather options")
	}
	if err := validateOptions(o); err != nil {
		logrus.Fatalf("invalid options: %v", err)
	}
	level, _ := logrus.ParseLevel(o.logLevel)
	logrus.SetLevel(level)

	configAgent, err := agents.NewConfigAgent(o.configPath, agents.WithConfigMetrics(configresolverMetrics.ErrorRate))
	if err != nil {
		logrus.Fatalf("Failed to get config agent: %v", err)
	}

	registryAgent, err := agents.NewRegistryAgent(o.registryPath, agents.WithRegistryMetrics(configresolverMetrics.ErrorRate), agents.WithRegistryFlat(o.flatRegistry))
	if err != nil {
		logrus.Fatalf("Failed to get registry agent: %v", err)
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
		l("configGeneration"),
		l("registryGeneration"),
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
	http.HandleFunc("/configWithInjectedTest", handler(registryserver.ResolveConfigWithInjectedTest(configAgent, registryAgent, configresolverMetrics)).ServeHTTP)
	http.HandleFunc("/resolve", handler(registryserver.ResolveLiteralConfig(registryAgent, configresolverMetrics)).ServeHTTP)
	http.HandleFunc("/configGeneration", handler(getConfigGeneration(configAgent)).ServeHTTP)
	http.HandleFunc("/registryGeneration", handler(getRegistryGeneration(registryAgent)).ServeHTTP)
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
