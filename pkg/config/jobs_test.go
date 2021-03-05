package config

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	prowconfig "k8s.io/test-infra/prow/config"
)

func TestPresubmitsAddAll(t *testing.T) {
	allowUnexported := cmp.AllowUnexported(prowconfig.Brancher{}, prowconfig.RegexpChangeMatcher{}, prowconfig.Presubmit{})

	testCases := []struct {
		description string
		source      Presubmits
		destination Presubmits
		expected    Presubmits
	}{{
		description: "merge empty structure into empty structure",
	}, {
		description: "merge empty structure into non-empty structure",
		source:      Presubmits{},
		destination: Presubmits{"org/repo": {
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "destination-job-for-org-repo"}},
		}},
		expected: Presubmits{"org/repo": {
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "destination-job-for-org-repo"}},
		}},
	}, {
		description: "merge non-empty structure into empty structure",
		source: Presubmits{"org/repo": {
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "source-job-for-org-repo"}},
		}},
		destination: Presubmits{},
		expected: Presubmits{"org/repo": {
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "source-job-for-org-repo", Labels: map[string]string{"pj-rehearse.openshift.io/source-type": "changedPresubmit"}}},
		}},
	}, {
		description: "merge different jobs for a single repo, result should have both",
		source: Presubmits{"org/repo": {
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "source-job-for-org-repo"}},
		}},
		destination: Presubmits{"org/repo": {
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "destination-job-for-org-repo"}}}},
		expected: Presubmits{"org/repo": {
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "destination-job-for-org-repo"}},
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "source-job-for-org-repo", Labels: map[string]string{"pj-rehearse.openshift.io/source-type": "changedPresubmit"}}},
		}},
	}, {
		description: "merge jobs for different repos, result should have both",
		source: Presubmits{"org/source-repo": {
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "source-job-for-org-source-repo"}},
		}},
		destination: Presubmits{"org/destination-repo": {
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "destination-job-for-org-destination-repo"}}}},
		expected: Presubmits{
			"org/source-repo":      {prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "source-job-for-org-source-repo", Labels: map[string]string{"pj-rehearse.openshift.io/source-type": "changedPresubmit"}}}},
			"org/destination-repo": {prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "destination-job-for-org-destination-repo"}}},
		},
	}, {
		description: "merge jobs with same name for a single repo, result has the one originally in destination",
		source: Presubmits{"org/repo": {
			prowconfig.Presubmit{
				JobBase:   prowconfig.JobBase{Name: "same-job-for-org-repo"},
				AlwaysRun: true,
			},
		}},
		destination: Presubmits{"org/repo": {
			prowconfig.Presubmit{
				JobBase:   prowconfig.JobBase{Name: "same-job-for-org-repo"},
				AlwaysRun: false,
			}}},
		expected: Presubmits{"org/repo": {
			prowconfig.Presubmit{
				JobBase:   prowconfig.JobBase{Name: "same-job-for-org-repo"},
				AlwaysRun: false,
			},
		}},
	},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tc.destination.AddAll(tc.source, ChangedPresubmit)

			if diff := cmp.Diff(tc.destination, tc.expected, allowUnexported); diff != "" {
				t.Errorf("Presubmits differ from expected after AddAll:\n%s", diff)
			}
		})
	}
}

func TestPresubmitsAdd(t *testing.T) {
	allowUnexported := cmp.AllowUnexported(prowconfig.Brancher{}, prowconfig.RegexpChangeMatcher{}, prowconfig.Presubmit{})

	testCases := []struct {
		description string
		presubmits  Presubmits
		repo        string
		job         prowconfig.Presubmit
		expected    Presubmits
	}{{
		description: "add job to new repo",
		presubmits:  Presubmits{},
		repo:        "org/repo",
		job:         prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "job-for-org-repo"}},
		expected:    Presubmits{"org/repo": {prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "job-for-org-repo", Labels: map[string]string{"pj-rehearse.openshift.io/source-type": "changedPresubmit"}}}}},
	},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tc.presubmits.Add(tc.repo, tc.job, ChangedPresubmit)

			if diff := cmp.Diff(tc.expected, tc.presubmits, allowUnexported); diff != "" {
				t.Errorf("Presubmits differ from expected after Add:\n%s", diff)
			}
		})
	}

}

func TestGetSourceType(t *testing.T) {
	testCases := []struct {
		id       string
		labels   map[string]string
		expected SourceType
	}{
		{
			id:       "happy",
			labels:   map[string]string{SourceTypeLabel: "changedPresubmit"},
			expected: ChangedPresubmit,
		},
		{
			id: "happy multiple",
			labels: map[string]string{
				"another-label":  "another-value",
				"another-label2": "another-value2",
				SourceTypeLabel:  "changedPresubmit"},
			expected: ChangedPresubmit,
		},
		{
			id:       "sad",
			labels:   map[string]string{},
			expected: Unknown,
		},
		{
			id: "sad multiple",
			labels: map[string]string{
				"another-label":  "another-value",
				"another-label2": "another-value2",
			},
			expected: Unknown,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {

			actual := GetSourceType(tc.labels)

			if diff := cmp.Diff(actual, tc.expected); diff != "" {
				t.Error(diff)
			}
		})
	}
}
