package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/bugzilla"
	prowConfig "k8s.io/test-infra/prow/config"
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
	configPath   string
	registryPath string
	prowPath     string
	jobPath      string
	logLevel     string
	address      string
	uiAddress    string
	gracePeriod  time.Duration
	cycle        time.Duration
	validateOnly bool
	flatRegistry bool
}

type traceResponseWriter struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func handleWithMetrics(h http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := time.Now()
		// Initialize the status to 200 in case WriteHeader is not called
		trw := &traceResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		h(trw, r)
		latency := time.Since(t)
		labels := prometheus.Labels{"status": strconv.Itoa(trw.statusCode), "path": r.URL.EscapedPath()}
		bzbpMetrics.httpRequestDuration.With(labels).Observe(latency.Seconds())
		bzbpMetrics.httpResponseSize.With(labels).Observe(float64(trw.size))
	})
}

// Needs additional work
func getAuthToken() []byte {
	// Auth token commented out for security reasons
	// Will modify function to use serviceAgent
	return []byte("")
}

// Handler returns a function which populates the response with the details of the bug
// Expects a POST request with field ID
// Returns bug details in JSON format
func getBugHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusNotImplemented)
			w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
			return
		}

		encoded, err := ioutil.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Could not read bug ID from request body."))
			return
		}
		client := bugzilla.NewClient(getAuthToken, "https://bugzilla.redhat.com/")
		// Reusing the bug struct from bugzilla namespace
		bug := bugzilla.Bug{}
		if err = json.Unmarshal(encoded, &bug); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Could not parse request body as unresolved config."))
			return
		}
		bugInfo, err := client.GetBug(bug.ID)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Bug ID not found"))
			return
		}
		jsonBugInfo, err := json.MarshalIndent(*bugInfo, "", "  ")
		if err != nil {
			recordError("failed to marshal bugInfo")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "failed to marshal bugInfo to JSON: %v", err)
			// logger.WithError(err).Errorf("failed to marshal bugInfo to JSON")
			return
		}
		fmt.Println(bugInfo)
		w.WriteHeader(http.StatusOK)
		w.Write(jsonBugInfo)
	}
}

func genericHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(http.StatusText(http.StatusNotFound)))
	}
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.address, "address", ":8080", "Address to run server on")
	fs.StringVar(&o.uiAddress, "ui-address", ":8082", "Address to run the registry UI on")
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	fs.Parse(os.Args[1:])
	return o
}

func validateOptions(o options) error {
	_, err := log.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %v", err)
	}
	return nil
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
	metrics.ExposeMetrics("ci-operator-bugzilla-backporter", prowConfig.PushGateway{})

	http.HandleFunc("/", handleWithMetrics(genericHandler()))
	http.HandleFunc("/getbug", handleWithMetrics(getBugHandler()))
	interrupts.ListenAndServe(&http.Server{Addr: o.address}, o.gracePeriod)

	health.ServeReady()
	interrupts.WaitForGracefulShutdown()
}
