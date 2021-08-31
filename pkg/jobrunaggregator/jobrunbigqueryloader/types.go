package jobrunbigqueryloader

import (
	"context"

	"cloud.google.com/go/bigquery"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/junit"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

type BigQueryInserter interface {
	Put(ctx context.Context, src interface{}) (err error)
}

type jobRunRow struct {
	prowJob *prowv1.ProwJob
	jobRun  jobrunaggregatorapi.JobRunInfo
}

func newJobRunRow(jobRun jobrunaggregatorapi.JobRunInfo, prowJob *prowv1.ProwJob) *jobRunRow {
	return &jobRunRow{
		prowJob: prowJob,
		jobRun:  jobRun,
	}

}

var _ bigquery.ValueSaver = &jobRunRow{}

func (v *jobRunRow) Save() (map[string]bigquery.Value, string, error) {
	insertID := v.jobRun.GetJobRunID()
	row := map[string]bigquery.Value{
		"Name":       insertID,
		"JobName":    v.jobRun.GetJobName(),
		"Status":     v.prowJob.Status.State,
		"StartTime":  v.prowJob.Status.StartTime,
		"EndTime":    v.prowJob.Status.CompletionTime,
		"ReleaseTag": v.prowJob.Labels["release.openshift.io/analysis"],
		"Cluster":    v.prowJob.Spec.Cluster,
	}

	return row, insertID, nil
}

type testRunRow struct {
	prowJob    *prowv1.ProwJob
	jobRun     jobrunaggregatorapi.JobRunInfo
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

var _ bigquery.ValueSaver = &testRunRow{}

func (v *testRunRow) Save() (map[string]bigquery.Value, string, error) {

	status := "Unknown"
	switch {
	case v.testCase.FailureOutput != nil:
		status = "Failed"
	case v.testCase.SkipMessage != nil:
		status = "Skipped"
	default:
		status = "Passed"
	}

	row := map[string]bigquery.Value{
		"Name":       v.testCase.Name,
		"JobRunName": v.jobRun.GetJobRunID(),
		"JobName":    v.jobRun.GetJobName(),
		"Status":     status,
	}

	return row, "", nil
}
