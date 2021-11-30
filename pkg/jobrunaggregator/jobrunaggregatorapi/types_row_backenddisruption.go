package jobrunaggregatorapi

const (
	BackendDisruptionTableName = "BackendDisruption"

	unifiedBackendDisruptionSchema = `
SELECT 
  BackendDisruption.BackendName as BackendName,
  JobRuns.name as JobRunName,
  Jobs.Jobname as JobName,
  BackendDisruption.DisruptionSeconds as DisruptionSeconds,
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
FROM openshift-ci-data-analysis.ci_data.BackendDisruption
INNER JOIN openshift-ci-data-analysis.ci_data.BackendDisruption_JobRuns as JobRuns on BackendDisruption.JobRunName = JobRuns.Name
INNER JOIN openshift-ci-data-analysis.ci_data.Jobs on JobRuns.JobName = Jobs.JobName
`

	unifiedBackendDisruptionBackendsSchema = `
SELECT 
  JobRuns.name as JobRunName,
  Jobs.Jobname as JobName,
  JobRuns.StartTime as JobRunStartTime,
  JobRuns.ReleaseTag as ReleaseTag,
  BackendDisruption.BackendName as BackendName,
  BackendDisruption.DisruptionSeconds as DisruptionSeconds,
  JobRuns.Cluster as Cluster,
  Jobs.Platform as Platform,
  Jobs.Network as Network,
  Jobs.IPMode as IPMode,
  Jobs.Topology as Topology,
  Jobs.Release as Release,
  Jobs.FromRelease as FromRelease,
  if(Jobs.FromRelease="",false,true) as IsUpgrade,
FROM openshift-ci-data-analysis.ci_data.BackendDisruption
INNER JOIN openshift-ci-data-analysis.ci_data.BackendDisruption_JobRuns as JobRuns on JobRuns.Name = BackendDisruption.JobRunName
INNER JOIN openshift-ci-data-analysis.ci_data.Jobs on JobRuns.JobName = Jobs.JobName
`

	unifiedBackendDisruptionBackendByCountSchema = `
SELECT 
  Jobs.Jobname as JobName,
  ByCount.BackendName as BackendName,
  ByCount.DisruptionSeconds as DisruptionSeconds,
  ByCount.NumberOfRuns as NumberOfRuns,
  Jobs.Platform as Platform,
  Jobs.Network as Network,
  Jobs.IPMode as IPMode,
  Jobs.Topology as Topology,
  Jobs.Release as Release,
  Jobs.FromRelease as FromRelease,
  if(Jobs.FromRelease="",false,true) as IsUpgrade,
FROM openshift-ci-data-analysis.ci_data.BackendDisruption_ByCountByJob as ByCount
INNER JOIN openshift-ci-data-analysis.ci_data.Jobs on ByCount.JobName = Jobs.JobName
`

	unifiedBackendDisruptionByCountByJobSchema = `
SELECT
    JobName, 
    BackendName, 
    DisruptionSeconds,
    count(JobRunName) as NumberOfRuns
from 
    openshift-ci-data-analysis.ci_data.BackendDisruption_Unified_Backends
WHERE
    JobRunStartTime BETWEEN
        TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 10 DAY)
        AND
        TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 3 DAY)
group by
    JobName, BackendName, DisruptionSeconds
`
)

// BackendDisruptionRow is for the BackendDisruption table.
type BackendDisruptionRow struct {
	BackendName       string
	JobRunName        string
	DisruptionSeconds int
}

// BackendDisruptionStatisticsRow is for the historical query used by the aggregator.
type BackendDisruptionStatisticsRow struct {
	BackendName       string
	Mean              float64
	StandardDeviation float64
	P1                float64
	P2                float64
	P3                float64
	P4                float64
	P5                float64
	P6                float64
	P7                float64
	P8                float64
	P9                float64
	P10               float64
	P11               float64
	P12               float64
	P13               float64
	P14               float64
	P15               float64
	P16               float64
	P17               float64
	P18               float64
	P19               float64
	P20               float64
	P21               float64
	P22               float64
	P23               float64
	P24               float64
	P25               float64
	P26               float64
	P27               float64
	P28               float64
	P29               float64
	P30               float64
	P31               float64
	P32               float64
	P33               float64
	P34               float64
	P35               float64
	P36               float64
	P37               float64
	P38               float64
	P39               float64
	P40               float64
	P41               float64
	P42               float64
	P43               float64
	P44               float64
	P45               float64
	P46               float64
	P47               float64
	P48               float64
	P49               float64
	P50               float64
	P51               float64
	P52               float64
	P53               float64
	P54               float64
	P55               float64
	P56               float64
	P57               float64
	P58               float64
	P59               float64
	P60               float64
	P61               float64
	P62               float64
	P63               float64
	P64               float64
	P65               float64
	P66               float64
	P67               float64
	P68               float64
	P69               float64
	P70               float64
	P71               float64
	P72               float64
	P73               float64
	P74               float64
	P75               float64
	P76               float64
	P77               float64
	P78               float64
	P79               float64
	P80               float64
	P81               float64
	P82               float64
	P83               float64
	P84               float64
	P85               float64
	P86               float64
	P87               float64
	P88               float64
	P89               float64
	P90               float64
	P91               float64
	P92               float64
	P93               float64
	P94               float64
	P95               float64
	P96               float64
	P97               float64
	P98               float64
	P99               float64
}
