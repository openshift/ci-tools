package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
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

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/webreg"
)

type options struct {
	configPath             string
	registryPath           string
	prowPath               string
	jobPath                string
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
	fs.StringVar(&o.prowPath, "prow-config", "", "Path to prow config")
	fs.StringVar(&o.jobPath, "jobs", "", "Path to job config dir")
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
	if o.prowPath == "" {
		return fmt.Errorf("--prow-config is required")
	}
	if _, err := os.Stat(o.prowPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("--prow-config points to a nonexistent file: %w", err)
		}
		return fmt.Errorf("Error getting stat info for --prow-config file: %w", err)
	}
	if o.validateOnly && o.flatRegistry {
		return errors.New("--validate-only and --flat-registry flags cannot be set simultaneously")
	}
	return o.instrumentationOptions.Validate(false)
}

func resolveConfig(configAgent agents.ConfigAgent, registryAgent agents.RegistryAgent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
			return
		}
		metadata, err := webreg.MetadataFromQuery(w, r)
		if err != nil {
			metrics.RecordError("invalid query", configresolverMetrics.ErrorRate)
		}
		logger := logrus.WithFields(api.LogFieldsFor(metadata))

		config, err := configAgent.GetMatchingConfig(metadata)
		if err != nil {
			metrics.RecordError("config not found", configresolverMetrics.ErrorRate)
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "failed to get config: %v", err)
			logger.WithError(err).Warning("failed to get config")
			return
		}
		resolveAndRespond(registryAgent, config, w, logger)
	}
}

func resolveLiteralConfig(registryAgent agents.RegistryAgent) http.HandlerFunc {
	logger := logrus.NewEntry(logrus.New())
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
			return
		}

		encoded, err := ioutil.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Could not read unresolved config from request body."))
			return
		}
		unresolvedConfig := api.ReleaseBuildConfiguration{}
		if err = json.Unmarshal(encoded, &unresolvedConfig); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Could not parse request body as unresolved config."))
			return
		}
		resolveAndRespond(registryAgent, unresolvedConfig, w, logger)
	}
}

func resolveAndRespond(registryAgent agents.RegistryAgent, config api.ReleaseBuildConfiguration, w http.ResponseWriter, logger *logrus.Entry) {
	config, err := registryAgent.ResolveConfig(config)
	if err != nil {
		metrics.RecordError("failed to resolve config with registry", configresolverMetrics.ErrorRate)
		w.WriteHeader(http.StatusBadRequest)
		if _, writeErr := w.Write([]byte(fmt.Sprintf("failed to resolve config: %v", err))); writeErr != nil {
			logger.WithError(writeErr).Warning("failed to write body after config resolving failed")
		}
		fmt.Fprintf(w, "failed to resolve config with registry: %v", err)
		logger.WithError(err).Warning("failed to resolve config with registry")
		return
	}
	jsonConfig, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		metrics.RecordError("failed to marshal config", configresolverMetrics.ErrorRate)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "failed to marshal config to JSON: %v", err)
		logger.WithError(err).Errorf("failed to marshal config to JSON")
		return
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(jsonConfig); err != nil {
		logrus.WithError(err).Error("Failed to write response")
	}
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
		l("help",
			l("adding-components"),
			l("examples"),
			l("ci-operator"),
			l("leases"),
		),
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
	http.HandleFunc("/config", handler(resolveConfig(configAgent, registryAgent)).ServeHTTP)
	http.HandleFunc("/resolve", handler(resolveLiteralConfig(registryAgent)).ServeHTTP)
	http.HandleFunc("/configGeneration", handler(getConfigGeneration(configAgent)).ServeHTTP)
	http.HandleFunc("/registryGeneration", handler(getRegistryGeneration(registryAgent)).ServeHTTP)
	interrupts.ListenAndServe(&http.Server{Addr: ":" + strconv.Itoa(o.port)}, o.gracePeriod)
	uiServer := &http.Server{
		Addr:    ":" + strconv.Itoa(o.uiPort),
		Handler: uihandler(webreg.WebRegHandler(registryAgent, configAgent)),
	}
	interrupts.ListenAndServe(uiServer, o.gracePeriod)
	health.ServeReady()
	interrupts.WaitForGracefulShutdown()
}
