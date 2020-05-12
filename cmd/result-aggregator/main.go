package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	prowConfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"

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
)

func init() {
	prometheus.MustRegister(errorRate)
}

type options struct {
	logLevel    string
	address     string
	gracePeriod time.Duration
	username    string
	password    string
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.address, "address", ":8080", "Address to run server on")
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	flag.StringVar(&o.username, "username", "", "Username to trust for clients.")
	flag.StringVar(&o.password, "password-file", "", "File holding the password for clients.")
	fs.Parse(os.Args[1:])
	return o
}

func validateOptions(o options) error {
	_, err := log.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %v", err)
	}
	if o.username == "" {
		return errors.New("--username is required")
	}
	if o.password == "" {
		return errors.New("--password-file is required")
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

func handleError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprint(w, err)
}

func genericHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Debug("got invalid request")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(http.StatusText(http.StatusNotFound)))
	}
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

func handleCIOperatorResult(username string, password func() []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		user, pass, ok := r.BasicAuth()
		if !ok || user != username || pass != string(password()) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, "Unauthorized")
			return
		}

		bytes, err := ioutil.ReadAll(r.Body)
		if err != nil {
			handleError(w, fmt.Errorf("unable to ready request body: %v", err))
			return
		}

		request := &results.Request{}
		if err := json.Unmarshal(bytes, request); err != nil {
			handleError(w, fmt.Errorf("unable to decode request body: %v", err))
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

func main() {
	o := gatherOptions()
	err := validateOptions(o)
	if err != nil {
		log.Fatalf("invalid options: %v", err)
	}

	level, _ := log.ParseLevel(o.logLevel)
	log.SetLevel(level)
	logrusutil.ComponentInit()
	health := pjutil.NewHealth()

	secretAgent := secret.Agent{}
	if err := secretAgent.Start([]string{o.password}); err != nil {
		log.WithError(err).Fatal("Could not load secrets.")
	}

	http.HandleFunc("/", genericHandler())
	http.HandleFunc("/result", handleCIOperatorResult(o.username, secretAgent.GetTokenGenerator(o.password)))
	metrics.ExposeMetrics("result-aggregator", prowConfig.PushGateway{})

	interrupts.ListenAndServe(&http.Server{Addr: o.address}, o.gracePeriod)
	health.ServeReady()
	interrupts.WaitForGracefulShutdown()
}
