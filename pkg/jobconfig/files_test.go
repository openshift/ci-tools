package jobconfig

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "k8s.io/test-infra/prow/config"
)

func TestMergeConfigs(t *testing.T) {
	var testCases = []struct {
		name     string
		dest     *prowconfig.JobConfig
		part     *prowconfig.JobConfig
		expected *prowconfig.JobConfig
	}{
		{
			name:     "empty dest and empty part leads to empty result",
			dest:     &prowconfig.JobConfig{},
			part:     &prowconfig.JobConfig{},
			expected: &prowconfig.JobConfig{},
		},
		{
			name: "empty dest leads to copy of part",
			dest: &prowconfig.JobConfig{},
			part: &prowconfig.JobConfig{
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{Name: "test"}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{Name: "post-test"}}},
			},
			expected: &prowconfig.JobConfig{
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{Name: "test"}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{Name: "post-test"}}},
			},
		},
		{
			name: "empty part leads to dest",
			dest: &prowconfig.JobConfig{
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{Name: "test"}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{Name: "post-test"}}},
			},
			part: &prowconfig.JobConfig{},
			expected: &prowconfig.JobConfig{
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{Name: "test"}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{Name: "post-test"}}},
			},
		},
		{
			name: "data in both leads to merge",
			dest: &prowconfig.JobConfig{
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{Name: "test"}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{Name: "post-test"}}},
			},
			part: &prowconfig.JobConfig{
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{Name: "test-2"}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{Name: "post-test-2"}}},
			},
			expected: &prowconfig.JobConfig{
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{Name: "test"}, {Name: "test-2"}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{Name: "post-test"}, {Name: "post-test-2"}}},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mergeConfigs(testCase.dest, testCase.part)
			if actual, expected := testCase.dest, testCase.expected; !equality.Semantic.DeepEqual(actual, expected) {
				t.Errorf("%s: wanted to get %v, got %v", testCase.name, expected, actual)
			}
		})
	}
}

func TestMergeJobConfig(t *testing.T) {
	tests := []struct {
		allJobs                       sets.String
		destination, source, expected *prowconfig.JobConfig
	}{
		{
			allJobs:     sets.String{},
			destination: &prowconfig.JobConfig{},
			source: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "source-job", Context: "ci/prow/source"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "source-job", Context: "ci/prow/source"},
				}},
			},
		}, {
			allJobs: sets.String{},
			destination: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "another-job", Context: "ci/prow/another"},
				}},
			},
			source: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "source-job", Context: "ci/prow/source"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "source-job", Context: "ci/prow/source"},
					{Name: "another-job", Context: "ci/prow/another"},
				}},
			},
		}, {
			allJobs: sets.String{},
			destination: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "same-job", Context: "ci/prow/same"},
				}},
			},
			source: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "same-job", Context: "ci/prow/different"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{Name: "same-job", Context: "ci/prow/different"},
				}},
			},
		}, {
			allJobs:     sets.String{},
			destination: &prowconfig.JobConfig{},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "source-job", Agent: "ci/prow/source"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "source-job", Agent: "ci/prow/source"},
				}},
			},
		}, {
			allJobs: sets.String{},
			destination: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "another-job", Agent: "ci/prow/another"},
				}},
			},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "source-job", Agent: "ci/prow/source"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "source-job", Agent: "ci/prow/source"},
					{Name: "another-job", Agent: "ci/prow/another"},
				}},
			},
		}, {
			allJobs: sets.String{},
			destination: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/same"},
				}},
			},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/different"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/different"},
				}},
			},
		}, {
			allJobs: sets.String{},
			destination: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/same"},
				}},
			},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/same"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/same"},
				}},
			},
		}, {
			allJobs: sets.NewString("other-job"),
			destination: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/same"},
					{Name: "other-job", Agent: "ci/prow/same"},
					{Name: "old-job", Agent: "ci/prow/same"},
				}},
			},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/same"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{Name: "same-job", Agent: "ci/prow/same"},
					{Name: "old-job", Agent: "ci/prow/same"},
				}},
			},
		},
	}
	for _, tc := range tests {
		mergeJobConfig(tc.destination, tc.source, tc.allJobs)

		if !equality.Semantic.DeepEqual(tc.destination, tc.expected) {
			t.Errorf("expected merged job config diff:\n%s", diff.ObjectDiff(tc.expected, tc.destination))
		}
	}
}

