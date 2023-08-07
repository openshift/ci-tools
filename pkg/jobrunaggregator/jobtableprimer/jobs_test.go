package jobtableprimer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestForDuplicates(t *testing.T) {
	seen := sets.New[string]()
	for _, curr := range jobsToAnalyze {
		if seen.Has(curr.JobName) {
			t.Error(curr.JobName)
		}
		seen.Insert(curr.JobName)
	}
}

func TestJobVersions(t *testing.T) {
	var testCases = []struct {
		name        string
		jobName     string
		fromVersion string
		toVersion   string
	}{
		{
			name:        "multi-version-upgrade",
			jobName:     "release-openshift-origin-installer-e2e-aws-upgrade-4.11-to-4.12-to-4.13-to-4.14-ci",
			fromVersion: "4.11",
			toVersion:   "4.14",
		},
		{
			name:        "non-upgrade",
			jobName:     "release-openshift-origin-installer-e2e-aws-4.11-to-4.12-to-4.13-to-4.14-ci",
			fromVersion: "",
			toVersion:   "4.14",
		},
		{
			name:        "non-upgrade-mixed-version-order",
			jobName:     "release-openshift-origin-installer-e2e-aws-4.11-to-4.12-to-4.14-to-4.13-ci",
			fromVersion: "",
			toVersion:   "4.14",
		},
		{
			name:        "micro-version-upgrade",
			jobName:     "release-openshift-origin-installer-e2e-aws-upgrade-4.14-ci",
			fromVersion: "4.14",
			toVersion:   "4.14",
		},
		{
			name:        "minor-version-upgrade",
			jobName:     "release-openshift-origin-installer-e2e-aws-upgrade-4.13-to-4.14-ci",
			fromVersion: "4.13",
			toVersion:   "4.14",
		},
		{
			name:        "missing-version-upgrade",
			jobName:     "release-openshift-origin-installer-e2e-aws-upgrade-ci",
			fromVersion: "",
			toVersion:   "unknown",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			j := newJob(tc.jobName)

			assert.NotNil(t, j, "Unexpected nil builder")
			assert.NotNil(t, j.job, "Unexpected nil job")
			assert.Equal(t, tc.toVersion, j.job.Release, "Invalid toVersion")
			assert.Equal(t, tc.fromVersion, j.job.FromRelease, "Invalid fromVersion")
		})
	}

}
