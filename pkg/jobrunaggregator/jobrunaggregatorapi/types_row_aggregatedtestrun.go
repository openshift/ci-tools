package jobrunaggregatorapi

import (
	"time"

	"cloud.google.com/go/bigquery"
)

type AggregatedTestRunRow struct {
	AggregationStartDate time.Time
	TestName             string
	// TODO work out how to avoid the bigquery dep
	TestSuiteName     bigquery.NullString
	JobName           string
	PassCount         int
	FailCount         int
	FlakeCount        int
	PassPercentage    float64
	WorkingPercentage float64
	DominantCluster   string
	//JobLabels            []string
}
