package jobrunaggregatorapi

import "time"

type UnifiedTestRunRow struct {
	TestName        string
	JobRunName      string
	JobName         string
	TestStatus      string
	JobRunStartTime time.Time
	ReleaseTag      string
	Cluster         string
	//JobLabels       []string
}

type AggregatedTestRunRow struct {
	AggregationStartDate time.Time
	TestName             string
	JobName              string
	PassCount            int
	FailCount            int
	FlakeCount           int
	PassPercentage       int
	WorkingPercentage    int
	DominantCluster      string
	//JobLabels            []string
}
