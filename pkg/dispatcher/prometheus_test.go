package dispatcher

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/google/go-cmp/cmp"
	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

type prometheusAPIForTest struct {
	queryFunc func(ctx context.Context, query string, ts time.Time) (model.Value, prometheusapi.Warnings, error)
}

var (
	supportedQueries = sets.NewString(`sum(increase(prowjob_state_transitions{state="pending"}[7d])) by (job_name)`)
)

func (prometheusAPI *prometheusAPIForTest) Query(ctx context.Context, query string, ts time.Time) (model.Value, prometheusapi.Warnings, error) {
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
			actual, err := GetJobVolumesFromPrometheus(tc.ctx, &prometheusAPIForTest{tc.queryFunc}, time.Now())
			if !reflect.DeepEqual(tc.expected, actual) {
				t.Errorf("%s: actual differs from expected:\n%s", t.Name(), cmp.Diff(tc.expected, actual))
			}
			equalError(t, tc.expectedError, err)
		})
	}
}

func equalError(t *testing.T, expected, actual error) {
	if (expected == nil) != (actual == nil) {
		t.Errorf("%s: expecting error \"%v\", got \"%v\"", t.Name(), expected, actual)
	}
	if expected != nil && actual != nil && expected.Error() != actual.Error() {
		t.Errorf("%s: expecting error msg %q, got %q", t.Name(), expected.Error(), actual.Error())
	}
}
