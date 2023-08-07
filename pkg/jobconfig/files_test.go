package jobconfig

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "k8s.io/test-infra/prow/config"
)

var unexportedFields = []cmp.Option{
	cmpopts.IgnoreUnexported(prowconfig.Presubmit{}),
	cmpopts.IgnoreUnexported(prowconfig.Periodic{}),
	cmpopts.IgnoreUnexported(prowconfig.Brancher{}),
	cmpopts.IgnoreUnexported(prowconfig.RegexpChangeMatcher{}),
}

func TestAppend(t *testing.T) {
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
				PresubmitsStatic:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test"}}}},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test"}}}},
				Periodics:         []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "periodic-test"}}},
			},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test"}}}},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test"}}}},
				Periodics:         []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "periodic-test"}}},
			},
		},
		{
			name: "empty part leads to dest",
			dest: &prowconfig.JobConfig{
				PresubmitsStatic:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test"}}}},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test"}}}},
				Periodics:         []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "periodic-test"}}},
			},
			part: &prowconfig.JobConfig{},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test"}}}},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test"}}}},
				Periodics:         []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "periodic-test"}}},
			},
		},
		{
			name: "data in both leads to merge",
			dest: &prowconfig.JobConfig{
				PresubmitsStatic:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test"}}}},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test"}}}},
				Periodics:         []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "periodic-test"}}},
			},
			part: &prowconfig.JobConfig{
				PresubmitsStatic:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test-2"}}}},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test-2"}}}},
				Periodics:         []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "periodic-test-2"}}},
			},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic:  map[string][]prowconfig.Presubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "test"}}, {JobBase: prowconfig.JobBase{Name: "test-2"}}}},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"super/duper": {{JobBase: prowconfig.JobBase{Name: "post-test"}}, {JobBase: prowconfig.JobBase{Name: "post-test-2"}}}},
				Periodics:         []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "periodic-test"}}, {JobBase: prowconfig.JobBase{Name: "periodic-test-2"}}},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			Append(testCase.dest, testCase.part)
			if actual, expected := testCase.dest, testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: wanted to get %v, got %v", testCase.name, expected, actual)
			}
		})
	}
}

