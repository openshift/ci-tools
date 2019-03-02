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
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test"}}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test"}}}},
			},
			expected: &prowconfig.JobConfig{
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test"}}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test"}}}},
			},
		},
		{
			name: "empty part leads to dest",
			dest: &prowconfig.JobConfig{
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test"}}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test"}}}},
			},
			part: &prowconfig.JobConfig{},
			expected: &prowconfig.JobConfig{
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test"}}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test"}}}},
			},
		},
		{
			name: "data in both leads to merge",
			dest: &prowconfig.JobConfig{
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test"}}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test"}}}},
			},
			part: &prowconfig.JobConfig{
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test-2"}}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test-2"}}}},
			},
			expected: &prowconfig.JobConfig{
				Presubmits:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test"}}, {JobBase: prowconfig.JobBase{Name: "test-2"}}}},
				Postsubmits: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test"}}, {JobBase: prowconfig.JobBase{Name: "post-test-2"}}}},
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
					{JobBase: prowconfig.JobBase{Name: "source-job"}, Context: "ci/prow/source"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job"}, Context: "ci/prow/source"},
				}},
			},
		}, {
			allJobs: sets.String{},
			destination: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "another-job"}, Context: "ci/prow/another"},
				}},
			},
			source: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job"}, Context: "ci/prow/source"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job"}, Context: "ci/prow/source"},
					{JobBase: prowconfig.JobBase{Name: "another-job"}, Context: "ci/prow/another"},
				}},
			},
		}, {
			allJobs: sets.String{},
			destination: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job"}, Context: "ci/prow/same"},
				}},
			},
			source: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job"}, Context: "ci/prow/different"},
				}},
			},
			expected: &prowconfig.JobConfig{
				Presubmits: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job"}, Context: "ci/prow/different"},
				}},
			},
		}, {
			allJobs:     sets.String{},
			destination: &prowconfig.JobConfig{},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job", Agent: "ci/prow/source"}},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job", Agent: "ci/prow/source"}},
				}},
			},
		}, {
			allJobs: sets.String{},
			destination: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "another-job", Agent: "ci/prow/another"}},
				}},
			},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job", Agent: "ci/prow/source"}},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job", Agent: "ci/prow/source"}},
					{JobBase: prowconfig.JobBase{Name: "another-job", Agent: "ci/prow/another"}},
				}},
			},
		}, {
			allJobs: sets.String{},
			destination: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
				}},
			},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/different"}},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/different"}},
				}},
			},
		}, {
			allJobs: sets.String{},
			destination: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
				}},
			},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
				}},
			},
		}, {
			allJobs: sets.NewString("other-job"),
			destination: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
					{JobBase: prowconfig.JobBase{Name: "other-job", Agent: "ci/prow/same"}},
					{JobBase: prowconfig.JobBase{Name: "old-job", Agent: "ci/prow/same"}},
				}},
			},
			source: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
				}},
			},
			expected: &prowconfig.JobConfig{
				Postsubmits: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
					{JobBase: prowconfig.JobBase{Name: "old-job", Agent: "ci/prow/same"}},
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
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Agent:          "agent",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10,
					Cluster:        "somewhere",
				},
				AlwaysRun:           true,
				Context:             "context",
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
				SkipReport:          true,
				Optional:            true,
				Trigger:             "whatever",
				RerunCommand:        "something",
			},
			new: &prowconfig.Presubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Agent:          "agent",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10,
					Cluster:        "somewhere",
				},
				AlwaysRun:           true,
				Context:             "context",
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
				SkipReport:          true,
				Optional:            true,
				Trigger:             "whatever",
				RerunCommand:        "something",
			},
			expected: prowconfig.Presubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Agent:          "agent",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10,
					Cluster:        "somewhere",
				},
				AlwaysRun:           true,
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
				Context:             "context",
				SkipReport:          true,
				Optional:            true,
				Trigger:             "whatever",
				RerunCommand:        "something",
			},
		},
		{
			name: "new can update fields in the old",
			old: &prowconfig.Presubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Agent:          "agent",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10,
					Cluster:        "somewhere",
				},
				AlwaysRun:           true,
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
				Context:             "context",
				SkipReport:          true,
				Optional:            true,
				Trigger:             "whatever",
				RerunCommand:        "something",
			},
			new: &prowconfig.Presubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Agent:          "agent",
					Labels:         map[string]string{"foo": "baz"},
					MaxConcurrency: 10,
					Cluster:        "somewhere",
				},
				AlwaysRun:           true,
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
				Context:             "contaxt",
				SkipReport:          true,
				Optional:            true,
				Trigger:             "whatever",
				RerunCommand:        "something",
			},
			expected: prowconfig.Presubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Agent:          "agent",
					Labels:         map[string]string{"foo": "baz"},
					MaxConcurrency: 10,
					Cluster:        "somewhere",
				},
				AlwaysRun:           true,
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
				Context:             "contaxt",
				SkipReport:          true,
				Optional:            true,
				Trigger:             "whatever",
				RerunCommand:        "something",
			},
		},
		{
			name: "new cannot update honored fields in old",
			old: &prowconfig.Presubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Agent:          "agent",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10,
					Cluster:        "somewhere",
				},
				AlwaysRun:           true,
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
				Context:             "context",
				SkipReport:          true,
				Optional:            true,
				Trigger:             "whatever",
				RerunCommand:        "something",
			},
			new: &prowconfig.Presubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Agent:          "agent",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10000,
					Cluster:        "somewhere",
				},
				AlwaysRun:           false,
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "whatever"},
				Context:             "context",
				SkipReport:          false,
				Optional:            false,
				Trigger:             "whatever",
				RerunCommand:        "something",
			},
			expected: prowconfig.Presubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Agent:          "agent",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10,
					Cluster:        "somewhere",
				},
				AlwaysRun:           true,
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
				Context:             "context",
				SkipReport:          true,
				Optional:            true,
				Trigger:             "whatever",
				RerunCommand:        "something",
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
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
			new: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
			expected: prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
		},
		{
			name: "new can update fields in the old",
			old: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
			new: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{Name: "pull-ci-super-duper",
					Labels:         map[string]string{"foo": "baz"},
					MaxConcurrency: 10,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
			expected: prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{Name: "pull-ci-super-duper",
					Labels:         map[string]string{"foo": "baz"},
					MaxConcurrency: 10,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
		},
		{
			name: "new cannot update honored fields in old",
			old: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{Name: "pull-ci-super-duper",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
			new: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{Name: "pull-ci-super-duper",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10000,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
			expected: prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{Name: "pull-ci-super-duper",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 10,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
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

func TestExtractRepoElementsFromPath(t *testing.T) {
	var testCases = []struct {
		name          string
		path          string
		expected      *Info
		expectedError bool
	}{
		{
			name: "simple path parses fine",
			path: "./org/repo/org-repo-branch-presubmits.yaml",
			expected: &Info{
				Org:      "org",
				Repo:     "repo",
				Branch:   "branch",
				Type:     "presubmits",
				Filename: "./org/repo/org-repo-branch-presubmits.yaml",
			},
			expectedError: false,
		},
		{
			name:          "empty path fails to parse",
			path:          "",
			expected:      nil,
			expectedError: true,
		},
		{
			name: "prefix to a valid path parses fine",
			path: "./something/crazy/org/repo/org-repo-branch-presubmits.yaml",
			expected: &Info{
				Org:      "org",
				Repo:     "repo",
				Branch:   "branch",
				Type:     "presubmits",
				Filename: "./something/crazy/org/repo/org-repo-branch-presubmits.yaml",
			},
			expectedError: false,
		},
		{
			name:          "too few nested directories fails to parse",
			path:          "./repo/org-repo-branch-presubmits.yaml",
			expected:      nil,
			expectedError: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			elements, err := extractInfoFromPath(testCase.path)
			if err == nil && testCase.expectedError {
				t.Errorf("%s: expected an error, but got none", testCase.name)
			}
			if err != nil && !testCase.expectedError {
				t.Errorf("%s: expected no error, but got one: %v", testCase.name, err)
			}
			if actual, expected := elements, testCase.expected; !equality.Semantic.DeepEqual(actual, expected) {
				t.Errorf("%s: did not get expected repo info from path:\n%s", testCase.name, diff.ObjectDiff(actual, expected))
			}
		})
	}
}
