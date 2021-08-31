package jobrunaggregatoranalyzer

type AggregationConfiguration struct {
	IndividualJobs []JobRunInfo
}

type JobRunInfo struct {
	JobName      string
	JobRunID     string
	HumanURL     string
	GCSBucketURL string

	Status string
}
