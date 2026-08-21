package dispatcher

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
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
	JobDurationQuery          string
	JobCPUQuery               string
	JobMemoryQuery            string
	RunWeight                 float64
	DurationHourWeight        float64
	CPUHourWeight             float64
	MemoryGBHourWeight        float64
	MinimumJobDemand          float64
}

func (o *PrometheusOptions) demandDefaults() {
	if o.MinimumJobDemand == 0 {
		o.MinimumJobDemand = 1
	}
	if o.RunWeight == 0 && o.DurationHourWeight == 0 && o.CPUHourWeight == 0 && o.MemoryGBHourWeight == 0 {
		o.RunWeight = 1
	}
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// Validate validates the values in the options
func (o *PrometheusOptions) Validate() error {
	o.demandDefaults()
	if (o.PrometheusUsername == "") != (o.PrometheusPasswordPath == "") {
		return fmt.Errorf("--prometheus-username and --prometheus-password-path must be specified together")
	}
	if o.PrometheusPasswordPath != "" && o.PrometheusBearerTokenPath != "" {
		return fmt.Errorf("--prometheus-password-path and --prometheus-bearer-token-path are mutually exclusive")
	}
	if !isFinite(o.RunWeight) || !isFinite(o.DurationHourWeight) || !isFinite(o.CPUHourWeight) || !isFinite(o.MemoryGBHourWeight) || !isFinite(o.MinimumJobDemand) ||
		o.RunWeight < 0 || o.DurationHourWeight < 0 || o.CPUHourWeight < 0 || o.MemoryGBHourWeight < 0 || o.MinimumJobDemand <= 0 {
		return fmt.Errorf("Prometheus demand weights must be finite and non-negative and --minimum-job-demand must be finite and positive")
	}
	return nil
}

// AddFlags sets up the flags for PrometheusOptions
func (o *PrometheusOptions) AddFlags(fs *flag.FlagSet) {
	fs.StringVar(&o.PrometheusURL, "prometheus-url", "https://thanos-querier-openshift-monitoring.apps.ci.l2s4.p1.openshiftapps.com", "The prometheus URL")
	fs.StringVar(&o.PrometheusUsername, "prometheus-username", "", "The Prometheus username.")
	fs.StringVar(&o.PrometheusPasswordPath, "prometheus-password-path", "", "The path to a file containing the Prometheus password")
	fs.StringVar(&o.PrometheusBearerTokenPath, "prometheus-bearer-token-path", "", "The path to a file containing the Prometheus bearer token")
	fs.StringVar(&o.JobDurationQuery, "prometheus-job-duration-query", "", "Optional PromQL returning aggregate job duration seconds by job_name over the demand window.")
	fs.StringVar(&o.JobCPUQuery, "prometheus-job-cpu-query", "", "Optional PromQL returning aggregate CPU seconds by job_name over the demand window.")
	fs.StringVar(&o.JobMemoryQuery, "prometheus-job-memory-query", "", "Optional PromQL returning aggregate byte-seconds by job_name over the demand window.")
	fs.Float64Var(&o.RunWeight, "job-run-weight", 1, "Demand weight applied to each observed run.")
	fs.Float64Var(&o.DurationHourWeight, "job-duration-hour-weight", 1, "Demand weight applied to each observed job runtime hour.")
	fs.Float64Var(&o.CPUHourWeight, "job-cpu-hour-weight", 1, "Demand weight applied to each observed CPU-hour.")
	fs.Float64Var(&o.MemoryGBHourWeight, "job-memory-gb-hour-weight", 0.25, "Demand weight applied to each observed GiB-hour.")
	fs.Float64Var(&o.MinimumJobDemand, "minimum-job-demand", 1, "Demand assigned to jobs with no usable historical samples.")
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
	return queryJobValues(ctx, prometheusAPI, `sum(increase(prowjob_state_transitions{state="pending"}[7d])) by (job_name)`, ts)
}

func queryJobValues(ctx context.Context, prometheusAPI PrometheusAPI, query string, ts time.Time) (map[string]float64, error) {
	result, warnings, err := prometheusAPI.Query(ctx, query, ts)
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
		jobName := string(v.Metric[model.LabelName("job_name")])
		value := float64(v.Value)
		if jobName == "" {
			return nil, errors.New("Prometheus demand result is missing job_name")
		}
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("Prometheus demand for job %q is not a finite non-negative value", jobName)
		}
		jobVolumes[jobName] += value
	}

	return jobVolumes, nil
}

// GetWeightedJobDemandFromPrometheus combines run count with optional aggregate
// runtime and resource-consumption queries. Optional queries must return a vector
// labelled by job_name, with duration/CPU in seconds and memory in byte-seconds.
func GetWeightedJobDemandFromPrometheus(ctx context.Context, prometheusAPI PrometheusAPI, ts time.Time, options PrometheusOptions) (map[string]float64, error) {
	options.demandDefaults()
	if err := options.Validate(); err != nil {
		return nil, err
	}
	runs, err := GetJobVolumesFromPrometheus(ctx, prometheusAPI, ts)
	if err != nil {
		return nil, fmt.Errorf("query job runs: %w", err)
	}
	type weightedQuery struct {
		query  string
		weight float64
		scale  float64
		name   string
	}
	queries := []weightedQuery{
		{query: options.JobDurationQuery, weight: options.DurationHourWeight, scale: 3600, name: "duration"},
		{query: options.JobCPUQuery, weight: options.CPUHourWeight, scale: 3600, name: "CPU"},
		{query: options.JobMemoryQuery, weight: options.MemoryGBHourWeight, scale: 3600 * 1024 * 1024 * 1024, name: "memory"},
	}
	demand := make(map[string]float64, len(runs))
	for job, count := range runs {
		weightedDemand := count * options.RunWeight
		if !isFinite(weightedDemand) {
			return nil, fmt.Errorf("weighted demand for job %q is not finite", job)
		}
		demand[job] = weightedDemand
	}
	for _, weighted := range queries {
		if weighted.query == "" || weighted.weight == 0 {
			continue
		}
		values, err := queryJobValues(ctx, prometheusAPI, weighted.query, ts)
		if err != nil {
			return nil, fmt.Errorf("query job %s: %w", weighted.name, err)
		}
		for job, value := range values {
			weightedDemand := demand[job] + value/weighted.scale*weighted.weight
			if !isFinite(weightedDemand) {
				return nil, fmt.Errorf("weighted demand for job %q is not finite", job)
			}
			demand[job] = weightedDemand
		}
	}
	for job := range demand {
		if demand[job] < options.MinimumJobDemand {
			demand[job] = options.MinimumJobDemand
		}
	}
	return demand, nil
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
