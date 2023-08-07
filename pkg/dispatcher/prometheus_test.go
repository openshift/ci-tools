package dispatcher

import (
	"context"
	"fmt"
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
	supportedQueries = sets.New[string](`sum(increase(prowjob_state_transitions{state="pending"}[7d])) by (job_name)`)
)

func (prometheusAPI *prometheusAPIForTest) Query(ctx context.Context, query string, ts time.Time, opts ...prometheusapi.Option) (model.Value, prometheusapi.Warnings, error) {
	if !supportedQueries.Has(query) {
		return nil, nil, fmt.Errorf("not supported query: %s", query)
	}
	return prometheusAPI.queryFunc(ctx, query, ts)
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
