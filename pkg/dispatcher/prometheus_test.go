package dispatcher

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

type prometheusAPIForTest struct {
	queryFunc func(ctx context.Context, query string, ts time.Time) (model.Value, prometheusapi.Warnings, error)
}

var (
	supportedQueries = sets.New[string](`sum(increase(prowjob_state_transitions{state="pending"}[7d])) by (job_name)`, "duration", "cpu", "memory")
)

func (prometheusAPI *prometheusAPIForTest) Query(ctx context.Context, query string, ts time.Time, opts ...prometheusapi.Option) (model.Value, prometheusapi.Warnings, error) {
	if !supportedQueries.Has(query) {
		return nil, nil, fmt.Errorf("not supported query: %s", query)
	}
	return prometheusAPI.queryFunc(ctx, query, ts)
}

func TestGetWeightedJobDemandFromPrometheus(t *testing.T) {
	valueFor := func(job string, value float64) model.Vector {
		return model.Vector{&model.Sample{Metric: model.Metric{model.LabelName("job_name"): model.LabelValue(job)}, Value: model.SampleValue(value)}}
	}
	api := &prometheusAPIForTest{queryFunc: func(_ context.Context, query string, _ time.Time) (model.Value, prometheusapi.Warnings, error) {
		switch query {
		case `sum(increase(prowjob_state_transitions{state="pending"}[7d])) by (job_name)`:
			return valueFor("hourly", 2), nil, nil
		case "duration":
			return valueFor("hourly", 3600), nil, nil
		case "cpu":
			return valueFor("hourly", 7200), nil, nil
		case "memory":
			return valueFor("hourly", 3*3600*1024*1024*1024), nil, nil
		default:
			return nil, nil, fmt.Errorf("unexpected query %q", query)
		}
	}}
	demand, err := GetWeightedJobDemandFromPrometheus(context.Background(), api, time.Now(), PrometheusOptions{
		JobDurationQuery: "duration", JobCPUQuery: "cpu", JobMemoryQuery: "memory",
		RunWeight: 1, DurationHourWeight: 1, CPUHourWeight: 1, MemoryGBHourWeight: .25, MinimumJobDemand: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if demand["hourly"] != 5.75 {
		t.Fatalf("expected weighted demand 5.75, got %v", demand)
	}
}

func TestGetWeightedJobDemandRejectsNonFiniteSamples(t *testing.T) {
	api := &prometheusAPIForTest{queryFunc: func(_ context.Context, _ string, _ time.Time) (model.Value, prometheusapi.Warnings, error) {
		return model.Vector{&model.Sample{Metric: model.Metric{model.LabelName("job_name"): model.LabelValue("job")}, Value: model.SampleValue(math.Inf(1))}}, nil, nil
	}}
	if _, err := GetWeightedJobDemandFromPrometheus(context.Background(), api, time.Now(), PrometheusOptions{}); err == nil || !strings.Contains(err.Error(), "finite") {
		t.Fatalf("non-finite Prometheus sample was accepted: %v", err)
	}
}

func TestPrometheusOptionsValidateRejectsNonFiniteDemandValues(t *testing.T) {
	testCases := []struct {
		name    string
		options PrometheusOptions
	}{
		{name: "NaN run weight", options: PrometheusOptions{RunWeight: math.NaN(), MinimumJobDemand: 1}},
		{name: "infinite duration weight", options: PrometheusOptions{DurationHourWeight: math.Inf(1), MinimumJobDemand: 1}},
		{name: "infinite CPU weight", options: PrometheusOptions{CPUHourWeight: math.Inf(-1), MinimumJobDemand: 1}},
		{name: "NaN memory weight", options: PrometheusOptions{MemoryGBHourWeight: math.NaN(), MinimumJobDemand: 1}},
		{name: "NaN minimum demand", options: PrometheusOptions{RunWeight: 1, MinimumJobDemand: math.NaN()}},
		{name: "infinite minimum demand", options: PrometheusOptions{RunWeight: 1, MinimumJobDemand: math.Inf(1)}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.options.Validate(); err == nil || !strings.Contains(err.Error(), "finite") {
				t.Fatalf("non-finite demand option was accepted: %v", err)
			}
		})
	}
}

func TestGetWeightedJobDemandValidatesBeforeQuerying(t *testing.T) {
	queried := false
	api := &prometheusAPIForTest{queryFunc: func(_ context.Context, _ string, _ time.Time) (model.Value, prometheusapi.Warnings, error) {
		queried = true
		return model.Vector{}, nil, nil
	}}
	if _, err := GetWeightedJobDemandFromPrometheus(context.Background(), api, time.Now(), PrometheusOptions{RunWeight: math.Inf(1), MinimumJobDemand: 1}); err == nil {
		t.Fatal("non-finite demand options were accepted")
	}
	if queried {
		t.Fatal("Prometheus was queried before demand options were validated")
	}
}

func TestGetWeightedJobDemandRejectsNonFiniteComputedDemand(t *testing.T) {
	api := &prometheusAPIForTest{queryFunc: func(_ context.Context, _ string, _ time.Time) (model.Value, prometheusapi.Warnings, error) {
		return model.Vector{&model.Sample{Metric: model.Metric{model.LabelName("job_name"): model.LabelValue("job")}, Value: model.SampleValue(math.MaxFloat64)}}, nil, nil
	}}
	if _, err := GetWeightedJobDemandFromPrometheus(context.Background(), api, time.Now(), PrometheusOptions{RunWeight: 2, MinimumJobDemand: 1}); err == nil || !strings.Contains(err.Error(), "weighted demand") {
		t.Fatalf("non-finite computed demand was accepted: %v", err)
	}
}

func TestGetJobVolumesFromPrometheusSumsDuplicateJobSeries(t *testing.T) {
	api := &prometheusAPIForTest{queryFunc: func(_ context.Context, _ string, _ time.Time) (model.Value, prometheusapi.Warnings, error) {
		return model.Vector{
			&model.Sample{Metric: model.Metric{model.LabelName("job_name"): model.LabelValue("job")}, Value: 2.5},
			&model.Sample{Metric: model.Metric{model.LabelName("job_name"): model.LabelValue("job")}, Value: 3.5},
		}, nil, nil
	}}
	volumes, err := GetJobVolumesFromPrometheus(context.Background(), api, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if volumes["job"] != 6 {
		t.Fatalf("duplicate series were not summed: %#v", volumes)
	}
}

func TestGetJobVolumesFromPrometheus(t *testing.T) {

	now := time.Now().Unix()

	testCases := []struct {
		name             string
		ctx              context.Context
		queryFunc        func(ctx context.Context, query string, ts time.Time) (model.Value, prometheusapi.Warnings, error)
		updateJobVolumes bool
		expected         map[string]float64
		expectedError    error
	}{
		{
			name: "basic case",
			queryFunc: func(ctx context.Context, query string, ts time.Time) (model.Value, prometheusapi.Warnings, error) {
				vec := model.Vector([]*model.Sample{
					{
						Metric:    model.Metric(map[model.LabelName]model.LabelValue{model.LabelName("job_name"): model.LabelValue("pull-ci-some-test-job")}),
						Value:     model.SampleValue(float64(23)),
						Timestamp: model.Time(now),
					},
					{
						Metric:    model.Metric(map[model.LabelName]model.LabelValue{model.LabelName("job_name"): model.LabelValue("release-openshift-ocp-installer-e2e-vsphere-upi-4.2")}),
						Value:     model.SampleValue(float64(61.04382516525817)),
						Timestamp: model.Time(now),
					},
				})
				return vec, nil, nil
			},
			updateJobVolumes: true,
			expected: map[string]float64{
				"pull-ci-some-test-job":                               float64(23),
				"release-openshift-ocp-installer-e2e-vsphere-upi-4.2": float64(61.04382516525817),
			},
		},
		{
			name: "wrong type",
			queryFunc: func(ctx context.Context, query string, ts time.Time) (model.Value, prometheusapi.Warnings, error) {
				sca := &model.Scalar{
					Value:     model.SampleValue(float64(23)),
					Timestamp: model.Time(now),
				}
				return sca, nil, nil
			},
			updateJobVolumes: true,
			expectedError:    fmt.Errorf("returned result of type *model.Scalar from Prometheus cannot be cast to vector"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualError := GetJobVolumesFromPrometheus(tc.ctx, &prometheusAPIForTest{tc.queryFunc}, time.Now())
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}
