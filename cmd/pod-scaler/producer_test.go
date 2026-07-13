package main

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"

	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
)

func TestQueriesByMetric(t *testing.T) {
	expected := map[string]string{
		"pods/container_cpu_usage_seconds_total": `sum by (
    namespace,
    pod,
    container
  ) (rate(container_cpu_usage_seconds_total{container!="POD",container!=""}[3m]))
  * on(namespace,pod) 
  group_left(
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_openshift_io_build_name,
    label_ci_openshift_io_release,
    label_app,
    label_pod_scaler_openshift_io_measured
  ) max by (
    namespace,
    pod,
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_openshift_io_build_name,
    label_ci_openshift_io_release,
    label_app,
    label_pod_scaler_openshift_io_measured
  ) (kube_pod_labels{label_created_by_ci="true",label_ci_openshift_io_metadata_step=""})`,
		"pods/container_memory_working_set_bytes": `sum by (
    namespace,
    pod,
    container
  ) (container_memory_working_set_bytes{container!="POD",container!=""})
  * on(namespace,pod) 
  group_left(
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_openshift_io_build_name,
    label_ci_openshift_io_release,
    label_app,
    label_pod_scaler_openshift_io_measured
  ) max by (
    namespace,
    pod,
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_openshift_io_build_name,
    label_ci_openshift_io_release,
    label_app,
    label_pod_scaler_openshift_io_measured
  ) (kube_pod_labels{label_created_by_ci="true",label_ci_openshift_io_metadata_step=""})`,
		"prowjobs/container_cpu_usage_seconds_total": `sum by (
    namespace,
    pod,
    container
  ) (rate(container_cpu_usage_seconds_total{container!="POD",container!=""}[3m]))
  * on(namespace,pod) 
  group_left(
    label_created_by_prow,
    label_prow_k8s_io_context,
    label_prow_k8s_io_refs_org,
    label_prow_k8s_io_refs_repo,
    label_prow_k8s_io_refs_base_ref,
    label_prow_k8s_io_job,
    label_prow_k8s_io_type,
    label_pod_scaler_openshift_io_measured
  ) max by (
    namespace,
    pod,
    label_created_by_prow,
    label_prow_k8s_io_context,
    label_prow_k8s_io_refs_org,
    label_prow_k8s_io_refs_repo,
    label_prow_k8s_io_refs_base_ref,
    label_prow_k8s_io_job,
    label_prow_k8s_io_type,
    label_pod_scaler_openshift_io_measured
  ) (kube_pod_labels{label_created_by_prow="true",label_prow_k8s_io_job!="",label_ci_openshift_org_rehearse=""})`,
		"prowjobs/container_memory_working_set_bytes": `sum by (
    namespace,
    pod,
    container
  ) (container_memory_working_set_bytes{container!="POD",container!=""})
  * on(namespace,pod) 
  group_left(
    label_created_by_prow,
    label_prow_k8s_io_context,
    label_prow_k8s_io_refs_org,
    label_prow_k8s_io_refs_repo,
    label_prow_k8s_io_refs_base_ref,
    label_prow_k8s_io_job,
    label_prow_k8s_io_type,
    label_pod_scaler_openshift_io_measured
  ) max by (
    namespace,
    pod,
    label_created_by_prow,
    label_prow_k8s_io_context,
    label_prow_k8s_io_refs_org,
    label_prow_k8s_io_refs_repo,
    label_prow_k8s_io_refs_base_ref,
    label_prow_k8s_io_job,
    label_prow_k8s_io_type,
    label_pod_scaler_openshift_io_measured
  ) (kube_pod_labels{label_created_by_prow="true",label_prow_k8s_io_job!="",label_ci_openshift_org_rehearse=""})`,
		"steps/container_cpu_usage_seconds_total": `sum by (
    namespace,
    pod,
    container
  ) (rate(container_cpu_usage_seconds_total{container!="POD",container!=""}[3m]))
  * on(namespace,pod) 
  group_left(
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_ci_openshift_io_metadata_step,
    label_pod_scaler_openshift_io_measured
  ) max by (
    namespace,
    pod,
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_ci_openshift_io_metadata_step,
    label_pod_scaler_openshift_io_measured
  ) (kube_pod_labels{label_created_by_ci="true",label_ci_openshift_io_metadata_step!=""})`,
		"steps/container_memory_working_set_bytes": `sum by (
    namespace,
    pod,
    container
  ) (container_memory_working_set_bytes{container!="POD",container!=""})
  * on(namespace,pod) 
  group_left(
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_ci_openshift_io_metadata_step,
    label_pod_scaler_openshift_io_measured
  ) max by (
    namespace,
    pod,
    label_ci_openshift_io_metadata_org,
    label_ci_openshift_io_metadata_repo,
    label_ci_openshift_io_metadata_branch,
    label_ci_openshift_io_metadata_variant,
    label_ci_openshift_io_metadata_target,
    label_ci_openshift_io_metadata_step,
    label_pod_scaler_openshift_io_measured
  ) (kube_pod_labels{label_created_by_ci="true",label_ci_openshift_io_metadata_step!=""})`,
	}
	if diff := cmp.Diff(expected, queriesByMetric()); diff != "" {
		t.Errorf("incorrect queries: %v", diff)
	}
}

