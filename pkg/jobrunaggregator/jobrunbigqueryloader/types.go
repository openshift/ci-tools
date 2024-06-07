package jobrunbigqueryloader

import (
	"time"

	"cloud.google.com/go/bigquery"

	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
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