func TestMergePresubmits(t *testing.T) {
	var testCases = []struct {
		name     string
		old, new *prowconfig.Presubmit
		expected prowconfig.Presubmit
	}{
		{
			name: "identical old and new returns identical",
			old: &prowconfig.Presubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				AlwaysRun:      true,
				RunIfChanged:   "foo",
				Context:        "context",
				SkipReport:     true,
				Optional:       true,
				Trigger:        "whatever",
				RerunCommand:   "something",
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
			new: &prowconfig.Presubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				AlwaysRun:      true,
				RunIfChanged:   "foo",
				Context:        "context",
				SkipReport:     true,
				Optional:       true,
				Trigger:        "whatever",
				RerunCommand:   "something",
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
			expected: prowconfig.Presubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				AlwaysRun:      true,
				RunIfChanged:   "foo",
				Context:        "context",
				SkipReport:     true,
				Optional:       true,
				Trigger:        "whatever",
				RerunCommand:   "something",
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
		},
		{
			name: "new can update fields in the old",
			old: &prowconfig.Presubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				AlwaysRun:      true,
				RunIfChanged:   "foo",
				Context:        "context",
				SkipReport:     true,
				Optional:       true,
				Trigger:        "whatever",
				RerunCommand:   "something",
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
			new: &prowconfig.Presubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "baz"},
				AlwaysRun:      true,
				RunIfChanged:   "foo",
				Context:        "contaxt",
				SkipReport:     true,
				Optional:       true,
				Trigger:        "whatever",
				RerunCommand:   "something",
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
			expected: prowconfig.Presubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "baz"},
				AlwaysRun:      true,
				RunIfChanged:   "foo",
				Context:        "contaxt",
				SkipReport:     true,
				Optional:       true,
				Trigger:        "whatever",
				RerunCommand:   "something",
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
		},
		{
			name: "new cannot update honored fields in old",
			old: &prowconfig.Presubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				AlwaysRun:      true,
				RunIfChanged:   "foo",
				Context:        "context",
				SkipReport:     true,
				Optional:       true,
				Trigger:        "whatever",
				RerunCommand:   "something",
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
			new: &prowconfig.Presubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				AlwaysRun:      false,
				RunIfChanged:   "whatever",
				Context:        "context",
				SkipReport:     false,
				Optional:       false,
				Trigger:        "whatever",
				RerunCommand:   "something",
				MaxConcurrency: 10000,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
			expected: prowconfig.Presubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				AlwaysRun:      true,
				RunIfChanged:   "foo",
				Context:        "context",
				SkipReport:     true,
				Optional:       true,
				Trigger:        "whatever",
				RerunCommand:   "something",
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := mergePresubmits(testCase.old, testCase.new), testCase.expected; !equality.Semantic.DeepEqual(actual, expected) {
				t.Errorf("%s: did not get expected merged presubmit config:\n%s", testCase.name, diff.ObjectDiff(actual, expected))
			}
		})
	}
}

func TestMergePostsubmits(t *testing.T) {
	var testCases = []struct {
		name     string
		old, new *prowconfig.Postsubmit
		expected prowconfig.Postsubmit
	}{
		{
			name: "identical old and new returns identical",
			old: &prowconfig.Postsubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
			new: &prowconfig.Postsubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
			expected: prowconfig.Postsubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
		},
		{
			name: "new can update fields in the old",
			old: &prowconfig.Postsubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
			new: &prowconfig.Postsubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "baz"},
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
			expected: prowconfig.Postsubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "baz"},
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
		},
		{
			name: "new cannot update honored fields in old",
			old: &prowconfig.Postsubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
			new: &prowconfig.Postsubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				MaxConcurrency: 10000,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
			expected: prowconfig.Postsubmit{
				Name:           "pull-ci-super-duper",
				Labels:         map[string]string{"foo": "bar"},
				MaxConcurrency: 10,
				Agent:          "agent",
				Cluster:        "somewhere",
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := mergePostsubmits(testCase.old, testCase.new), testCase.expected; !equality.Semantic.DeepEqual(actual, expected) {
				t.Errorf("%s: did not get expected merged postsubmit config:\n%s", testCase.name, diff.ObjectDiff(actual, expected))
			}
		})
	}
}
