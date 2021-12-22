package jobrunaggregatorapi

import "time"

const (
	LegacyJobRunTableName     = "JobRuns"
	DisruptionJobRunTableName = "BackendDisruption_JobRuns"

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
