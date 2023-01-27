package pod_scaler

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openhistogram/circonusllhist"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestCoalesce(t *testing.T) {
	var testCases = []struct {
		name   string
		input  []TimeRange
		output []TimeRange
	}{
		{
			name: "no overlaps",
			input: []TimeRange{
				{
					Start: time.Date(1, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2, 0, 0, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(3, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(4, 0, 0, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(5, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(6, 0, 0, 0, 0, 0, 0, time.UTC),
				},
			},
			output: []TimeRange{
				{
					Start: time.Date(1, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2, 0, 0, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(3, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(4, 0, 0, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(5, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(6, 0, 0, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			name: "some overlaps",
			input: []TimeRange{
				{
					Start: time.Date(1, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(3, 0, 0, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(3, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(4, 0, 0, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(5, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(6, 0, 0, 0, 0, 0, 0, time.UTC),
				},
			},
			output: []TimeRange{
				{
					Start: time.Date(1, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(4, 0, 0, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(5, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(6, 0, 0, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			name: "all overlaps",
			input: []TimeRange{
				{
					Start: time.Date(1, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(3, 0, 0, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(3, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(5, 0, 0, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(5, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(6, 0, 0, 0, 0, 0, 0, time.UTC),
				},
			},
			output: []TimeRange{
				{
					Start: time.Date(1, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(6, 0, 0, 0, 0, 0, 0, time.UTC),
				},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if diff := cmp.Diff(testCase.output, coalesce(testCase.input)); diff != "" {
				t.Errorf("%s: got incorrect output: %v", testCase.name, diff)
			}
		})
	}
}

func TestUncoveredRanges(t *testing.T) {
	var testCases = []struct {
		name     string
		input    TimeRange
		coverage []TimeRange
		output   []TimeRange
	}{
		{
			name: "more than fully covered",
			input: TimeRange{
				Start: time.Date(2, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(3, 0, 0, 0, 0, 0, 0, time.UTC),
			},
			coverage: []TimeRange{
				{
					Start: time.Date(1, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(6, 0, 0, 0, 0, 0, 0, time.UTC),
				},
			},
			output: nil,
		},
		{
			name: "exactly covered",
			input: TimeRange{
				Start: time.Date(2, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(3, 0, 0, 0, 0, 0, 0, time.UTC),
			},
			coverage: []TimeRange{
				{
					Start: time.Date(2, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(3, 0, 0, 0, 0, 0, 0, time.UTC),
				},
			},
			output: nil,
		},
		{
			name: "partially covered",
			input: TimeRange{
				Start: time.Date(2, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(3, 0, 0, 0, 0, 0, 0, time.UTC),
			},
			coverage: []TimeRange{
				{
					Start: time.Date(1, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2, 1, 0, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(2, 3, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2, 8, 0, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(2, 11, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(3, 3, 0, 0, 0, 0, 0, time.UTC),
				},
			},
			output: []TimeRange{
				{
					Start: time.Date(2, 1, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2, 3, 0, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(2, 8, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2, 11, 0, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			name: "not covered",
			input: TimeRange{
				Start: time.Date(2, 0, 0, 0, 0, 0, 0, time.UTC),
				End:   time.Date(3, 0, 0, 0, 0, 0, 0, time.UTC),
			},
			coverage: []TimeRange{
				{
					Start: time.Date(11, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(21, 1, 0, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(21, 3, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(21, 8, 0, 0, 0, 0, 0, time.UTC),
				},
			},
			output: []TimeRange{
				{
					Start: time.Date(2, 0, 0, 0, 0, 0, 0, time.UTC),
					End:   time.Date(3, 0, 0, 0, 0, 0, 0, time.UTC),
				},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if diff := cmp.Diff(testCase.output, UncoveredRanges(testCase.input, testCase.coverage)); diff != "" {
				t.Errorf("%s: got incorrect output: %v", testCase.name, diff)
			}
		})
	}
}

func TestMetadataFromMetric(t *testing.T) {
	var testCases = []struct {
		name   string
		metric model.Metric
		meta   FullMetadata
	}{
		{
			name: "step pod",
			metric: model.Metric{
				model.LabelName("label_ci_openshift_io_metadata_org"):     "org",
				model.LabelName("label_ci_openshift_io_metadata_repo"):    "repo",
				model.LabelName("label_ci_openshift_io_metadata_branch"):  "branch",
				model.LabelName("label_ci_openshift_io_metadata_variant"): "variant",
				model.LabelName("label_ci_openshift_io_metadata_target"):  "target",
				model.LabelName("label_ci_openshift_io_metadata_step"):    "step",
				model.LabelName("pod"):                                    "pod",
				model.LabelName("container"):                              "container",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Target:    "target",
				Step:      "step",
				Pod:       "pod",
				Container: "container",
			},
		},
		{
			name: "build pod",
			metric: model.Metric{
				model.LabelName("label_ci_openshift_io_metadata_org"):     "org",
				model.LabelName("label_ci_openshift_io_metadata_repo"):    "repo",
				model.LabelName("label_ci_openshift_io_metadata_branch"):  "branch",
				model.LabelName("label_ci_openshift_io_metadata_variant"): "variant",
				model.LabelName("label_ci_openshift_io_metadata_target"):  "target",
				model.LabelName("label_openshift_io_build_name"):          "src",
				model.LabelName("pod"):                                    "src-build",
				model.LabelName("container"):                              "container",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Pod:       "src-build",
				Container: "container",
			},
		},
		{
			name: "release pod",
			metric: model.Metric{
				model.LabelName("label_ci_openshift_io_metadata_org"):     "org",
				model.LabelName("label_ci_openshift_io_metadata_repo"):    "repo",
				model.LabelName("label_ci_openshift_io_metadata_branch"):  "branch",
				model.LabelName("label_ci_openshift_io_metadata_variant"): "variant",
				model.LabelName("label_ci_openshift_io_metadata_target"):  "target",
				model.LabelName("label_ci_openshift_io_release"):          "latest",
				model.LabelName("pod"):                                    "release-latest-cli",
				model.LabelName("container"):                              "container",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Pod:       "release-latest-cli",
				Container: "container",
			},
		},
		{
			name: "RPM repo pod",
			metric: model.Metric{
				model.LabelName("label_ci_openshift_io_metadata_org"):     "org",
				model.LabelName("label_ci_openshift_io_metadata_repo"):    "repo",
				model.LabelName("label_ci_openshift_io_metadata_branch"):  "branch",
				model.LabelName("label_ci_openshift_io_metadata_variant"): "variant",
				model.LabelName("label_ci_openshift_io_metadata_target"):  "target",
				model.LabelName("label_app"):                              "rpm-repo",
				model.LabelName("pod"):                                    "rpm-repo-5d88d4fc4c-jg2xb",
				model.LabelName("container"):                              "rpm-repo",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Container: "rpm-repo",
			},
		},
		{
			name: "raw prowjob pod",
			metric: model.Metric{
				model.LabelName("label_created_by_prow"):           "true",
				model.LabelName("label_prow_k8s_io_refs_org"):      "org",
				model.LabelName("label_prow_k8s_io_refs_repo"):     "repo",
				model.LabelName("label_prow_k8s_io_refs_base_ref"): "branch",
				model.LabelName("label_prow_k8s_io_context"):       "context",
				model.LabelName("pod"):                             "d316d4cc-a437-11eb-b35f-0a580a800e92",
				model.LabelName("container"):                       "container",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				},
				Target:    "context",
				Container: "container",
			},
		},
		{
			name: "raw periodic prowjob pod without context",
			metric: model.Metric{
				model.LabelName("label_created_by_prow"):           "true",
				model.LabelName("label_prow_k8s_io_refs_org"):      "org",
				model.LabelName("label_prow_k8s_io_refs_repo"):     "repo",
				model.LabelName("label_prow_k8s_io_refs_base_ref"): "branch",
				model.LabelName("label_prow_k8s_io_context"):       "",
				model.LabelName("label_prow_k8s_io_job"):           "periodic-ci-org-repo-branch-context",
				model.LabelName("label_prow_k8s_io_type"):          "periodic",
				model.LabelName("pod"):                             "d316d4cc-a437-11eb-b35f-0a580a800e92",
				model.LabelName("container"):                       "container",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				},
				Target:    "context",
				Container: "container",
			},
		},
		{
			name: "raw repo-less periodic prowjob pod without context",
			metric: model.Metric{
				model.LabelName("label_created_by_prow"):     "true",
				model.LabelName("label_prow_k8s_io_context"): "",
				model.LabelName("label_prow_k8s_io_job"):     "periodic-handwritten-prowjob",
				model.LabelName("label_prow_k8s_io_type"):    "periodic",
				model.LabelName("pod"):                       "d316d4cc-a437-11eb-b35f-0a580a800e92",
				model.LabelName("container"):                 "container",
			},
			meta: FullMetadata{
				Target:    "periodic-handwritten-prowjob",
				Container: "container",
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if diff := cmp.Diff(testCase.meta, metadataFromMetric(testCase.metric)); diff != "" {
				t.Errorf("%s: got incorrect meta from metric: %v", testCase.name, diff)
			}
		})
	}
}

func TestSyntheticContextFromJob(t *testing.T) {
	var testCases = []struct {
		name     string
		meta     api.Metadata
		metric   model.Metric
		expected string
	}{
		{
			name: "periodic prowjob",
			metric: model.Metric{
				model.LabelName("label_prow_k8s_io_job"):  "periodic-ci-org-repo-branch-context",
				model.LabelName("label_prow_k8s_io_type"): "periodic",
			},
			meta: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			expected: "context",
		},
		{
			name: "periodic prowjob without repo",
			metric: model.Metric{
				model.LabelName("label_prow_k8s_io_job"):  "periodic-handwritten-prowjob",
				model.LabelName("label_prow_k8s_io_type"): "periodic",
			},
			expected: "periodic-handwritten-prowjob",
		},
		{
			name: "postsubmit prowjob",
			metric: model.Metric{
				model.LabelName("label_prow_k8s_io_job"):  "branch-ci-org-repo-branch-context",
				model.LabelName("label_prow_k8s_io_type"): "postsubmit",
			},
			meta: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			expected: "context",
		},
		{
			name: "presubmit prowjob",
			metric: model.Metric{
				model.LabelName("label_prow_k8s_io_job"):  "pull-ci-org-repo-branch-context",
				model.LabelName("label_prow_k8s_io_type"): "presubmit",
			},
			meta: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			expected: "context",
		},
		{
			name: "context lost due to truncation",
			metric: model.Metric{
				model.LabelName("label_prow_k8s_io_job"):  "periodic-ci-org-which-contributes-to-making-the-full-name-longe",
				model.LabelName("label_prow_k8s_io_type"): "periodic",
			},
			meta: api.Metadata{
				Org:    "org-which-contributes-to-making-the-full-name-longer-than-the-character-limit-for-labels",
				Repo:   "repo",
				Branch: "branch",
			},
			expected: "",
		},
		{
			name: "context lost due to no job label",
			metric: model.Metric{
				model.LabelName("label_prow_k8s_io_type"): "periodic",
			},
			meta: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			expected: "",
		},
		{
			name: "context lost due to no type label",
			metric: model.Metric{
				model.LabelName("label_prow_k8s_io_job"): "pull-ci-org-repo-branch-context",
			},
			meta: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			expected: "",
		},
		{
			name: "context lost due to invalid type label",
			metric: model.Metric{
				model.LabelName("label_prow_k8s_io_job"):  "pull-ci-org-repo-branch-context",
				model.LabelName("label_prow_k8s_io_type"): "asldkjslkdjf",
			},
			meta: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			expected: "",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if diff := cmp.Diff(testCase.expected, syntheticContextFromJob(testCase.meta, testCase.metric)); diff != "" {
				t.Errorf("%s: got incorrect synthetic job name: %v", testCase.name, diff)
			}
		})
	}
}

func year(y int) time.Time {
	return time.Date(y, 0, 0, 0, 0, 0, 0, time.UTC)
}

func TestCachedQuery_Record(t *testing.T) {
	var metrics = []struct {
		metric model.Metric
		meta   FullMetadata
	}{
		{
			metric: model.Metric{
				model.LabelName("label_ci_openshift_io_metadata_org"):     "org",
				model.LabelName("label_ci_openshift_io_metadata_repo"):    "repo",
				model.LabelName("label_ci_openshift_io_metadata_branch"):  "branch",
				model.LabelName("label_ci_openshift_io_metadata_variant"): "variant",
				model.LabelName("label_ci_openshift_io_metadata_target"):  "target",
				model.LabelName("label_ci_openshift_io_metadata_step"):    "step",
				model.LabelName("pod"):                                    "pod",
				model.LabelName("container"):                              "container",
				model.LabelName("namespace"):                              "namespace",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Target:    "target",
				Step:      "step",
				Pod:       "pod",
				Container: "container",
			},
		},
		{
			metric: model.Metric{
				model.LabelName("label_ci_openshift_io_metadata_org"):     "ORG",
				model.LabelName("label_ci_openshift_io_metadata_repo"):    "REPO",
				model.LabelName("label_ci_openshift_io_metadata_branch"):  "BRANCH",
				model.LabelName("label_ci_openshift_io_metadata_variant"): "VARIANT",
				model.LabelName("label_ci_openshift_io_metadata_target"):  "TARGET",
				model.LabelName("label_ci_openshift_io_metadata_step"):    "STEP",
				model.LabelName("pod"):                                    "POD",
				model.LabelName("container"):                              "CONTAINER",
				model.LabelName("namespace"):                              "NAMESPACE",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:     "ORG",
					Repo:    "REPO",
					Branch:  "BRANCH",
					Variant: "VARIANT",
				},
				Target:    "TARGET",
				Step:      "STEP",
				Pod:       "POD",
				Container: "CONTAINER",
			},
		},
		{
			metric: model.Metric{
				model.LabelName("label_ci_openshift_io_metadata_org"):     "ORG",
				model.LabelName("label_ci_openshift_io_metadata_repo"):    "REPO",
				model.LabelName("label_ci_openshift_io_metadata_branch"):  "BRANCH",
				model.LabelName("label_ci_openshift_io_metadata_variant"): "VARIANT",
				model.LabelName("label_ci_openshift_io_metadata_target"):  "TARGET",
				model.LabelName("label_ci_openshift_io_metadata_step"):    "STEP",
				model.LabelName("pod"):                                    "POD",
				model.LabelName("container"):                              "CONTAINER",
				model.LabelName("namespace"):                              "OTHER_NAMESPACE",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:     "ORG",
					Repo:    "REPO",
					Branch:  "BRANCH",
					Variant: "VARIANT",
				},
				Target:    "TARGET",
				Step:      "STEP",
				Pod:       "POD",
				Container: "CONTAINER",
			},
		},
	}
	q := CachedQuery{
		Query: "whatever",
		RangesByCluster: map[string][]TimeRange{
			"cluster": {},
			"CLUSTER": {},
		},
		Data:           map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{},
		DataByMetaData: map[FullMetadata][]model.Fingerprint{},
	}

	logger := logrus.WithField("test", "TestCachedQuery_Record")

	// insert into an empty data structure, should update ranges and make new hist
	q.Record("cluster", TimeRange{Start: year(1), End: year(20)}, model.Matrix{{
		Metric: metrics[0].metric,
		Values: []model.SamplePair{
			{Value: 1, Timestamp: 1},
			{Value: 2, Timestamp: 2},
			{Value: 3, Timestamp: 3},
		},
	}}, logger)

	expectedInner := circonusllhist.New()
	for _, value := range []float64{1, 2, 3} {
		if err := expectedInner.RecordValue(value); err != nil {
			t.Errorf("failed to insert value into histogram, this should never happen: %v", err)
		}
	}
	expectedHist := circonusllhist.NewHistogramWithoutLookups(expectedInner)
	expected := CachedQuery{
		Query: "whatever",
		RangesByCluster: map[string][]TimeRange{
			"cluster": {{Start: year(1), End: year(20)}},
			"CLUSTER": {},
		},
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
			metrics[0].metric.Fingerprint(): expectedHist,
		},
		DataByMetaData: map[FullMetadata][]model.Fingerprint{
			metrics[0].meta: {metrics[0].metric.Fingerprint()},
		},
	}
	if diff := cmp.Diff(expected, q, dataComparer); diff != "" {
		t.Errorf("got incorrect state after first insertion: %v", diff)
	}

	// insert data from another cluster for another metric
	q.Record("CLUSTER", TimeRange{Start: year(1), End: year(20)}, model.Matrix{{
		Metric: metrics[1].metric,
		Values: []model.SamplePair{
			{Value: 1, Timestamp: 1},
			{Value: 2, Timestamp: 2},
			{Value: 3, Timestamp: 3},
		},
	}}, logger)

	expected = CachedQuery{
		Query: "whatever",
		RangesByCluster: map[string][]TimeRange{
			"cluster": {{Start: year(1), End: year(20)}},
			"CLUSTER": {{Start: year(1), End: year(20)}},
		},
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
			metrics[0].metric.Fingerprint(): expectedHist,
			metrics[1].metric.Fingerprint(): expectedHist,
		},
		DataByMetaData: map[FullMetadata][]model.Fingerprint{
			metrics[0].meta: {metrics[0].metric.Fingerprint()},
			metrics[1].meta: {metrics[1].metric.Fingerprint()},
		},
	}
	if diff := cmp.Diff(expected, q, dataComparer); diff != "" {
		t.Errorf("got incorrect state after second insertion: %v", diff)
	}

	// insert more data for an existing metric and existing cluster
	q.Record("CLUSTER", TimeRange{Start: year(20), End: year(25)}, model.Matrix{{
		Metric: metrics[1].metric,
		Values: []model.SamplePair{
			{Value: 4, Timestamp: 1},
			{Value: 5, Timestamp: 2},
			{Value: 6, Timestamp: 3},
		},
	}}, logger)

	biggerInner := circonusllhist.New()
	for _, value := range []float64{1, 2, 3, 4, 5, 6} {
		if err := biggerInner.RecordValue(value); err != nil {
			t.Errorf("failed to insert value into histogram, this should never happen: %v", err)
		}
	}
	biggerHist := circonusllhist.NewHistogramWithoutLookups(biggerInner)
	expected = CachedQuery{
		Query: "whatever",
		RangesByCluster: map[string][]TimeRange{
			"cluster": {{Start: year(1), End: year(20)}},
			"CLUSTER": {{Start: year(1), End: year(25)}},
		},
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
			metrics[0].metric.Fingerprint(): expectedHist,
			metrics[1].metric.Fingerprint(): biggerHist,
		},
		DataByMetaData: map[FullMetadata][]model.Fingerprint{
			metrics[0].meta: {metrics[0].metric.Fingerprint()},
			metrics[1].meta: {metrics[1].metric.Fingerprint()},
		},
	}
	if diff := cmp.Diff(expected, q, dataComparer); diff != "" {
		t.Errorf("got incorrect state after third insertion: %v", diff)
	}

	// insert more data for an existing cluster and metadata but for a new metric fingerprint
	q.Record("CLUSTER", TimeRange{Start: year(30), End: year(35)}, model.Matrix{{
		Metric: metrics[2].metric,
		Values: []model.SamplePair{
			{Value: 7, Timestamp: 1},
			{Value: 8, Timestamp: 2},
			{Value: 9, Timestamp: 3},
		},
	}}, logger)

	otherInner := circonusllhist.New()
	for _, value := range []float64{7, 8, 9} {
		if err := otherInner.RecordValue(value); err != nil {
			t.Errorf("failed to insert value into histogram, this should never happen: %v", err)
		}
	}
	otherHist := circonusllhist.NewHistogramWithoutLookups(otherInner)
	expected = CachedQuery{
		Query: "whatever",
		RangesByCluster: map[string][]TimeRange{
			"cluster": {{Start: year(1), End: year(20)}},
			"CLUSTER": {{Start: year(1), End: year(25)}, {Start: year(30), End: year(35)}},
		},
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
			metrics[0].metric.Fingerprint(): expectedHist,
			metrics[1].metric.Fingerprint(): biggerHist,
			metrics[2].metric.Fingerprint(): otherHist,
		},
		DataByMetaData: map[FullMetadata][]model.Fingerprint{
			metrics[0].meta: {metrics[0].metric.Fingerprint()},
			metrics[1].meta: {metrics[1].metric.Fingerprint(), metrics[2].metric.Fingerprint()},
		},
	}
	if diff := cmp.Diff(expected, q, dataComparer); diff != "" {
		t.Errorf("got incorrect state after fourth insertion: %v", diff)
	}
}

var dataComparer = cmp.Comparer(func(a, b *circonusllhist.HistogramWithoutLookups) bool {
	return a.Histogram().Equals(b.Histogram())
})

func TestCachedQuery_Prune(t *testing.T) {
	q := CachedQuery{
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{},
		DataByMetaData: map[FullMetadata][]model.Fingerprint{
			{Step: "1"}:      {1},
			{Step: "2"}:      {2},
			{Step: "3"}:      {3},
			{Step: "4"}:      {4},
			{Step: "5"}:      {5},
			{Step: "6-30"}:   {6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30},
			{Step: "31-130"}: {31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94, 95, 96, 97, 98, 99, 100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 114, 115, 116, 117, 118, 119, 120, 121, 122, 123, 124, 125, 126, 127, 128, 129, 130},
		},
	}

	for i := 1; i < 131; i++ {
		q.Data[model.Fingerprint(i)] = circonusllhist.NewHistogramWithoutLookups(circonusllhist.New(circonusllhist.NoLookup()))
	}
	q.Prune()

	expected := CachedQuery{
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{},
		DataByMetaData: map[FullMetadata][]model.Fingerprint{
			{Step: "1"}:      {1},
			{Step: "2"}:      {2},
			{Step: "3"}:      {3},
			{Step: "4"}:      {4},
			{Step: "5"}:      {5},
			{Step: "6-30"}:   {6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30},
			{Step: "31-130"}: {106, 107, 108, 109, 110, 111, 112, 113, 114, 115, 116, 117, 118, 119, 120, 121, 122, 123, 124, 125, 126, 127, 128, 129, 130},
		},
	}

	for i := 1; i < 31; i++ {
		expected.Data[model.Fingerprint(i)] = circonusllhist.NewHistogramWithoutLookups(circonusllhist.New(circonusllhist.NoLookup()))
	}

	for i := 106; i < 131; i++ {
		expected.Data[model.Fingerprint(i)] = circonusllhist.NewHistogramWithoutLookups(circonusllhist.New(circonusllhist.NoLookup()))
	}

	if diff := cmp.Diff(expected, q, dataComparer); diff != "" {
		t.Errorf("got incorrect state after pruning: %v", diff)
	}
}

func TestMetadataFor(t *testing.T) {
	var testCases = []struct {
		name           string
		pod, container string
		labels         map[string]string
		meta           FullMetadata
	}{
		{
			name:      "step pod",
			pod:       "pod",
			container: "container",
			labels: map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
				"ci.openshift.io/metadata.step":    "step",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Target:    "target",
				Step:      "step",
				Pod:       "pod",
				Container: "container",
			},
		},
		{
			name:      "build pod",
			pod:       "src-build",
			container: "container",
			labels: map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
				"openshift.io/build.name":          "src",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Pod:       "src-build",
				Container: "container",
			},
		},
		{
			name:      "release pod",
			pod:       "release-latest-cli",
			container: "container",
			labels: map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
				"ci.openshift.io/release":          "latest",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Pod:       "release-latest-cli",
				Container: "container",
			},
		},
		{
			name:      "RPM repo pod",
			pod:       "rpm-repo-5d88d4fc4c-jg2xb",
			container: "rpm-repo",
			labels: map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
				"app":                              "rpm-repo",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Container: "rpm-repo",
			},
		},
		{
			name:      "raw prowjob pod",
			pod:       "d316d4cc-a437-11eb-b35f-0a580a800e92",
			container: "container",
			labels: map[string]string{
				"created-by-prow":           "true",
				"prow.k8s.io/refs.org":      "org",
				"prow.k8s.io/refs.repo":     "repo",
				"prow.k8s.io/refs.base_ref": "branch",
				"prow.k8s.io/context":       "context",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				},
				Target:    "context",
				Container: "container",
			},
		},
		{
			name:      "raw periodic prowjob pod without context",
			pod:       "d316d4cc-a437-11eb-b35f-0a580a800e92",
			container: "container",
			labels: map[string]string{
				"created-by-prow":           "true",
				"prow.k8s.io/refs.org":      "org",
				"prow.k8s.io/refs.repo":     "repo",
				"prow.k8s.io/refs.base_ref": "branch",
				"prow.k8s.io/job":           "periodic-ci-org-repo-branch-context",
				"prow.k8s.io/type":          "periodic",
			},
			meta: FullMetadata{
				Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				},
				Target:    "context",
				Container: "container",
			},
		},
		{
			name:      "raw repo-less periodic prowjob pod without context",
			pod:       "d316d4cc-a437-11eb-b35f-0a580a800e92",
			container: "container",
			labels: map[string]string{
				"created-by-prow":  "true",
				"prow.k8s.io/job":  "periodic-handwritten-prowjob",
				"prow.k8s.io/type": "periodic",
			},
			meta: FullMetadata{
				Target:    "periodic-handwritten-prowjob",
				Container: "container",
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if diff := cmp.Diff(MetadataFor(testCase.labels, testCase.pod, testCase.container), testCase.meta); diff != "" {
				t.Errorf("%s: got incorrect metadata: %v", testCase.name, diff)
			}
		})
	}
}