func TestMergeJobConfig(t *testing.T) {
	tests := []struct {
		allJobs                       sets.Set[string]
		destination, source, expected *prowconfig.JobConfig
	}{
		{
			allJobs:     sets.Set[string]{},
			destination: &prowconfig.JobConfig{},
			source: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job"}, Reporter: prowconfig.Reporter{Context: "ci/prow/source"}},
				}},
			},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job"}, Reporter: prowconfig.Reporter{Context: "ci/prow/source"}},
				}},
			},
		}, {
			allJobs: sets.Set[string]{},
			destination: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "another-job"}, Reporter: prowconfig.Reporter{Context: "ci/prow/another"}},
				}},
			},
			source: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job"}, Reporter: prowconfig.Reporter{Context: "ci/prow/source"}},
				}},
			},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job"}, Reporter: prowconfig.Reporter{Context: "ci/prow/source"}},
					{JobBase: prowconfig.JobBase{Name: "another-job"}, Reporter: prowconfig.Reporter{Context: "ci/prow/another"}},
				}},
			},
		}, {
			allJobs: sets.Set[string]{},
			destination: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job"}, Reporter: prowconfig.Reporter{Context: "ci/prow/same"}},
				}},
			},
			source: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job"}, Reporter: prowconfig.Reporter{Context: "ci/prow/different"}},
				}},
			},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job"}, Reporter: prowconfig.Reporter{Context: "ci/prow/different"}},
				}},
			},
		}, {
			allJobs:     sets.Set[string]{},
			destination: &prowconfig.JobConfig{},
			source: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job", Agent: "ci/prow/source"}},
				}},
			},
			expected: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job", Agent: "ci/prow/source"}},
				}},
			},
		}, {
			allJobs: sets.Set[string]{},
			destination: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "another-job", Agent: "ci/prow/another"}},
				}},
			},
			source: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job", Agent: "ci/prow/source"}},
				}},
			},
			expected: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "source-job", Agent: "ci/prow/source"}},
					{JobBase: prowconfig.JobBase{Name: "another-job", Agent: "ci/prow/another"}},
				}},
			},
		}, {
			allJobs: sets.Set[string]{},
			destination: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
				}},
			},
			source: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/different"}},
				}},
			},
			expected: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/different"}},
				}},
			},
		}, {
			allJobs: sets.Set[string]{},
			destination: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
				}},
			},
			source: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
				}},
			},
			expected: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
				}},
			},
		}, {
			allJobs: sets.New[string]("other-job"),
			destination: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
					{JobBase: prowconfig.JobBase{Name: "other-job", Agent: "ci/prow/same"}},
					{JobBase: prowconfig.JobBase{Name: "old-job", Agent: "ci/prow/same"}},
				}},
			},
			source: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
				}},
			},
			expected: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {
					{JobBase: prowconfig.JobBase{Name: "same-job", Agent: "ci/prow/same"}},
					{JobBase: prowconfig.JobBase{Name: "old-job", Agent: "ci/prow/same"}},
				}},
			},
		},
	}
	for _, tc := range tests {
		mergeJobConfig(tc.destination, tc.source, tc.allJobs)

		if diff := cmp.Diff(tc.expected, tc.destination, unexportedFields...); diff != "" {
			t.Errorf("expected merged job config diff: %s", diff)
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
				AlwaysRun: true,
				Reporter: prowconfig.Reporter{
					Context:    "context",
					SkipReport: true,
				},
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
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
				AlwaysRun: true,
				Reporter: prowconfig.Reporter{
					Context:    "context",
					SkipReport: true,
				},
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
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
				AlwaysRun: true,
				Reporter: prowconfig.Reporter{
					Context:    "context",
					SkipReport: true,
				},
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
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
				AlwaysRun: true,
				Reporter: prowconfig.Reporter{
					Context:    "context",
					SkipReport: true,
				},
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
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
				AlwaysRun: true,
				Reporter: prowconfig.Reporter{
					Context:    "contaxt",
					SkipReport: true,
				},
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
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
				AlwaysRun: true,
				Reporter: prowconfig.Reporter{
					Context:    "contaxt",
					SkipReport: true,
				},
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
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
				AlwaysRun: true,
				Reporter: prowconfig.Reporter{
					Context:    "context",
					SkipReport: true,
				},
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "foo"},
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
				AlwaysRun: false,
				Reporter: prowconfig.Reporter{
					Context:    "context",
					SkipReport: false,
				},
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "whatever"},
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
				AlwaysRun: false,
				Reporter: prowconfig.Reporter{
					Context:    "context",
					SkipReport: true,
				},
				RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "whatever"},
				Optional:            false,
				Trigger:             "whatever",
				RerunCommand:        "something",
			},
		},
		{
			name:     "Run if changed from new takes precedence",
			old:      &prowconfig.Presubmit{RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "old"}},
			new:      &prowconfig.Presubmit{RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "new"}},
			expected: prowconfig.Presubmit{RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{RunIfChanged: "new"}},
		},
		{
			name:     "Skip if only changed from new takes precedence",
			old:      &prowconfig.Presubmit{RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{SkipIfOnlyChanged: "old"}},
			new:      &prowconfig.Presubmit{RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{SkipIfOnlyChanged: "new"}},
			expected: prowconfig.Presubmit{RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{SkipIfOnlyChanged: "new"}},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result := mergePresubmits(testCase.old, testCase.new)
			if diff := cmp.Diff(testCase.expected, result, unexportedFields...); diff != "" {
				t.Errorf("result differs from expected: %s", diff)
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
		{
			name: "job with promotion label and MaxConcurrency 1",
			old: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Labels:         map[string]string{"foo": "bar"},
					MaxConcurrency: 3,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
			new: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Labels:         map[string]string{"foo": "bar", "ci-operator.openshift.io/is-promotion": "bla"},
					MaxConcurrency: 1,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
			expected: prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					Labels:         map[string]string{"foo": "bar", "ci-operator.openshift.io/is-promotion": "bla"},
					MaxConcurrency: 1,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
		},
		{
			name: "job without promotion label",
			old: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					MaxConcurrency: 3,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
			new: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					MaxConcurrency: 1,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
			expected: prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Name:           "pull-ci-super-duper",
					MaxConcurrency: 3,
					Agent:          "agent",
					Cluster:        "somewhere",
				},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result := mergePostsubmits(testCase.old, testCase.new)
			if diff := cmp.Diff(testCase.expected, result, unexportedFields...); diff != "" {
				t.Errorf("%s: did not get expected merged postsubmit config: %s", testCase.name, diff)
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
			name: "simple periodic path parses fine",
			path: "./org/repo/org-repo-branch-periodics.yaml",
			expected: &Info{
				Org:      "org",
				Repo:     "repo",
				Branch:   "branch",
				Type:     "periodics",
				Filename: "./org/repo/org-repo-branch-periodics.yaml",
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
			if diff := cmp.Diff(testCase.expected, elements); diff != "" {
				t.Errorf("%s: did not get expected repo info from path: %s", testCase.name, diff)
			}
		})
	}
}

