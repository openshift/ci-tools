package jobrunaggregatorapi

import (
	"cloud.google.com/go/bigquery"
)

const (
	AlertsTableName = "Alerts"
)

type AlertRow struct {
	Name               string
	Namespace          string
	Level              string
	AlertSeconds       int
	JobName            bigquery.NullString
	JobRunName         string
	JobRunStartTime    bigquery.NullTimestamp
	JobRunEndTime      bigquery.NullTimestamp
	Cluster            bigquery.NullString
	ReleaseTag         bigquery.NullString
	MasterNodesUpdated bigquery.NullString
	JobRunStatus       bigquery.NullString
}
