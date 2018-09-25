package jobconfig

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/diff"
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
		destination, source, expected *prowconfig.JobConfig
	}{
		{
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
		},
	}
	for _, tc := range tests {
		mergeJobConfig(tc.destination, tc.source)

		if !equality.Semantic.DeepEqual(tc.destination, tc.expected) {
			t.Errorf("expected merged job config diff:\n%s", diff.ObjectDiff(tc.expected, tc.destination))
		}
	}
}
