package jobrunaggregatorapi

import "cloud.google.com/go/bigquery"

const (
	JobsTableName = "Jobs"
)

type JobRow struct {
	JobName                     string
	GCSBucketName               string
	GCSJobHistoryLocationPrefix string
	CollectDisruption           bool
	CollectTestRuns             bool
}

type JobRowWithVariants struct {
	JobName                     string
	GCSBucketName               string
	GCSJobHistoryLocationPrefix string
	CollectDisruption           bool
	CollectTestRuns             bool
	Platform                    string
	Architecture                string
	Network                     string
	IPMode                      string
	Topology                    string
	Release                     string
	FromRelease                 bigquery.NullString
}
