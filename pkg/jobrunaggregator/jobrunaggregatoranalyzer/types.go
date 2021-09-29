package jobrunaggregatoranalyzer

type AggregationConfiguration struct {
	UnfinishedJobs []JobRunInfo
	FinishedJobs   []JobRunInfo
}

type JobRunInfo struct {
	JobName      string
	JobRunID     string
	HumanURL     string
	GCSBucketURL string

	Status string
}