func TestDivideRange(t *testing.T) {
	var testCases = []struct {
		name      string
		uncovered []podscaler.TimeRange
		step      time.Duration
		numSteps  int64
		expected  []prometheusapi.Range
	}{
		{
			name: "smaller range than one step",
			uncovered: []podscaler.TimeRange{{
				Start: time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 0, 20, 0, time.UTC),
			}},
			step:     time.Minute,
			numSteps: 100,
			expected: nil,
		},
		{
			name: "range is one step",
			uncovered: []podscaler.TimeRange{{
				Start: time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 1, 0, 0, time.UTC),
			}},
			step:     time.Minute,
			numSteps: 100,
			expected: nil,
		},
		{
			name: "smaller range than one division",
			uncovered: []podscaler.TimeRange{{
				Start: time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 10, 0, 0, time.UTC),
			}},
			step:     time.Minute,
			numSteps: 100,
			expected: []prometheusapi.Range{{
				Start: time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 10, 0, 0, time.UTC),
				Step:  time.Minute,
			}},
		},
		{
			name: "range fits exactly one division",
			uncovered: []podscaler.TimeRange{{
				Start: time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 10, 0, 0, time.UTC),
			}},
			step:     time.Minute,
			numSteps: 10,
			expected: []prometheusapi.Range{{
				Start: time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 10, 0, 0, time.UTC),
				Step:  time.Minute,
			}},
		},
		{
			name: "range fits more than one division, evenly divisible",
			uncovered: []podscaler.TimeRange{{
				Start: time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 30, 0, 0, time.UTC),
			}},
			step:     time.Minute,
			numSteps: 10,
			expected: []prometheusapi.Range{{
				Start: time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 10, 0, 0, time.UTC),
				Step:  time.Minute,
			}, {
				Start: time.Date(0, 0, 0, 0, 11, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 21, 0, 0, time.UTC),
				Step:  time.Minute,
			}, {
				Start: time.Date(0, 0, 0, 0, 22, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 30, 0, 0, time.UTC),
				Step:  time.Minute,
			}},
		},
		{
			name: "range fits more than one division, not evenly divisible",
			uncovered: []podscaler.TimeRange{{
				Start: time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 36, 0, 0, time.UTC),
			}},
			step:     time.Minute,
			numSteps: 10,
			expected: []prometheusapi.Range{{
				Start: time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 10, 0, 0, time.UTC),
				Step:  time.Minute,
			}, {
				Start: time.Date(0, 0, 0, 0, 11, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 21, 0, 0, time.UTC),
				Step:  time.Minute,
			}, {
				Start: time.Date(0, 0, 0, 0, 22, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 32, 0, 0, time.UTC),
				Step:  time.Minute,
			}, {
				Start: time.Date(0, 0, 0, 0, 33, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 36, 0, 0, time.UTC),
				Step:  time.Minute,
			}},
		},
		{
			name: "uncovered ranges smaller than, and larger than divisions, both equally and unequally divisible",
			uncovered: []podscaler.TimeRange{{ // this one is smaller than a step
				Start: time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 0, 0, 10, 0, time.UTC),
			}, { // this one is smaller than a division
				Start: time.Date(0, 0, 0, 1, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 1, 2, 0, 0, time.UTC),
			}, { // this one is exactly one division
				Start: time.Date(0, 0, 0, 2, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 2, 10, 0, 0, time.UTC),
			}, { // this one is two divisions, evenly dividing
				Start: time.Date(0, 0, 0, 3, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 3, 20, 0, 0, time.UTC),
			}, { // this one is two divisions, not evenly dividing
				Start: time.Date(0, 0, 0, 4, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 4, 17, 0, 0, time.UTC),
			}},
			step:     time.Minute,
			numSteps: 10,
			expected: []prometheusapi.Range{{
				Start: time.Date(0, 0, 0, 1, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 1, 2, 0, 0, time.UTC),
				Step:  time.Minute,
			}, {
				Start: time.Date(0, 0, 0, 2, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 2, 10, 0, 0, time.UTC),
				Step:  time.Minute,
			}, {
				Start: time.Date(0, 0, 0, 3, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 3, 10, 0, 0, time.UTC),
				Step:  time.Minute,
			}, {
				Start: time.Date(0, 0, 0, 3, 11, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 3, 20, 0, 0, time.UTC),
				Step:  time.Minute,
			}, {
				Start: time.Date(0, 0, 0, 4, 0, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 4, 10, 0, 0, time.UTC),
				Step:  time.Minute,
			}, {
				Start: time.Date(0, 0, 0, 4, 11, 0, 0, time.UTC),
				End:   time.Date(0, 0, 0, 4, 17, 0, 0, time.UTC),
				Step:  time.Minute,
			}},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := divideRange(testCase.uncovered, testCase.step, testCase.numSteps)
			if diff := cmp.Diff(actual, testCase.expected); diff != "" {
				t.Errorf("%s: got incorrect ranges: %v", testCase.name, diff)
			}
			seen := map[time.Time]interface{}{}
			for i, item := range actual {
				if item.End.Sub(item.Start) == 0 {
					t.Errorf("%s: divided[%d]: got a 0-length range from %s to %s", testCase.name, i, item.Start.String(), item.End.String())
				}
				if _, ok := seen[item.Start]; ok {
					t.Errorf("%s: divided[%d].start: overlaps with a boundary of another range", testCase.name, i)
				}
				seen[item.Start] = nil
				if _, ok := seen[item.End]; ok {
					t.Errorf("%s: divided[%d].end: overlaps with a boundary of another range", testCase.name, i)
				}
				seen[item.End] = nil
			}
		})
	}
}

