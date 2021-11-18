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
)

type AlertRow struct {
	JobRunName   string
	Name         string
	Namespace    string
	Level        string
	AlertSeconds int
}