func TestInfo_Basename(t *testing.T) {
	testCases := []struct {
		name     string
		info     *Info
		expected string
	}{
		{
			name: "simple path creates simple basename",
			info: &Info{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
				Type:   "presubmits",
			},
			expected: "org-repo-branch-presubmits.yaml",
		},
		{
			name: "path for periodics without branch creates complex basename",
			info: &Info{
				Org:    "org",
				Repo:   "repo",
				Branch: "",
				Type:   "periodics",
			},
			expected: "org-repo-periodics.yaml",
		},
		{
			name: "path for periodics with branch creates simple basename",
			info: &Info{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
				Type:   "periodics",
			},
			expected: "org-repo-branch-periodics.yaml",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.expected, func(t *testing.T) {
			if diff := cmp.Diff(testCase.expected, testCase.info.Basename()); diff != "" {
				t.Errorf("%s: didn't get correct basename: %v", testCase.name, diff)
			}
		})
	}
}

func TestInfo_ConfigMapName(t *testing.T) {
	testCases := []struct {
		name     string
		branch   string
		jobType  string
		expected string
	}{
		{
			name:     "master branch goes to master configmap",
			branch:   "master",
			jobType:  "presubmits",
			expected: "job-config-master-presubmits",
		},
		{
			name:     "main branch goes to main configmap",
			branch:   "main",
			jobType:  "presubmits",
			expected: "job-config-main-presubmits",
		},
		{
			name:     "master branch goes to master configmap",
			branch:   "master",
			jobType:  "postsubmits",
			expected: "job-config-master-postsubmits",
		},
		{
			name:     "main branch goes to master configmap",
			branch:   "main",
			jobType:  "postsubmits",
			expected: "job-config-main-postsubmits",
		},
		{
			name:     "periodic without relationship to a repo goes to misc",
			branch:   "",
			jobType:  "periodics",
			expected: "job-config-misc",
		},
		{
			name:     "periodic with relationship to a repo master branch goes to branch shard",
			branch:   "master",
			jobType:  "periodics",
			expected: "job-config-master-periodics",
		},
		{
			name:     "periodic with relationship to a repo main branch goes to branch shard",
			branch:   "main",
			jobType:  "periodics",
			expected: "job-config-main-periodics",
		},
		{
			name:     "periodic with relationship to a repo branch goes to branch shard",
			branch:   "release-3.11",
			jobType:  "periodics",
			expected: "job-config-3.x",
		},
		{
			name:     "enterprise 3.6 branch goes to 3.x configmap",
			branch:   "enterprise-3.6",
			expected: "job-config-3.x",
		},
		{
			name:     "openshift 3.6 branch goes to 3.x configmap",
			branch:   "openshift-3.6",
			expected: "job-config-3.x",
		},
		{
			name:     "release 3.11 branch goes to 3.x configmap",
			branch:   "release-3.11",
			expected: "job-config-3.x",
		},
		{
			name:     "enterprise 3.11 branch goes to 3.x configmap",
			branch:   "enterprise-3.11",
			expected: "job-config-3.x",
		},
		{
			name:     "openshift 3.11 branch goes to 3.x configmap",
			branch:   "openshift-3.11",
			expected: "job-config-3.x",
		},
		{
			name:     "release 3.11 branch goes to 3.x configmap",
			branch:   "release-3.11",
			expected: "job-config-3.x",
		},
		{
			name:     "knative release branch goes to misc configmap",
			branch:   "release-0.2",
			expected: "job-config-misc",
		},
		{
			name:     "azure release branch goes to misc configmap",
			branch:   "release-v1",
			expected: "job-config-misc",
		},
		{
			name:     "ansible dev branch goes to misc configmap",
			branch:   "devel-40",
			expected: "job-config-misc",
		},
		{
			name:     "release 4.0 branch goes to 4.0 configmap",
			branch:   "release-4.0",
			expected: "job-config-4.0",
		},
		{
			name:     "release 4.1 branch goes to 4.1 configmap",
			branch:   "release-4.1",
			expected: "job-config-4.1",
		},
		{
			name:     "release 4.2 branch goes to 4.2 configmap",
			branch:   "release-4.2",
			expected: "job-config-4.2",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.expected, func(t *testing.T) {
			info := Info{Branch: testCase.branch, Type: testCase.jobType}
			if diff := cmp.Diff(testCase.expected, info.ConfigMapName()); diff != "" {
				t.Errorf("%s: didn't get correct basename: %v", testCase.name, diff)
			}
		})
	}
}