func TestFilterMemoryFloor(t *testing.T) {
	logger := logrus.NewEntry(logrus.StandardLogger())

	// authoritativeMinMemoryLimit is 1Mi = 1048576 bytes
	minValue := model.SampleValue(authoritativeMinMemoryLimit.AsApproximateFloat64())

	tests := []struct {
		name     string
		input    model.Matrix
		expected []int // expected number of values per stream after filtering
	}{
		{
			name: "sub-1Mi samples are skipped",
			input: model.Matrix{
				{
					Metric: model.Metric{"__name__": "container_memory_working_set_bytes"},
					Values: []model.SamplePair{
						{Timestamp: model.TimeFromUnix(1), Value: 324 * 1024},        // 324Ki - below floor
						{Timestamp: model.TimeFromUnix(2), Value: 512 * 1024},        // 512Ki - below floor
						{Timestamp: model.TimeFromUnix(3), Value: minValue},          // exactly 1Mi - kept
						{Timestamp: model.TimeFromUnix(4), Value: 2 * 1024 * 1024},   // 2Mi - kept
						{Timestamp: model.TimeFromUnix(5), Value: 100 * 1024 * 1024}, // 100Mi - kept
					},
				},
			},
			expected: []int{3},
		},
		{
			name: "all samples above floor are kept",
			input: model.Matrix{
				{
					Metric: model.Metric{"__name__": "container_memory_working_set_bytes"},
					Values: []model.SamplePair{
						{Timestamp: model.TimeFromUnix(1), Value: minValue},
						{Timestamp: model.TimeFromUnix(2), Value: minValue + 1},
						{Timestamp: model.TimeFromUnix(3), Value: 50 * 1024 * 1024},
					},
				},
			},
			expected: []int{3},
		},
		{
			name: "all samples below floor are removed",
			input: model.Matrix{
				{
					Metric: model.Metric{"__name__": "container_memory_working_set_bytes"},
					Values: []model.SamplePair{
						{Timestamp: model.TimeFromUnix(1), Value: 100},
						{Timestamp: model.TimeFromUnix(2), Value: 500 * 1024},
					},
				},
			},
			expected: []int{0},
		},
		{
			name:     "empty matrix is handled",
			input:    model.Matrix{},
			expected: []int{},
		},
		{
			name: "multiple streams are filtered independently",
			input: model.Matrix{
				{
					Metric: model.Metric{"container": "a"},
					Values: []model.SamplePair{
						{Timestamp: model.TimeFromUnix(1), Value: 100},             // below floor
						{Timestamp: model.TimeFromUnix(2), Value: 2 * 1024 * 1024}, // above floor
					},
				},
				{
					Metric: model.Metric{"container": "b"},
					Values: []model.SamplePair{
						{Timestamp: model.TimeFromUnix(1), Value: 3 * 1024 * 1024}, // above floor
					},
				},
			},
			expected: []int{1, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filterMemoryFloor(tt.input, logger)
			for i, stream := range tt.input {
				if len(stream.Values) != tt.expected[i] {
					t.Errorf("stream %d: expected %d values after filtering, got %d", i, tt.expected[i], len(stream.Values))
				}
			}
		})
	}
}
