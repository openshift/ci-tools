package jobrunbigqueryloader

import (
	"time"

	"cloud.google.com/go/bigquery"

	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/junit"
)

func newJobRunRow(jobRun jobrunaggregatorapi.JobRunInfo, prowJob *prowv1.ProwJob, masterNodesUpdated string) *jobrunaggregatorapi.JobRunRow {
	var endTime time.Time
	if prowJob.Status.CompletionTime != nil {
		endTime = prowJob.Status.CompletionTime.Time
	}

	var nsMasterNodesUpdated bigquery.NullString

	if len(masterNodesUpdated) > 0 {
		nsMasterNodesUpdated = bigquery.NullString{
			StringVal: masterNodesUpdated,
			Valid:     true,
		}
	}

	return &jobrunaggregatorapi.JobRunRow{
		Name:               jobRun.GetJobRunID(),
		JobName:            jobRun.GetJobName(),
		Status:             string(prowJob.Status.State),
		StartTime:          prowJob.Status.StartTime.Time,
		EndTime:            endTime,
		ReleaseTag:         prowJob.Labels["release.openshift.io/analysis"],
		Cluster:            prowJob.Spec.Cluster,
		MasterNodesUpdated: nsMasterNodesUpdated,
	}

}

func newTestRunRow(jobRunRow *jobrunaggregatorapi.JobRunRow, status string, testSuiteStr string, testCase *junit.TestCase) *jobrunaggregatorapi.TestRunRow {
	return &jobrunaggregatorapi.TestRunRow{
		Name:      testCase.Name,
		Status:    status,
		TestSuite: testSuiteStr,
		JobName: bigquery.NullString{
			StringVal: jobRunRow.JobName,
			Valid:     true,
		},
		JobRunName: jobRunRow.Name,
		JobRunStartTime: bigquery.NullTimestamp{
			Timestamp: jobRunRow.StartTime,
			Valid:     true,
		},
		JobRunEndTime: bigquery.NullTimestamp{
			Timestamp: jobRunRow.EndTime,
			Valid:     true,
		},
		Cluster: bigquery.NullString{
			StringVal: jobRunRow.Cluster,
			Valid:     true,
		},
		ReleaseTag: bigquery.NullString{
			StringVal: jobRunRow.ReleaseTag,
			Valid:     true,
		},
		JobRunStatus: bigquery.NullString{
			StringVal: jobRunRow.Status,
			Valid:     true,
		},
		MasterNodesUpdated: jobRunRow.MasterNodesUpdated,
	}
}
