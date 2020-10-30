package config

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
	prowconfig "k8s.io/test-infra/prow/config"
)

func TestPresubmitsAddAll(t *testing.T) {
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
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "source-job-for-org-repo"}},
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
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "source-job-for-org-repo"}},
		}},
	}, {
		description: "merge jobs for different repos, result should have both",
		source: Presubmits{"org/source-repo": {
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "source-job-for-org-source-repo"}},
		}},
		destination: Presubmits{"org/destination-repo": {
			prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "destination-job-for-org-destination-repo"}}}},
		expected: Presubmits{
			"org/source-repo":      {prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "source-job-for-org-source-repo"}}},
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
			tc.destination.AddAll(tc.source)

			if !reflect.DeepEqual(tc.destination, tc.expected) {
				t.Errorf("Presubmits differ from expected after AddAll:\n%s", diff.ObjectDiff(tc.expected, tc.destination))
			}
		})
	}
}

func TestPresubmitsAdd(t *testing.T) {
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
		expected:    Presubmits{"org/repo": {prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: "job-for-org-repo"}}}},
	},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tc.presubmits.Add(tc.repo, tc.job)

			if !reflect.DeepEqual(tc.expected, tc.presubmits) {
				t.Errorf("Presubmits differ from expected after Add:\n%s", diff.ObjectDiff(tc.expected, tc.presubmits))
			}
		})
	}

}
