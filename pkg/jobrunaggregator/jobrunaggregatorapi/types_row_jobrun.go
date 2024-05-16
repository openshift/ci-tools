package jobrunaggregatorapi

import (
	"time"

	"cloud.google.com/go/bigquery"
)

const (
	LegacyJobRunTableName     = "JobRuns"
	DisruptionJobRunTableName = "BackendDisruption_JobRuns"

	// TODO: Remove population of this table, I don't think it's used anywhere in ci-tools or the views in bigquery.
	// Instead used the data embedded in each Alerts row, or the jobs table in openshift-gce-devel.ci_analysis_us
	AlertJobRunTableName = "Alerts_JobRuns"
)

type JobRunRow struct {
	Name               string
	JobName            string
	Status             string
	StartTime          time.Time
	EndTime            time.Time
	ReleaseTag         string
	Cluster            string
	MasterNodesUpdated bigquery.NullString
}

// TestPlatformProwJobRow is a transient struct for processing results from the bigquery jobs table populated
// by testplatform. ProwJob kube resources are stored here after we upload job artifacts to GCS.
type TestPlatformProwJobRow struct {
	JobName        string    `bigquery:"prowjob_job_name"`
	State          string    `bigquery:"prowjob_state"`
	BuildID        string    `bigquery:"prowjob_build_id"`
	Type           string    `bigquery:"prowjob_type"`
	Cluster        string    `bigquery:"prowjob_cluster"`
	StartTime      time.Time `bigquery:"prowjob_start_ts"`
	CompletionTime time.Time `bigquery:"prowjob_completion_ts"`
	URL            string    `bigquery:"prowjob_url"`
}
