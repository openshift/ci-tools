package dispatcher

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/api"
	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"
)

// PrometheusOptions exposes options used in contacting a Prometheus instance
type PrometheusOptions struct {
	PrometheusURL             string
	PrometheusUsername        string
	PrometheusPasswordPath    string
	PrometheusBearerTokenPath string
}

// Validate validates the values in the options
func (o *PrometheusOptions) Validate() error {
	if (o.PrometheusUsername == "") != (o.PrometheusPasswordPath == "") {
		return fmt.Errorf("--prometheus-username and --prometheus-password-path must be specified together")
	}
	if o.PrometheusPasswordPath != "" && o.PrometheusBearerTokenPath != "" {
		return fmt.Errorf("--prometheus-password-path and --prometheus-bearer-token-path are mutually exclusive")
	}
	return nil
}

// AddFlags sets up the flags for PrometheusOptions
func (o *PrometheusOptions) AddFlags(fs *flag.FlagSet) {
	fs.StringVar(&o.PrometheusURL, "prometheus-url", "https://thanos-querier-openshift-monitoring.apps.ci.l2s4.p1.openshiftapps.com", "The prometheus URL")
	fs.StringVar(&o.PrometheusUsername, "prometheus-username", "", "The Prometheus username.")
	fs.StringVar(&o.PrometheusPasswordPath, "prometheus-password-path", "", "The path to a file containing the Prometheus password")
	fs.StringVar(&o.PrometheusBearerTokenPath, "prometheus-bearer-token-path", "", "The path to a file containing the Prometheus bearer token")
}

type basicAuthRoundTripper struct {
	username             string
	passwordPath         string
	passwordGetter       func(passwordPath string) []byte
	originalRoundTripper http.RoundTripper
}

func (rt *basicAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.SetBasicAuth(rt.username, string(rt.passwordGetter(rt.passwordPath)))
	return rt.originalRoundTripper.RoundTrip(req)
}

type bearerTokenAuthRoundTripper struct {
	bearerTokenPath      string
	bearerTokenGetter    func(string) []byte
	originalRoundTripper http.RoundTripper
}

func (rt *bearerTokenAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", string(rt.bearerTokenGetter(rt.bearerTokenPath))))
	return rt.originalRoundTripper.RoundTrip(req)
}

// PrometheusAPI defines what we expect Prometheus to do in the package
type PrometheusAPI interface {
	// Query performs a query for the given time.
	Query(ctx context.Context, query string, ts time.Time, opts ...prometheusapi.Option) (model.Value, prometheusapi.Warnings, error)
}

// GetJobVolumesFromPrometheus gets job volumes from a Prometheus server for the given time
func GetJobVolumesFromPrometheus(ctx context.Context, prometheusAPI PrometheusAPI, ts time.Time) (map[string]float64, error) {
	result, warnings, err := prometheusAPI.Query(ctx, `sum(increase(prowjob_state_transitions{state="pending"}[7d])) by (job_name)`, ts)
	if err != nil {
		return nil, err
	}
	if len(warnings) > 0 {
		logrus.WithField("Warnings", warnings).Warn("Got warnings from Prometheus")
	}

	vector, ok := result.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("returned result of type %T from Prometheus cannot be cast to vector", result)
	}

	jobVolumes := map[string]float64{}
	for _, v := range vector {
		jobVolumes[string(v.Metric[model.LabelName("job_name")])] = float64(v.Value)
	}

	return jobVolumes, nil
}

// NewPrometheusClient return a Prometheus client
func (o *PrometheusOptions) NewPrometheusClient(secretGetter func(string) []byte) (api.Client, error) {
	roundTripper := api.DefaultRoundTripper
	if o.PrometheusUsername != "" {
		roundTripper = &basicAuthRoundTripper{username: o.PrometheusUsername, passwordPath: o.PrometheusPasswordPath, passwordGetter: secretGetter, originalRoundTripper: api.DefaultRoundTripper}
	}
	if o.PrometheusBearerTokenPath != "" {
		roundTripper = &bearerTokenAuthRoundTripper{bearerTokenPath: o.PrometheusBearerTokenPath, bearerTokenGetter: secretGetter, originalRoundTripper: api.DefaultRoundTripper}
	}
	return api.NewClient(api.Config{Address: o.PrometheusURL, RoundTripper: roundTripper})
}
