package jobrunaggregatorapi

import (
	"time"

	"cloud.google.com/go/bigquery"
)

const (
	LegacyJobRunTableName     = "JobRuns"
	DisruptionJobRunTableName = "BackendDisruption_JobRuns"
	AlertJobRunTableName      = "Alerts_JobRuns"

	JobRunSchema = `
[
  {
    "name": "Name",
    "description": "name of the jobrun (the long number)",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "JobName",
    "description": "name of the job from CI",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "Status",
    "description": "error, failure, success",
    "type": "STRING",
    "mode": "NULLABLE"
  },
  {
    "name": "StartTime",
    "description": "time the jobrun started",
    "type": "TIMESTAMP",
    "mode": "NULLABLE"
  },
  {
    "name": "EndTime",
    "description": "time the jobrun started",
    "type": "TIMESTAMP",
    "mode": "NULLABLE"
  },
  {
    "name": "ReleaseTag",
    "description": "",
    "type": "STRING",
    "mode": "NULLABLE"
  },
  {
    "name": "Cluster",
    "description": "the build farm cluster that the CI job ran on: build01, build02, build03, vsphere, etc",
    "type": "STRING",
    "mode": "NULLABLE"
  },
  {
    "name": "MasterNodesUpdated",
    "description": "indicator if master nodes restarted during the jobrun",
    "type": "STRING",
    "mode": "NULLABLE"
  }
]
`

	// used for TestRuns_Unified_AllJobRuns
	testRunsUnifiedJobRunsSchema = `
SELECT 
  JobRuns.name as JobRunName,
  Jobs.Jobname as JobName,
  JobRuns.StartTime as JobRunStartTime,
  JobRuns.ReleaseTag as ReleaseTag,
  JobRuns.Cluster as Cluster,
  Jobs.Platform as Platform,
  Jobs.Architecture as Architecture,
  Jobs.Network as Network,
  Jobs.IPMode as IPMode,
  Jobs.Topology as Topology,
  Jobs.Release as Release,
  Jobs.FromRelease as FromRelease,
  if(Jobs.FromRelease="",false,true) as IsUpgrade,
FROM openshift-ci-data-analysis.ci_data.JobRuns
INNER JOIN openshift-ci-data-analysis.ci_data.Jobs on JobRuns.JobName = Jobs.JobName
`
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
