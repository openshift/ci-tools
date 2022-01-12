package jobrunbigqueryloader

import (
	"time"

	"cloud.google.com/go/bigquery"

	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/junit"
)

func newJobRunRow(jobRun jobrunaggregatorapi.JobRunInfo, prowJob *prowv1.ProwJob) *jobrunaggregatorapi.JobRunRow {
	var endTime time.Time
	if prowJob.Status.CompletionTime != nil {
		endTime = prowJob.Status.CompletionTime.Time
	}
	return &jobrunaggregatorapi.JobRunRow{
		Name:       jobRun.GetJobRunID(),
		JobName:    jobRun.GetJobName(),
		Status:     string(prowJob.Status.State),
		StartTime:  prowJob.Status.StartTime.Time,
		EndTime:    endTime,
		ReleaseTag: prowJob.Labels["release.openshift.io/analysis"],
		Cluster:    prowJob.Spec.Cluster,
	}

}

type testRunRow struct {
	prowJob *prowv1.ProwJob
	jobRun  jobrunaggregatorapi.JobRunInfo

	// ci-tools/pkg/junit/types.go mentions that a TestSuite can itself hold
	// TestSuites, hence declared as a slice below; but we only use a single
	// TestSuite today.
	testSuites []string
	testCase   *junit.TestCase
}

func newTestRunRow(jobRun jobrunaggregatorapi.JobRunInfo, prowJob *prowv1.ProwJob, testSuites []string, testCase *junit.TestCase) *testRunRow {
	return &testRunRow{
		prowJob:    prowJob,
		jobRun:     jobRun,
		testSuites: testSuites,
		testCase:   testCase,
	}

}

// Ensure (at compile time) that testRunRow implements the bigquery.ValueSaver interface
var _ bigquery.ValueSaver = &testRunRow{}

// Save, a custom method defined for the testRunRow type, is called by the Put method
// to create a row that will be uploaded to Big Query.
func (v *testRunRow) Save() (map[string]bigquery.Value, string, error) {

	// the linter requires not setting a default value. This seems strictly worse and more error-prone to me, but
	// I am a slave to the bot.
	//status := "Unknown"
	var status string
	switch {
	case v.testCase.FailureOutput != nil:
		status = "Failed"
	case v.testCase.SkipMessage != nil:
		status = "Skipped"
	default:
		status = "Passed"
	}

	// We can add v.TestSuites[0] here so that the TestRun table gets populated with it.
	row := map[string]bigquery.Value{
		"Name":       v.testCase.Name,
		"JobRunName": v.jobRun.GetJobRunID(),
		"JobName":    v.jobRun.GetJobName(),
		"Status":     status,
	}

	return row, "", nil
}
