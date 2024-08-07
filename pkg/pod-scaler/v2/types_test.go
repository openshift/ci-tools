package v2

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
		DataByMetaData: map[FullMetadata][]FingerprintTime{},
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
		DataByMetaData: map[FullMetadata][]FingerprintTime{
			metrics[0].meta: {
				{
					Fingerprint: metrics[0].metric.Fingerprint(),
					Added:       year(20),
				},
			},
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
		DataByMetaData: map[FullMetadata][]FingerprintTime{
			metrics[0].meta: {
				{
					Fingerprint: metrics[0].metric.Fingerprint(),
					Added:       year(20),
				},
			},
			metrics[1].meta: {
				{
					Fingerprint: metrics[1].metric.Fingerprint(),
					Added:       year(20),
				},
			},
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
		DataByMetaData: map[FullMetadata][]FingerprintTime{
			metrics[0].meta: {
				{
					Fingerprint: metrics[0].metric.Fingerprint(),
					Added:       year(20),
				},
			},
			metrics[1].meta: {
				{
					Fingerprint: metrics[1].metric.Fingerprint(),
					Added:       year(20),
				},
			},
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
		DataByMetaData: map[FullMetadata][]FingerprintTime{
			metrics[0].meta: {
				{
					Fingerprint: metrics[0].metric.Fingerprint(),
					Added:       year(20),
				},
			},
			metrics[1].meta: {
				{
					Fingerprint: metrics[1].metric.Fingerprint(),
					Added:       year(20),
				},
				{
					Fingerprint: metrics[2].metric.Fingerprint(),
					Added:       year(35),
				},
			},
		},
	}
	if diff := cmp.Diff(expected, q, dataComparer); diff != "" {
		t.Errorf("got incorrect state after fourth insertion: %v", diff)
	}
}

var dataComparer = cmp.Comparer(func(a, b *circonusllhist.HistogramWithoutLookups) bool {
	return a.Histogram().Equals(b.Histogram())
})

func TestCachedQuery_Prune_limitOverallFingerprints(t *testing.T) {
	q := CachedQuery{
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{},
		DataByMetaData: map[FullMetadata][]FingerprintTime{
			{Step: "1"}:      {ft(1)},
			{Step: "2"}:      {ft(2)},
			{Step: "3"}:      {ft(3)},
			{Step: "4"}:      {ft(4)},
			{Step: "5"}:      {ft(5)},
			{Step: "6-30"}:   {ft(6), ft(7), ft(8), ft(9), ft(10), ft(11), ft(12), ft(13), ft(14), ft(15), ft(16), ft(17), ft(18), ft(19), ft(20), ft(21), ft(22), ft(23), ft(24), ft(25), ft(26), ft(27), ft(28), ft(29), ft(30)},
			{Step: "31-130"}: {ft(31), ft(32), ft(33), ft(34), ft(35), ft(36), ft(37), ft(38), ft(39), ft(40), ft(41), ft(42), ft(43), ft(44), ft(45), ft(46), ft(47), ft(48), ft(49), ft(50), ft(51), ft(52), ft(53), ft(54), ft(55), ft(56), ft(57), ft(58), ft(59), ft(60), ft(61), ft(62), ft(63), ft(64), ft(65), ft(66), ft(67), ft(68), ft(69), ft(70), ft(71), ft(72), ft(73), ft(74), ft(75), ft(76), ft(77), ft(78), ft(79), ft(80), ft(81), ft(82), ft(83), ft(84), ft(85), ft(86), ft(87), ft(88), ft(89), ft(90), ft(91), ft(92), ft(93), ft(94), ft(95), ft(96), ft(97), ft(98), ft(99), ft(100), ft(101), ft(102), ft(103), ft(104), ft(105), ft(106), ft(107), ft(108), ft(109), ft(110), ft(111), ft(112), ft(113), ft(114), ft(115), ft(116), ft(117), ft(118), ft(119), ft(120), ft(121), ft(122), ft(123), ft(124), ft(125), ft(126), ft(127), ft(128), ft(129), ft(130)},
		},
	}

	for i := 1; i < 131; i++ {
		q.Data[model.Fingerprint(i)] = circonusllhist.NewHistogramWithoutLookups(circonusllhist.New(circonusllhist.NoLookup()))
	}
	// invoke prune with a date that will not prune any data for simply being old
	q.prune(year(20).Add(-24 * time.Hour))

	expected := CachedQuery{
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{},
		DataByMetaData: map[FullMetadata][]FingerprintTime{
			{Step: "1"}:      {ft(1)},
			{Step: "2"}:      {ft(2)},
			{Step: "3"}:      {ft(3)},
			{Step: "4"}:      {ft(4)},
			{Step: "5"}:      {ft(5)},
			{Step: "6-30"}:   {ft(6), ft(7), ft(8), ft(9), ft(10), ft(11), ft(12), ft(13), ft(14), ft(15), ft(16), ft(17), ft(18), ft(19), ft(20), ft(21), ft(22), ft(23), ft(24), ft(25), ft(26), ft(27), ft(28), ft(29), ft(30)},
			{Step: "31-130"}: {ft(106), ft(107), ft(108), ft(109), ft(110), ft(111), ft(112), ft(113), ft(114), ft(115), ft(116), ft(117), ft(118), ft(119), ft(120), ft(121), ft(122), ft(123), ft(124), ft(125), ft(126), ft(127), ft(128), ft(129), ft(130)},
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

// ft generates a FingerprintTime for the supplied int representation of a fingerprint
func ft(fingerprint int) FingerprintTime {
	return FingerprintTime{
		Fingerprint: model.Fingerprint(fingerprint),
		Added:       year(20),
	}
}

func TestCachedQuery_Prune_removeOldFingerprints(t *testing.T) {
	now := time.Now()
	q := CachedQuery{
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{},
		DataByMetaData: map[FullMetadata][]FingerprintTime{
			{Step: "a"}: {
				fta(1, now),
				fta(2, now.Add(-25*time.Hour)), // Should be pruned
			},
			{Step: "b"}: {
				fta(3, now.Add(1*time.Hour)),
				fta(4, now.Add(-25*time.Hour)), // Should be pruned
				fta(5, now.Add(-23*time.Hour)),
				fta(6, now.Add(-80*24*time.Hour)), // Should be pruned
			},
		},
	}

	for i := 1; i < 7; i++ {
		q.Data[model.Fingerprint(i)] = circonusllhist.NewHistogramWithoutLookups(circonusllhist.New(circonusllhist.NoLookup()))
	}

	// Prune any data that was added longer than 1 day ago
	q.prune(now.Add(-24 * time.Hour))

	expected := CachedQuery{
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
			model.Fingerprint(1): circonusllhist.NewHistogramWithoutLookups(circonusllhist.New(circonusllhist.NoLookup())),
			model.Fingerprint(3): circonusllhist.NewHistogramWithoutLookups(circonusllhist.New(circonusllhist.NoLookup())),
			model.Fingerprint(5): circonusllhist.NewHistogramWithoutLookups(circonusllhist.New(circonusllhist.NoLookup())),
		},
		DataByMetaData: map[FullMetadata][]FingerprintTime{
			{Step: "a"}: {fta(1, now)},
			{Step: "b"}: {fta(3, now.Add(1*time.Hour)), fta(5, now.Add(-23*time.Hour))},
		},
	}

	if diff := cmp.Diff(expected, q, dataComparer); diff != "" {
		t.Errorf("got incorrect state after pruning: %v", diff)
	}
}

// fta generates a FingerprintTime for the supplied int representation of a fingerprint, and the added time
func fta(fingerprint int, added time.Time) FingerprintTime {
	return FingerprintTime{
		Fingerprint: model.Fingerprint(fingerprint),
		Added:       added,
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
