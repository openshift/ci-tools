package jobrunaggregatorapi

import "time"

const (
	// used to create TestRuns_Unified_Last200JobRuns
	// A query to pull the historical jobruns we will use to represent aggregated history
	testRunsUnifiedLast200JobRunsSchema = `
select 
    * 
from (
    SELECT 
        row_number() over (PARTITION BY Jobs.Jobname ORDER BY JobRuns.name DESC) JobRunIndex,
        JobRuns.name as JobRunName,
        Jobs.Jobname as JobName,
        JobRuns.StartTime as JobRunStartTime,
        JobRuns.ReleaseTag as ReleaseTag,
        JobRuns.Cluster as Cluster,
        Jobs.Platform as Platform,
        Jobs.Network as Network,
        Jobs.IPMode as IPMode,
        Jobs.Topology as Topology,
        Jobs.Release as Release,
        Jobs.FromRelease as FromRelease,
        if(Jobs.FromRelease="",false,true) as IsUpgrade,
    FROM openshift-ci-data-analysis.ci_data.JobRuns as JobRuns
    INNER JOIN openshift-ci-data-analysis.ci_data.Jobs on JobRuns.JobName = Jobs.JobName
    order by jobruns.name
)
where
    JobRunIndex <= 200 AND 
    JobRunStartTime >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 21 DAY)
`

	// used to create TestRuns_Unified_TestRunsForLast200JobRuns and TestRuns_Scheduled_Unified_TestRunsForLast200JobRuns
	// A query to find all the testruns to include in the historical data
	testRunsUnifiedTestRunsForLast200JobRunsSchema = `
select 
  testRuns.name as TestName,
  jobRuns.JobRunName as JobRunName,
  jobRuns.JobName as JobName,
  testRuns.Status as TestStatus,
  jobRuns.JobRunStartTime as JobRunStartTime,
  jobRuns.ReleaseTag as ReleaseTag,
  jobRuns.cluster as Cluster,
  jobRuns.platform as Platform,
  jobRuns.network as NetworkPlugin,
  jobRuns.release as Release,
  jobRuns.fromrelease as FromRelease,
  if(TestRuns.Status="Passed", 1, 0) as Passed,
  if(TestRuns.Status="Failed", 1, 0) as Failed,
from openshift-ci-data-analysis.ci_data.TestRuns
inner join openshift-ci-data-analysis.ci_data.TestRuns_Unified_Last200JobRuns as JobRuns on TestRuns.JobRunName = jobRuns.JobRunName
`

	// used to create TestRuns_Unified_TestRunsSingleResultForLast200JobRuns
	// A query to that sets a pass, fail, flake bit for every jobrun,test tuple.  This is logically correct *only*
	// if the testsuite is included.  When testsuite is added, this is one spot that is impacted
	testRunsUnifiedTestRunsSingleResultForLast200JobRunsSchema = `
select 
    JobRunName,
    TestName,
    if(sum(UnifiedTestRuns.Passed)>0 AND sum(UnifiedTestRuns.Failed)>0, 1, 0) as Flaked,
    if(sum(UnifiedTestRuns.Passed)>0 AND sum(UnifiedTestRuns.Failed)=0, 1, 0) as Passed,
    if(sum(UnifiedTestRuns.Passed)=0 AND sum(UnifiedTestRuns.Failed)>0, 1, 0) as Failed,
    min(UnifiedTestRuns.JobRunStartTime) as AggregationStartDate,
from openshift-ci-data-analysis.ci_data.TestRuns_Scheduled_Unified_TestRunsForLast200JobRuns as UnifiedTestRuns
group by JobRunName, TestName
`

	// used to create TestRuns_Summary_Last200Runs
	// This conforms to the AggregatedTestRunRow schema.  It sums the pass, fail, flake counts and pre-calculates
	// percentages for aggregated analysis.
	testRunsSummaryLast200RunsSchema = `
select 
  min(TestRuns.AggregationStartDate) as AggregationStartDate,
  testRuns.TestName as TestName,
  jobRuns.JobName as JobName,
  sum(TestRuns.Passed) as PassCount,
  sum(TestRuns.Failed) as FailCount,
  sum(TestRuns.Flaked) as FlakeCount,
  (sum(TestRuns.Passed)/(sum(TestRuns.Passed)+sum(TestRuns.Failed)+sum(TestRuns.Flaked)))*100 as PassPercentage,
  ((sum(TestRuns.Passed)+sum(TestRuns.Flaked))/(sum(TestRuns.Passed)+sum(TestRuns.Failed)+sum(TestRuns.Flaked)))*100 as WorkingPercentage,
  "" as DominantCluster,
from openshift-ci-data-analysis.ci_data.TestRuns_Unified_TestRunsSingleResultForLast200JobRuns as TestRuns
inner join openshift-ci-data-analysis.ci_data.TestRuns_Unified_Last200JobRuns as JobRuns on TestRuns.JobRunName = jobRuns.JobRunName
group by jobRuns.JobName, TestRuns.TestName
`
)

type AggregatedTestRunRow struct {
	AggregationStartDate time.Time
	TestName             string
	JobName              string
	PassCount            int
	FailCount            int
	FlakeCount           int
	PassPercentage       float64
	WorkingPercentage    float64
	DominantCluster      string
	//JobLabels            []string
}
