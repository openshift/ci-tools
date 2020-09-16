package dispatcher

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/api"
	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"
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

// GetJobVolumesFromPrometheus gets job volumes from a Prometheus server
func GetJobVolumesFromPrometheus(ctx context.Context, prometheusAPI PrometheusAPI) (map[string]float64, error) {
	result, warnings, err := prometheusAPI.Query(ctx, `sum(increase(prowjob_state_transitions{state="pending"}[7d])) by (job_name)`, time.Now())
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

func SaveJobVolumesToFile(jobVolumes map[string]float64, filename string) error {
	bytes, err := yaml.Marshal(jobVolumes)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filename, bytes, os.FileMode(0644))
}

func LoadJobVolumesFromFile(filename string) (map[string]float64, error) {
	var jobVolumes map[string]float64
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return jobVolumes, err
	}
	err = yaml.Unmarshal(bytes, &jobVolumes)
	return jobVolumes, err
}
