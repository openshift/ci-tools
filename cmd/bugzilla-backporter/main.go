package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/openshift/ci-tools/pkg/backporter"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"k8s.io/test-infra/pkg/flagutil"
	prowConfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
)

var (
	bzbpMetrics = struct {
		httpRequestDuration *prometheus.HistogramVec
		httpResponseSize    *prometheus.HistogramVec
		errorRate           *prometheus.CounterVec
	}{
		httpRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "bugzilla_backporter_http_request_duration_seconds",
				Help:    "http request duration in seconds",
				Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2},
			},
			[]string{"status", "path"},
		),
		httpResponseSize: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "bugzilla_backporter_http_response_size_bytes",
				Help:    "http response size in bytes",
				Buckets: []float64{256, 512, 1024, 2048, 4096, 6144, 8192, 10240, 12288, 16384, 24576, 32768, 40960, 49152, 57344, 65536},
			},
			[]string{"status", "path"},
		),
		errorRate: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "bugzilla_backporter_error_rate",
				Help: "number of errors, sorted by label/type",
			},
			[]string{"error"},
		),
	}
)

func recordError(label string) {
	labels := prometheus.Labels{"error": label}
	bzbpMetrics.errorRate.With(labels).Inc()
}

type options struct {
	logLevel    string
	address     string
	gracePeriod time.Duration
	bugzilla    prowflagutil.BugzillaOptions
}

type traceResponseWriter struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func handleWithMetrics(h backporter.HandlerFuncWithErrorReturn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := time.Now()
		// Initialize the status to 200 in case WriteHeader is not called
		trw := &traceResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		err := h(trw, r)
		if err != nil {
			recordError(err.Error())
		}
		latency := time.Since(t)
		labels := prometheus.Labels{"status": strconv.Itoa(trw.statusCode), "path": r.URL.EscapedPath()}
		bzbpMetrics.httpRequestDuration.With(labels).Observe(latency.Seconds())
		bzbpMetrics.httpResponseSize.With(labels).Observe(float64(trw.size))
	}
}

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.address, "address", ":8080", "Address to run server on")
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	for _, group := range []flagutil.OptionGroup{&o.bugzilla} {
		group.AddFlags(fs)
	}
	err := fs.Parse(os.Args[1:])
	if err != nil {
		return o, err
	}
	return o, nil
}

func processOptions(o options) error {
	level, err := log.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level '%s': %v", o.logLevel, err)
	}
	log.SetLevel(level)
	return nil
}

func main() {
	o, err := gatherOptions()
	if err != nil {
		log.Fatalf("invalid options: %v", err)
	}
	err = processOptions(o)
	if err != nil {
		log.Fatalf("invalid options: %v", err)
	}
	tokens := []string{o.bugzilla.ApiKeyPath}
	secretAgent := &secret.Agent{}
	if err := secretAgent.Start(tokens); err != nil {
		log.WithError(err).Fatal("Error starting secrets agent.")
	}
	bugzillaClient, err := o.bugzilla.BugzillaClient(secretAgent)
	if err != nil {
		log.WithError(err).Fatal("Error getting Bugzilla client.")
	}
	health := pjutil.NewHealth()
	metrics.ExposeMetrics("ci-operator-bugzilla-backporter", prowConfig.PushGateway{}, prowflagutil.DefaultMetricsPort)

	http.HandleFunc("/", handleWithMetrics(backporter.GetLandingHandler()))
	http.HandleFunc("/getclones", handleWithMetrics(backporter.GetClonesHandler(bugzillaClient)))
	// Leaving this in here to help with future debugging. This will return bug details in JSON format
	http.HandleFunc("/getbug", handleWithMetrics(backporter.GetBugHandler(bugzillaClient)))
	interrupts.ListenAndServe(&http.Server{Addr: o.address}, o.gracePeriod)

	health.ServeReady()
	interrupts.WaitForGracefulShutdown()
}