func TestPrune(t *testing.T) {
	testCases := []struct {
		name      string
		jobconfig *prowconfig.JobConfig
		Generator
		pruneLabels    labels.Set
		expectedConfig *prowconfig.JobConfig
	}{
		{
			name: "stale generated presubmit is pruned",
			jobconfig: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{LabelGenerator: "prowgen"}}}},
				},
			},
			Generator:      "prowgen",
			expectedConfig: &prowconfig.JobConfig{},
		},
		{
			name: "stale generated postsubmit is pruned",
			jobconfig: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{LabelGenerator: "prowgen"}}}},
				},
			},
			Generator:      "prowgen",
			expectedConfig: &prowconfig.JobConfig{},
		},
		{
			name: "not stale generated presubmit is kept",
			jobconfig: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{"prowgen": string(newlyGenerated)}}}},
				},
			},
			Generator: "prowgen",
			expectedConfig: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{"prowgen": string(newlyGenerated)}}}},
				},
			},
		},
		{
			name: "not stale generated postsubmit is kept",
			jobconfig: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{"prowgen": string(newlyGenerated)}}}},
				},
			},
			Generator: "prowgen",
			expectedConfig: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{"prowgen": string(newlyGenerated)}}}},
				},
			},
		},
		{
			name: "not generated presubmit is kept",
			jobconfig: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Name: "job"}}},
				},
			},
			Generator: "prowgen",
			expectedConfig: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Name: "job"}}},
				},
			},
		},
		{
			name: "not generated postsubmit is kept",
			jobconfig: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
					"repo": {{JobBase: prowconfig.JobBase{Name: "job"}}},
				},
			},
			Generator: "prowgen",
			expectedConfig: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
					"repo": {{JobBase: prowconfig.JobBase{Name: "job"}}},
				},
			},
		},
		{
			name: "periodics are kept",
			jobconfig: &prowconfig.JobConfig{
				Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{
					Name:   "job",
					Labels: map[string]string{"prowgen": string(newlyGenerated)}}}},
			},
			Generator: "prowgen",
			expectedConfig: &prowconfig.JobConfig{
				Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{
					Name:   "job",
					Labels: map[string]string{"prowgen": string(newlyGenerated)}}}},
			},
		},
		{
			name: "other tool's generated periodics are kept",
			jobconfig: &prowconfig.JobConfig{
				Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{
					Name:   "job",
					Labels: map[string]string{"prowgen": "true"}}}},
			},
			Generator: "cluster-init",
			expectedConfig: &prowconfig.JobConfig{
				Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{
					Name:   "job",
					Labels: map[string]string{"prowgen": "true"}}}},
			},
		},
		{
			name: "stale generated periodics are pruned",
			jobconfig: &prowconfig.JobConfig{
				Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{
					Name:   "job",
					Labels: map[string]string{LabelBuildFarm: "existingCluster", LabelGenerator: "cluster-init"}}}},
			},
			Generator:      "cluster-init",
			pruneLabels:    map[string]string{LabelBuildFarm: "existingCluster"},
			expectedConfig: &prowconfig.JobConfig{},
		},
		{
			name: "non matching generated periodics are kept",
			jobconfig: &prowconfig.JobConfig{
				Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{
					Name:   "job",
					Labels: map[string]string{LabelBuildFarm: "existingCluster", LabelGenerator: "cluster-init"}}}},
			},
			Generator:   "cluster-init",
			pruneLabels: map[string]string{LabelBuildFarm: "newCluster"},
			expectedConfig: &prowconfig.JobConfig{
				Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{
					Name:   "job",
					Labels: map[string]string{LabelBuildFarm: "existingCluster", LabelGenerator: "cluster-init"}}}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pruned, err := Prune(tc.jobconfig, tc.Generator, tc.pruneLabels)
			if err != nil {
				t.Fatalf("received error %v", err)
			}
			if diff := cmp.Diff(tc.expectedConfig, pruned, unexportedFields...); diff != "" {
				t.Fatalf("Pruned config differs:\n%s", diff)
			}
		})
	}
}

