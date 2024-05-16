package jobrunaggregatorapi

import (
	"cloud.google.com/go/bigquery"
)

// Move here from jobrunbigqueryloader/types.go
type TestRunRow struct {
	Name               string
	Status             string
	TestSuite          string
	JobName            bigquery.NullString
	JobRunName         string
	JobRunStartTime    bigquery.NullTimestamp
	JobRunEndTime      bigquery.NullTimestamp
	Cluster            bigquery.NullString
	ReleaseTag         bigquery.NullString
	MasterNodesUpdated bigquery.NullString
	JobRunStatus       bigquery.NullString
}
