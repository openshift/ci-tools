package jobrunaggregatorapi

const (
	AlertsTableName = "Alerts"

	alertSchema = `
[
  {
    "name": "JobRunName",
    "description": "name of the jobrun (the long number)",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "Name",
    "description": "name of the alert",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "Namespace",
    "description": "namespace the alert fires in",
    "type": "STRING",
    "mode": "NULLABLE"
  },
  {
    "name": "Level",
    "description": "Info, Warning, Critical",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "AlertSeconds",
    "description": "number of seconds the alert was firing",
    "type": "INTEGER",
    "mode": "REQUIRED"
  }
]
`

	unifiedAlertSchema = `
SELECT 
  Alerts.Name as AlertName,
  Alerts.Namespace as AlertNamespace,
  Alerts.Level as AlertLevel,
  JobRuns.name as JobRunName,
  Jobs.Jobname as JobName,
  Alerts.AlertSeconds as AlertSeconds,
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
FROM openshift-ci-data-analysis.ci_data.Alerts
INNER JOIN openshift-ci-data-analysis.ci_data.Alerts_JobRuns as JobRuns on Alerts.JobRunName = JobRuns.Name
INNER JOIN openshift-ci-data-analysis.ci_data.Jobs on JobRuns.JobName = Jobs.JobName
`
)

type AlertRow struct {
	JobRunName   string
	Name         string
	Namespace    string
	Level        string
	AlertSeconds int
}
