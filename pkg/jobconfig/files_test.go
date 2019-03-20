package jobconfig

import (
	"reflect"
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
			t.Errorf("expected merged job config diff:\n%s", diff.ObjectReflectDiff(tc.expected, tc.destination))
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
				t.Errorf("%s: did not get expected merged presubmit config:\n%s", testCase.name, diff.ObjectReflectDiff(actual, expected))
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
				t.Errorf("%s: did not get expected merged postsubmit config:\n%s", testCase.name, diff.ObjectReflectDiff(actual, expected))
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
				t.Errorf("%s: did not get expected repo info from path:\n%s", testCase.name, diff.ObjectReflectDiff(actual, expected))
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
	}
	for _, testCase := range testCases {
		t.Run(testCase.expected, func(t *testing.T) {
			if actual, expected := testCase.info.Basename(), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: didn't get correct basename: %v", testCase.name, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}
}

func TestInfo_ConfigMapName(t *testing.T) {
	testCases := []struct {
		name     string
		branch   string
		expected string
	}{
		{
			name:     "master branch goes to master configmap",
			branch:   "master",
			expected: "job-config-master",
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
			info := Info{Branch: testCase.branch}
			if actual, expected := info.ConfigMapName(), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: didn't get correct basename: %v", testCase.name, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}
}

func TestLabelGeneratedJobs(t *testing.T) {
	testCases := []struct {
		description       string
		jobconfig         *prowconfig.JobConfig
		label             string
		expectedJobConfig *prowconfig.JobConfig
	}{{
		description: "jobs without a generated label are left alone",
		jobconfig: &prowconfig.JobConfig{
			Presubmits: map[string][]prowconfig.Presubmit{"org/repo": {
				prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "org-repo-presubmit"}},
			}},
			Postsubmits: map[string][]prowconfig.Postsubmit{"org/repo": {
				prowconfig.Postsubmit{JobBase: prowconfig.JobBase{Name: "org-repo-postsubmit"}},
			}},
		},
		label: "generated-label",
		expectedJobConfig: &prowconfig.JobConfig{
			Presubmits: map[string][]prowconfig.Presubmit{"org/repo": {
				prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "org-repo-presubmit"}},
			}},
			Postsubmits: map[string][]prowconfig.Postsubmit{"org/repo": {
				prowconfig.Postsubmit{JobBase: prowconfig.JobBase{Name: "org-repo-postsubmit"}},
			}},
		},
	}, {
		description: "jobs with a generated label have the label set to a given value",
		jobconfig: &prowconfig.JobConfig{
			Presubmits: map[string][]prowconfig.Presubmit{"org/repo": {
				prowconfig.Presubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-presubmit",
					Labels: map[string]string{ProwJobLabelGenerated: Generated},
				}},
			}},
			Postsubmits: map[string][]prowconfig.Postsubmit{"org/repo": {
				prowconfig.Postsubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-postsubmit",
					Labels: map[string]string{ProwJobLabelGenerated: Generated},
				}},
			}},
		},
		label: "generated-label",
		expectedJobConfig: &prowconfig.JobConfig{
			Presubmits: map[string][]prowconfig.Presubmit{"org/repo": {
				prowconfig.Presubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-presubmit",
					Labels: map[string]string{ProwJobLabelGenerated: "generated-label"},
				}},
			}},
			Postsubmits: map[string][]prowconfig.Postsubmit{"org/repo": {
				prowconfig.Postsubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-postsubmit",
					Labels: map[string]string{ProwJobLabelGenerated: "generated-label"}},
				}},
			},
		}},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(*testing.T) {
			labelGeneratedJobs(tc.jobconfig, tc.label)
			if !reflect.DeepEqual(tc.expectedJobConfig, tc.jobconfig) {
				t.Errorf("Modified job config differs from expected:\n%s", diff.ObjectReflectDiff(tc.expectedJobConfig, tc.jobconfig))
			}
		})
	}
}

