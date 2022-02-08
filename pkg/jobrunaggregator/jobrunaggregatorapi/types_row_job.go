package jobrunaggregatorapi

// The jobSchema below is used to build the "Jobs" table.
//
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
  },
  {
    "name": "Platform",
    "description": "gcp, aws, vsphere, metal, etc",
    "type": "STRING",
    "mode": "REQUIRED"
  },
    {
    "name": "Architecture",
    "description": "amd64, arm64, ppc64le, s390x",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "Network",
    "description": "ovn, sdn, etc",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "IPMode",
    "description": "ipv4, ipv6, dual, etc",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "Topology",
    "description": "Single, HA, etc",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "Release",
    "description": "4.8, 4.9, etc",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "FromRelease",
    "description": "4.8, 4.9, etc",
    "type": "STRING",
    "mode": "REQUIRED"
  },
  {
    "name": "RunsUpgrade",
    "description": "true if the job run an upgrade",
    "type": "BOOLEAN",
    "mode": "REQUIRED"
  },
  {
    "name": "RunsE2EParallel",
    "description": "true if the job runs e2e parallel",
    "type": "BOOLEAN",
    "mode": "REQUIRED"
  },
  {
    "name": "RunsE2ESerial",
    "description": "true if the job runs e2e serial",
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
	Platform                    string
	Architecture                string
	Network                     string
	IPMode                      string
	Topology                    string
	Release                     string
	FromRelease                 string
	RunsUpgrade                 bool
	RunsE2EParallel             bool
	RunsE2ESerial               bool
}