func TestIsGenerated(t *testing.T) {
	testCases := []struct {
		description string
		labels      map[string]string
		generator   Generator
		expected    bool
	}{
		{
			description: "job without any labels is not generated",
			generator:   "prowgen",
			expected:    false,
		},
		{
			description: "job without the generated label is not generated",
			labels:      map[string]string{"some-label": "some-value"},
			generator:   "prowgen",
			expected:    false,
		},
		{
			description: "job with the generated label is generated",
			labels:      map[string]string{LabelGenerator: "prowgen"},
			expected:    true,
			generator:   "prowgen",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			generated, err := IsGenerated(prowconfig.JobBase{Labels: tc.labels}, tc.generator)
			if err != nil {
				t.Fatalf("received error %v", err)
			}
			if generated != tc.expected {
				t.Fatalf("%s: expected %t, got %t", tc.description, tc.expected, generated)
			}
		})
	}
}

func TestStaleSelectorFor(t *testing.T) {
	testCases := []struct {
		description string
		labels      map[string]string
		generator   Generator
		pruneLabels labels.Set
		expected    bool
	}{
		{
			description: "job without any labels and expecting some label is not stale",
			generator:   "prowgen",
			expected:    false,
		},
		{
			description: "job with expected label is stale",
			labels:      map[string]string{LabelGenerator: "prowgen"},
			generator:   "prowgen",
			expected:    true,
		},
		{
			description: "job with expected label, newly generated, is not stale",
			labels:      map[string]string{"prowgen": string(newlyGenerated)},
			generator:   "prowgen",
			expected:    false,
		},
		{
			description: "job with label other than expected label, is not stale",
			labels:      map[string]string{"prowgen": string(newlyGenerated)},
			generator:   "cluster-init",
			expected:    false,
		},
		{
			description: "job with existing cluster label is not stale",
			labels:      map[string]string{LabelGenerator: "cluster-init", LabelBuildFarm: "existingCluster"},
			generator:   "cluster-init",
			pruneLabels: map[string]string{LabelBuildFarm: "newCluster"},
			expected:    false,
		},
		{
			description: "job with passed in cluster label is stale",
			labels:      map[string]string{LabelGenerator: "cluster-init", LabelBuildFarm: "newCluster"},
			generator:   "cluster-init",
			pruneLabels: map[string]string{LabelBuildFarm: "newCluster"},
			expected:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			staleSelector, err := staleSelectorFor(tc.generator, tc.pruneLabels)
			if err != nil {
				t.Fatalf("received error %v", err)
			}
			stale := staleSelector.Matches(labels.Set(tc.labels))
			if stale != tc.expected {
				t.Fatalf("%s: expected %t, got %t", tc.description, tc.expected, stale)
			}
		})
	}
}
