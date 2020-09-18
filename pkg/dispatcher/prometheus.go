package dispatcher

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/api"
	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"
)

type basicAuthRoundTripper struct {
	username             string
	password             string
	originalRoundTripper http.RoundTripper
}

func (rt *basicAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.SetBasicAuth(rt.username, rt.password)
	return rt.originalRoundTripper.RoundTrip(req)
}

// PrometheusAPI defines what we expect Prometheus to do in the package
type PrometheusAPI interface {
	// Query performs a query for the given time.
	Query(ctx context.Context, query string, ts time.Time) (model.Value, prometheusapi.Warnings, error)
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
func NewPrometheusClient(prometheusURL, username, password string) (api.Client, error) {
	roundTripper := api.DefaultRoundTripper
	if username != "" {
		roundTripper = &basicAuthRoundTripper{username: username, password: password, originalRoundTripper: api.DefaultRoundTripper}
	}
	return api.NewClient(api.Config{Address: prometheusURL, RoundTripper: roundTripper})
}
