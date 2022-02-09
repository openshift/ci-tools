package jobrunaggregatorapi

import "time"

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
	Name       string
	JobName    string
	Status     string
	StartTime  time.Time
	EndTime    time.Time
	ReleaseTag string
	Cluster    string
}
