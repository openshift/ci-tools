package jobrunaggregatorapi

import (
	"cloud.google.com/go/bigquery"
)

const (
	TestRunTableName = "TestRuns"

	// The TestRunsSchema below is used to build the "TestRuns" table.
	//
	TestRunsSchema = `
[
  {
    "mode": "REQUIRED",
    "name": "Name",
    "description" : "Name of the test run",
    "type": "STRING"
  },
  {
    "mode": "REQUIRED",
    "name": "JobRunName",
    "description" : "Name of the JobRun (big number) that ran this test (e.g., 1389486541524439040)",
    "type": "STRING"
  },
  {
    "mode": "REQUIRED",
    "name": "JobName",
    "description" : "Name of the Job that as this test in it",
    "type": "STRING"
  },
  {
    "mode": "REQUIRED",
    "name": "Status",
    "description" : "Status of the test (e.g., pass, fail)",
    "type": "STRING"
  },
  {
    "mode": "NULLABLE",
    "name": "TestSuite",
    "description" : "Testsuite that this test belongs to",
    "type": "STRING"
  }
]
`
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
