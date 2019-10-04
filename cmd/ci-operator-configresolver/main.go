package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	prowConfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/load"
)

const (
	orgQuery     = "org"
	repoQuery    = "repo"
	branchQuery  = "branch"
	variantQuery = "variant"
)

type options struct {
	configPath  string
	logLevel    string
	address     string
	gracePeriod time.Duration
	cycle       time.Duration
}

type traceResponseWriter struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func (w *traceResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *traceResponseWriter) Write(data []byte) (int, error) {
	size, err := w.ResponseWriter.Write(data)
	w.size += size
	return size, err
}

var (
	configresolverMetrics = struct {
		httpRequestDuration *prometheus.HistogramVec
		httpResponseSize    *prometheus.HistogramVec
		errorRate           *prometheus.CounterVec
		configReloadTime    prometheus.Histogram
	}{
		httpRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "configresolver_http_request_duration_seconds",
				Help:    "http request duration in seconds",
				Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20},
			},
			[]string{"status"},
		),
		httpResponseSize: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "configresolver_http_response_size_bytes",
				Help:    "http response size in bytes",
				Buckets: []float64{16384, 32768, 65536, 131072, 262144, 524288, 1048576, 2097152, 4194304, 8388608, 16777216, 33554432},
			},
			[]string{"status"},
		),
		errorRate: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "configresolver_error_rate",
				Help: "number of errors, sorted by label/type",
			},
			[]string{"error"},
		),
		configReloadTime: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "configresolver_config_reload_duration_seconds",
				Help:    "config reload duration in seconds",
				Buckets: []float64{0.0005, 0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1},
			},
		),
	}
)

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.configPath, "config", "", "Path to config dirs")
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.address, "address", ":8080", "Address to run server on")
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	fs.DurationVar(&o.cycle, "cycle", time.Minute*2, "Cycle duration for config reload")
	fs.Parse(os.Args[1:])
	return o
}

func validateOptions(o options) error {
	_, err := log.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %v", err)
	}
	if o.cycle == 0 {
		return fmt.Errorf("invalid cycle: duration cannot equal 0")
	}
	return nil
}

func recordError(label string) {
	labels := prometheus.Labels{"error": label}
	configresolverMetrics.errorRate.With(labels).Inc()
}

func missingQuery(w http.ResponseWriter, field string) {
	recordError(fmt.Sprintf("query missing %s", field))
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, "%s query missing or incorrect", field)
}

func handleWithMetrics(h http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := time.Now()
		// Initialize the status to 200 in case WriteHeader is not called
		trw := &traceResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		h(trw, r)
		latency := time.Since(t)
		labels := prometheus.Labels{"status": strconv.Itoa(trw.statusCode)}
		configresolverMetrics.httpRequestDuration.With(labels).Observe(latency.Seconds())
		configresolverMetrics.httpResponseSize.With(labels).Observe(float64(trw.size))
	})
}

func resolveConfig(agent load.ConfigAgent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusNotImplemented)
			w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
			return
		}
		org := r.URL.Query().Get(orgQuery)
		if org == "" {
			missingQuery(w, orgQuery)
			return
		}
		repo := r.URL.Query().Get(repoQuery)
		if repo == "" {
			missingQuery(w, repoQuery)
			return
		}
		branch := r.URL.Query().Get(branchQuery)
		if branch == "" {
			missingQuery(w, branchQuery)
			return
		}
		variant := r.URL.Query().Get(variantQuery)
		info := config.Info{
			Org:     org,
			Repo:    repo,
			Branch:  branch,
			Variant: variant,
		}

		config, err := agent.GetConfig(info)
		if err != nil {
			recordError("config not found")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "failed to get config: %v", err)
			log.WithError(err).Warning("failed to get config")
			return
		}
		jsonConfig, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			recordError("failed to unmarshal config")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "failed to marshal config to JSON: %v", err)
			log.WithError(err).Errorf("failed to unmarshal config to JSON")
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(jsonConfig)
	}
}

func getGeneration(agent load.ConfigAgent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%d", agent.GetGeneration())
	}
}

func init() {
	prometheus.MustRegister(configresolverMetrics.httpRequestDuration)
	prometheus.MustRegister(configresolverMetrics.httpResponseSize)
	prometheus.MustRegister(configresolverMetrics.errorRate)
	prometheus.MustRegister(configresolverMetrics.configReloadTime)
}

func main() {
	o := gatherOptions()
	err := validateOptions(o)
	if err != nil {
		log.Fatalf("invalid options: %v", err)
	}
	level, _ := log.ParseLevel(o.logLevel)
	log.SetLevel(level)
	health := pjutil.NewHealth()
	metrics.ExposeMetrics("ci-operator-configresolver", prowConfig.PushGateway{})

	configAgent, err := load.NewConfigAgent(o.configPath, o.cycle, configresolverMetrics.errorRate, &configresolverMetrics.configReloadTime)
	if err != nil {
		log.Fatalf("Failed to get config agent: %v", err)
	}

	http.HandleFunc("/config", handleWithMetrics(resolveConfig(configAgent)))
	http.HandleFunc("/generation", handleWithMetrics(getGeneration(configAgent)))
	interrupts.ListenAndServe(&http.Server{Addr: o.address}, o.gracePeriod)
	health.ServeReady()
	interrupts.WaitForGracefulShutdown()
}
