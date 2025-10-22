package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"

	prowConfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/metrics"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/pkg/results"
)

var (
	errorRate = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ci_operator_error_rate",
			Help: "number of errors, sorted by label/type",
		},
		[]string{"job_name", "type", "state", "reason", "cluster"},
	)
	podScalerHighResourceCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pod_scaler_admission_high_determined_resource",
			Help: "number of times pod-scaler determined higher resource amount than what was configured, sorted by label/type",
		},
		[]string{"workload_name", "workload_type", "configured_amount", "determined_amount", "resource_type"},
	)
)

func init() {
	prometheus.MustRegister(errorRate, podScalerHighResourceCounter)
}

type options struct {
	logLevel    string
	address     string
	gracePeriod time.Duration
	passwdFile  string
}

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.address, "address", ":8080", "Address to run server on")
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	fs.StringVar(&o.passwdFile, "passwd-file", "", "Authenticate against a file. Each line of the file is with the form `<username>:<password>`.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func validateOptions(o options) error {
	_, err := log.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	if o.passwdFile == "" {
		return errors.New("--passwd-file must be specified")
	}
	return nil
}

func validateRequest(request *results.Request) error {
	if request.Reason == "" {
		return fmt.Errorf("reason field in request is empty")
	}
	if request.JobName == "" {
		return fmt.Errorf("job_name field in request is empty")
	}
	if request.State == "" {
		return fmt.Errorf("state field in request is empty")
	}
	if request.Type == "" {
		return fmt.Errorf("type field in request is empty")
	}
	if request.Cluster == "" {
		return fmt.Errorf("cluster field in request is empty")
	}
	return nil
}

func validatePodScalerRequest(request *results.PodScalerRequest) error {
	if request.WorkloadName == "" {
		return fmt.Errorf("workload_name field in request is empty")
	}
	if request.WorkloadType == "" {
		return fmt.Errorf("workload_type field in request is empty")
	}
	if request.ConfiguredAmount == "" {
		return fmt.Errorf("configured_amount field in request is empty")
	}
	if request.DeterminedAmount == "" {
		return fmt.Errorf("determined_amount field in request is empty")
	}
	if request.ResourceType == "" {
		return fmt.Errorf("resource_type field in request is empty")
	}
	return nil
}

func handleError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprint(w, html.EscapeString(err.Error()))
}

func withErrorRate(request *results.Request) {
	labels := prometheus.Labels{
		"job_name": request.JobName,
		"type":     request.Type,
		"state":    request.State,
		"reason":   request.Reason,
		"cluster":  request.Cluster,
	}
	errorRate.With(labels).Inc()
}

func recordHighResource(request *results.PodScalerRequest) {
	labels := prometheus.Labels{
		"workload_name":     request.WorkloadName,
		"workload_type":     request.WorkloadType,
		"configured_amount": request.ConfiguredAmount,
		"determined_amount": request.DeterminedAmount,
		"resource_type":     request.ResourceType,
	}
	podScalerHighResourceCounter.With(labels).Inc()
}

type validator interface {
	Validate(username, password string) bool
}

func loginHandler(validator validator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || !validator.Validate(user, pass) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleCIOperatorResult() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		bytes, err := io.ReadAll(r.Body)
		if err != nil {
			handleError(w, fmt.Errorf("unable to ready request body: %w", err))
			return
		}

		request := &results.Request{}
		if err := json.Unmarshal(bytes, request); err != nil {
			handleError(w, fmt.Errorf("unable to decode request body: %w", err))
			return
		}

		if err := validateRequest(request); err != nil {
			handleError(w, err)
			return
		}

		withErrorRate(request)

		w.WriteHeader(http.StatusOK)

		log.WithFields(log.Fields{"request": request, "duration": time.Since(start).String()}).Info("Request processed")
	}
}

func handlePodScalerResult() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		bytes, err := io.ReadAll(r.Body)
		if err != nil {
			handleError(w, fmt.Errorf("unable to read pod-scaler request body: %w", err))
			return
		}

		request := &results.PodScalerRequest{}
		if err = json.Unmarshal(bytes, request); err != nil {
			handleError(w, fmt.Errorf("unable to decode pod-scaler request body: %w", err))
			return
		}

		if err := validatePodScalerRequest(request); err != nil {
			handleError(w, err)
			return
		}

		recordHighResource(request)
		w.WriteHeader(http.StatusOK)
		log.WithFields(log.Fields{"request": request, "duration": time.Since(start).String()}).Info("Pod-scaler request processed")
	}
}

func main() {
	o, err := gatherOptions()
	if err != nil {
		log.WithError(err).Fatal("failed to gather options")
	}
	if err := validateOptions(o); err != nil {
		log.Fatalf("invalid options: %v", err)
	}

	level, _ := log.ParseLevel(o.logLevel)
	log.SetLevel(level)
	logrusutil.ComponentInit()
	health := pjutil.NewHealth()

	http.HandleFunc("/", http.NotFound)

	validator := &multi{delegates: []validator{&passwdFile{file: o.passwdFile}}}

	http.Handle("/result", loginHandler(validator, handleCIOperatorResult()))
	http.Handle("/pod-scaler", loginHandler(validator, handlePodScalerResult()))

	metrics.ExposeMetrics("result-aggregator", prowConfig.PushGateway{}, flagutil.DefaultMetricsPort)

	interrupts.ListenAndServe(&http.Server{Addr: o.address}, o.gracePeriod)
	health.ServeReady()
	interrupts.WaitForGracefulShutdown()
}
