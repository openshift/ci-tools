package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/openshift/ci-tools/pkg/api"

	"github.com/sirupsen/logrus"
	prowConfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/simplifypath"

	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/webreg"
)

type options struct {
	configPath   string
	registryPath string
	prowPath     string
	jobPath      string
	logLevel     string
	address      string
	uiAddress    string
	gracePeriod  time.Duration
	validateOnly bool
	flatRegistry bool
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
	fs.StringVar(&o.address, "address", ":8080", "Address to run server on")
	fs.StringVar(&o.uiAddress, "ui-address", ":8082", "Address to run the registry UI on")
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	_ = fs.Duration("cycle", time.Minute*2, "Legacy flag kept for compatibility. Does nothing")
	fs.BoolVar(&o.validateOnly, "validate-only", false, "Load the config and registry, validate them and exit.")
	fs.BoolVar(&o.flatRegistry, "flat-registry", false, "Disable directory structure based registry validation")
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
	return nil
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
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "failed to resolve config with registry: %v", err)
		logger.WithError(err).Warning("failed to resolve config with registry")
		return
	}
	writeJSON(w, logger, config)
}

func writeJSON(w http.ResponseWriter, logger *logrus.Entry, data interface{}) {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		metrics.RecordError("failed to marshal JSON", configresolverMetrics.ErrorRate)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "failed to marshal JSON: %v", err)
		logger.WithError(err).Errorf("failed to marshal JSON")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(jsonData); err != nil {
		logrus.WithError(err).Error("Failed to write response")
	}
}

func writeErrorPage(w http.ResponseWriter, pageErr error, status int) {
	w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, "%v\n", pageErr)
}

func resolveStepRegistry(regAgent agents.RegistryAgent, confAgent agents.ConfigAgent) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		trimmedPath := strings.TrimPrefix(req.URL.Path, req.URL.Host)
		// remove leading path prefix
		trimmedPath = strings.TrimPrefix(trimmedPath, "/step-registry/")
		// remove trailing slash
		trimmedPath = strings.TrimSuffix(trimmedPath, "/")
		splitURI := strings.Split(trimmedPath, "/")
		if len(splitURI) == 1 {
			switch splitURI[0] {
			//case "search":
			//	searchHandler(confAgent, w, req)
			//case "job":
			//	jobHandler(regAgent, confAgent, w, req)
			default:
				writeErrorPage(w, errors.New("Invalid path"), http.StatusNotImplemented)
			}
			return
		} else if len(splitURI) == 2 {
			switch splitURI[0] {
			case "reference":
				referenceHandler(regAgent, w, req)
				return
			//case "chain":
			//	chainHandler(regAgent, w, req)
			//	return
			//case "workflow":
			//	workflowHandler(regAgent, w, req)
			//	return
			default:
				writeErrorPage(w, fmt.Errorf("Component type %s not found", splitURI[0]), http.StatusNotFound)
				return
			}
		}
		writeErrorPage(w, errors.New("Invalid path"), http.StatusNotImplemented)
	}
}

func referenceHandler(agent agents.RegistryAgent, w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Since(start)) }()
	name := path.Base(req.URL.Path)

	refs, _, _, docs, metadata := agent.GetRegistryComponents()
	if _, ok := refs[name]; !ok {
		writeErrorPage(w, fmt.Errorf("Could not find reference `%s`.", name), http.StatusNotFound)
		return
	}
	refMetadataName := fmt.Sprint(name, load.RefSuffix)
	if _, ok := metadata[refMetadataName]; !ok {
		writeErrorPage(w, fmt.Errorf("Could not find metadata for file `%s`. Please contact the Developer Productivity Test Platform.", refMetadataName), http.StatusInternalServerError)
		return
	}
	ref := struct {
		Reference api.RegistryReference
		Metadata  api.RegistryInfo
	}{
		Reference: api.RegistryReference{
			LiteralTestStep: api.LiteralTestStep{
				As:       name,
				Commands: refs[name].Commands,
				From:     refs[name].From,
			},
			Documentation: docs[name],
		},
		Metadata: metadata[refMetadataName],
	}
	writeJSON(w, logrus.NewEntry(logrus.New()), ref)
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
	health := pjutil.NewHealth()
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
	http.HandleFunc("/step-registry", handler(resolveStepRegistry(registryAgent, configAgent)).ServeHTTP)
	http.HandleFunc("/configGeneration", handler(getConfigGeneration(configAgent)).ServeHTTP)
	http.HandleFunc("/registryGeneration", handler(getRegistryGeneration(registryAgent)).ServeHTTP)
	interrupts.ListenAndServe(&http.Server{Addr: o.address}, o.gracePeriod)
	uiServer := &http.Server{
		Addr:    o.uiAddress,
		Handler: uihandler(webreg.WebRegHandler(registryAgent, configAgent)),
	}
	interrupts.ListenAndServe(uiServer, o.gracePeriod)
	health.ServeReady()
	interrupts.WaitForGracefulShutdown()
}