func TestPruneStaleGeneratedJobs(t *testing.T) {
	staleLabel := "STALE"

	testCases := []struct {
		description string
		jobconfig   *prowconfig.JobConfig
		expected    *prowconfig.JobConfig
	}{{
		description: "jobs without any generated labels are not pruned",
		jobconfig: &prowconfig.JobConfig{
			Presubmits: map[string][]prowconfig.Presubmit{"org/repo": {
				prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "org-repo-presubmit"}},
			}},
			Postsubmits: map[string][]prowconfig.Postsubmit{"org/repo": {
				prowconfig.Postsubmit{JobBase: prowconfig.JobBase{Name: "org-repo-postsubmit"}},
			}},
		},
		expected: &prowconfig.JobConfig{
			Presubmits: map[string][]prowconfig.Presubmit{"org/repo": {
				prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "org-repo-presubmit"}},
			}},
			Postsubmits: map[string][]prowconfig.Postsubmit{"org/repo": {
				prowconfig.Postsubmit{JobBase: prowconfig.JobBase{Name: "org-repo-postsubmit"}},
			}},
		},
	}, {
		description: "jobs with generated labels, but non-matching value are not pruned",
		jobconfig: &prowconfig.JobConfig{
			Presubmits: map[string][]prowconfig.Presubmit{"org/repo": {
				prowconfig.Presubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-presubmit",
					Labels: map[string]string{ProwJobLabelGenerated: "NOT STALE"},
				}},
			}},
			Postsubmits: map[string][]prowconfig.Postsubmit{"org/repo": {
				prowconfig.Postsubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-postsubmit",
					Labels: map[string]string{ProwJobLabelGenerated: "NOT STALE"}}},
			}},
		},
		expected: &prowconfig.JobConfig{
			Presubmits: map[string][]prowconfig.Presubmit{"org/repo": {
				prowconfig.Presubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-presubmit",
					Labels: map[string]string{ProwJobLabelGenerated: "NOT STALE"},
				}},
			}},
			Postsubmits: map[string][]prowconfig.Postsubmit{"org/repo": {
				prowconfig.Postsubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-postsubmit",
					Labels: map[string]string{ProwJobLabelGenerated: "NOT STALE"}}},
			}},
		},
	}, {
		description: "jobs with generated labels and matching value are pruned",
		jobconfig: &prowconfig.JobConfig{
			Presubmits: map[string][]prowconfig.Presubmit{"org/repo": {
				prowconfig.Presubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-presubmit",
					Labels: map[string]string{ProwJobLabelGenerated: "NOT STALE"},
				}},
				prowconfig.Presubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-stale-presubmit",
					Labels: map[string]string{ProwJobLabelGenerated: staleLabel},
				}},
			}},
			Postsubmits: map[string][]prowconfig.Postsubmit{"org/repo": {
				prowconfig.Postsubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-stale-postsubmit",
					Labels: map[string]string{ProwJobLabelGenerated: staleLabel},
				}},
				prowconfig.Postsubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-postsubmit",
					Labels: map[string]string{ProwJobLabelGenerated: "NOT STALE"},
				}},
			}},
		},
		expected: &prowconfig.JobConfig{
			Presubmits: map[string][]prowconfig.Presubmit{"org/repo": {
				prowconfig.Presubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-presubmit",
					Labels: map[string]string{ProwJobLabelGenerated: "NOT STALE"},
				}},
			}},
			Postsubmits: map[string][]prowconfig.Postsubmit{"org/repo": {
				prowconfig.Postsubmit{JobBase: prowconfig.JobBase{
					Name:   "org-repo-postsubmit",
					Labels: map[string]string{ProwJobLabelGenerated: "NOT STALE"},
				}},
			}},
		},
	},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(*testing.T) {
			pruneStaleGeneratedJobs(tc.jobconfig, staleLabel)
			if !reflect.DeepEqual(tc.expected, tc.jobconfig) {
				t.Errorf("Pruned job config differs from expected:\n%s", diff.ObjectReflectDiff(tc.expected, tc.jobconfig))
			}
		})
	}
}
