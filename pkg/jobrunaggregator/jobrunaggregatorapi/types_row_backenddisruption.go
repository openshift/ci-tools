package jobrunaggregatorapi

import (
	"cloud.google.com/go/bigquery"
)

const BackendDisruptionTableName = "BackendDisruption"

type BackendDisruptionRow struct {
	BackendName        string
	DisruptionSeconds  int
	JobName            bigquery.NullString
	JobRunName         string
	JobRunStartTime    bigquery.NullTimestamp
	JobRunEndTime      bigquery.NullTimestamp
	Cluster            bigquery.NullString
	ReleaseTag         bigquery.NullString
	MasterNodesUpdated bigquery.NullString
	JobRunStatus       bigquery.NullString
}
