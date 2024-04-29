package jobrunaggregatorapi

// The jobSchema below is used to build the "Jobs" table.
const (
	JobsTableName = "Jobs"
	JobSchema     = `
[
  {
    "name": "JobName",
    "description": "name of the job from CI",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "GCSBucketName",
    "description": "name of the GCS bucket we store jobrun artifacts in",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "GCSJobHistoryLocationPrefix",
    "description": "spot in the GCS bucket to look.  like logs/<jobName>",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "CollectDisruption",
    "description": "should we collect disruption data from the job runs",
    "type": "BOOLEAN",
    "mode": "REQUIRED"
  },
  {
    "name": "CollectTestRuns",
    "description": "should we collect test run data from the job runs",
    "type": "BOOLEAN",
    "mode": "REQUIRED"
  }
]
`
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
	FromRelease                 string
}
