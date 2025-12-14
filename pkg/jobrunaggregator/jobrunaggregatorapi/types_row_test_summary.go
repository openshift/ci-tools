package jobrunaggregatorapi

import (
	"cloud.google.com/go/civil"
)

// TestSummaryByPeriodRow represents aggregated test results for a specific suite and release over a time period.
// This data structure corresponds to the suite_summary_by_period.sql query results.
type TestSummaryByPeriodRow struct {
	Release           string     `bigquery:"release"`
	Platform          string     `bigquery:"platform"`
	Topology          string     `bigquery:"topology"`
	Architecture      string     `bigquery:"architecture"`
	TestName          string     `bigquery:"test_name"`
	TotalTestCount    int64      `bigquery:"total_test_count"`
	TotalFailureCount int64      `bigquery:"total_failure_count"`
	TotalFlakeCount   int64      `bigquery:"total_flake_count"`
	FailureRate       float64    `bigquery:"failure_rate"`
	AvgDurationMs     float64    `bigquery:"avg_duration_ms"`
	PeriodStart       civil.Date `bigquery:"period_start"`
	PeriodEnd         civil.Date `bigquery:"period_end"`
	DaysWithData      int64      `bigquery:"days_with_data"`
}
