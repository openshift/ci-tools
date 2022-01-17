package jobrunaggregatorlib

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

// AggregationJobClient client view used by the aggregation job
type AggregationJobClient interface {
	// GetJobRunForJobNameBeforeTime returns the jobRun closest to, but BEFORE, the time provided.
	// This is useful for bounding a query of GCS buckets in a window.
	// nil means that no jobRun was found before the specified time.
	GetJobRunForJobNameBeforeTime(ctx context.Context, jobName string, targetTime time.Time) (*jobrunaggregatorapi.JobRunRow, error)
	// GetJobRunForJobNameAfterTime returns the jobRun closest to, but AFTER, the time provided.
	// This is useful for bounding a query of GCS buckets in a window.
	// nil means that no jobRun as found after the specified time.
	GetJobRunForJobNameAfterTime(ctx context.Context, jobName string, targetTime time.Time) (*jobrunaggregatorapi.JobRunRow, error)

	// GetBackendDisruptionStatisticsByJob gets the mean and p95 disruption per backend from the week from 10 days ago.
	GetBackendDisruptionStatisticsByJob(ctx context.Context, jobName string) ([]jobrunaggregatorapi.BackendDisruptionStatisticsRow, error)

	ListAggregatedTestRunsForJob(ctx context.Context, frequency, jobName string, startDay time.Time) ([]jobrunaggregatorapi.AggregatedTestRunRow, error)
}

// TestRunUploadClient client view used by the test run uploader.  This is separated to make it easier to reason about which tables
// are in use by this client
type TestRunUploadClient interface {
	GetLastJobRunWithTestRunDataForJobName(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error)
}

// TestRunSummarizerClient client view used by the test run summarization client.
type TestRunSummarizerClient interface {
	GetLastAggregationForJob(ctx context.Context, frequency, jobName string) (*jobrunaggregatorapi.AggregatedTestRunRow, error)
	ListUnifiedTestRunsForJobAfterDay(ctx context.Context, jobName string, startDay time.Time) (*UnifiedTestRunRowIterator, error)
}

// DisruptionUploadClient client view used by the disruption loader so its easier to reason about which tables are in play
type DisruptionUploadClient interface {
	GetLastJobRunWithDisruptionDataForJobName(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error)
}

// AlertUploadClient client view used by the alert loader so its easier to reason about which tables are in play
type AlertUploadClient interface {
	GetLastJobRunWithAlertDataForJobName(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error)
}

type JobLister interface {
	ListAllJobs(ctx context.Context) ([]jobrunaggregatorapi.JobRow, error)
}

type CIDataClient interface {
	JobLister
	AggregationJobClient
	TestRunUploadClient
	DisruptionUploadClient
	AlertUploadClient
	TestRunSummarizerClient

	// these deal with release tags
	ListReleaseTags(ctx context.Context) (sets.String, error)
}

type ciDataClient struct {
	dataCoordinates BigQueryDataCoordinates
	client          *bigquery.Client

	disruptionJobRunTableName string
	testJobRunTableName       string
}

func NewCIDataClient(dataCoordinates BigQueryDataCoordinates, client *bigquery.Client) CIDataClient {
	return &ciDataClient{
		dataCoordinates:           dataCoordinates,
		client:                    client,
		disruptionJobRunTableName: jobrunaggregatorapi.DisruptionJobRunTableName,
		testJobRunTableName:       jobrunaggregatorapi.LegacyJobRunTableName,
	}
}

func (c *ciDataClient) ListAllJobs(ctx context.Context) ([]jobrunaggregatorapi.JobRow, error) {
	// For Debugging, you can set "LIMIT X" where X is small
	// so that you can process only a small subset of jobs while
	// you debug.
	queryString := c.dataCoordinates.SubstituteDataSetLocation(
		`SELECT *  
FROM DATA_SET_LOCATION.Jobs
ORDER BY Jobs.JobName ASC
`)

	query := c.client.Query(queryString)
	jobRows, err := query.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query job table with %q: %w", queryString, err)
	}
	jobs := []jobrunaggregatorapi.JobRow{}
	for {
		job := &jobrunaggregatorapi.JobRow{}
		err = jobRows.Next(job)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *job)
	}

	return jobs, nil
}

func (c *ciDataClient) GetLastJobRunWithTestRunDataForJobName(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error) {
	return c.getLastJobRunWithTestRunDataForJobName(ctx, jobrunaggregatorapi.LegacyJobRunTableName, jobName)
}

func (c *ciDataClient) GetLastJobRunWithDisruptionDataForJobName(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error) {
	return c.getLastJobRunWithTestRunDataForJobName(ctx, jobrunaggregatorapi.DisruptionJobRunTableName, jobName)
}

func (c *ciDataClient) GetLastJobRunWithAlertDataForJobName(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error) {
	return c.getLastJobRunWithTestRunDataForJobName(ctx, jobrunaggregatorapi.AlertJobRunTableName, jobName)
}

func (c *ciDataClient) getLastJobRunWithTestRunDataForJobName(ctx context.Context, tableName, jobName string) (*jobrunaggregatorapi.JobRunRow, error) {
	// the JobRun.Name is always increasing, so we can sort by that name.  The starttime is based on the prowjob
	// time and I don't think that is coordinated.
	// the testruns jobrun table is now distinct, so we will use the jobrun table as authoritative for what data should and should not
	// be uploaded rather than using the absence of testruns itself.  This will avoid having a large join and if a jobrun lacks test runs
	// for some reason, this avoids duplicate jobruns being created.
	queryString := c.dataCoordinates.SubstituteDataSetLocation(
		`
SELECT *
FROM DATA_SET_LOCATION.` + tableName + ` as JobRuns 
WHERE JobRuns.JobName = @JobName
ORDER BY JobRuns.Name DESC
LIMIT 1
`)

	query := c.client.Query(queryString)
	query.QueryConfig.Parameters = []bigquery.QueryParameter{
		{Name: "JobName", Value: jobName},
	}
	lastJobRunRow, err := query.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query aggregation table with %q: %w", queryString, err)
	}
	lastJobRun := &jobrunaggregatorapi.JobRunRow{}
	err = lastJobRunRow.Next(lastJobRun)
	if err == iterator.Done {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return lastJobRun, nil
}

func (c *ciDataClient) GetBackendDisruptionStatisticsByJob(ctx context.Context, jobName string) ([]jobrunaggregatorapi.BackendDisruptionStatisticsRow, error) {
	rows := make([]jobrunaggregatorapi.BackendDisruptionStatisticsRow, 0)

	queryString := c.dataCoordinates.SubstituteDataSetLocation(`
SELECT
    p95.BackendName,
    P95.P1,
    P95.P2,
    P95.P3,
    P95.P4,
    P95.P5,
    P95.P6,
    P95.P7,
    P95.P8,
    P95.P9,
    P95.P10,
    P95.P11,
    P95.P12,
    P95.P13,
    P95.P14,
    P95.P15,
    P95.P16,
    P95.P17,
    P95.P18,
    P95.P19,
    P95.P20,
    P95.P21,
    P95.P22,
    P95.P23,
    P95.P24,
    P95.P25,
    P95.P26,
    P95.P27,
    P95.P28,
    P95.P29,
    P95.P30,
    P95.P31,
    P95.P32,
    P95.P33,
    P95.P34,
    P95.P35,
    P95.P36,
    P95.P37,
    P95.P38,
    P95.P39,
    P95.P40,
    P95.P41,
    P95.P42,
    P95.P43,
    P95.P44,
    P95.P45,
    P95.P46,
    P95.P47,
    P95.P48,
    P95.P49,
    P95.P50,
    P95.P51,
    P95.P52,
    P95.P53,
    P95.P54,
    P95.P55,
    P95.P56,
    P95.P57,
    P95.P58,
    P95.P59,
    P95.P60,
    P95.P61,
    P95.P62,
    P95.P63,
    P95.P64,
    P95.P65,
    P95.P66,
    P95.P67,
    P95.P68,
    P95.P69,
    P95.P70,
    P95.P71,
    P95.P72,
    P95.P73,
    P95.P74,
    P95.P75,
    P95.P76,
    P95.P77,
    P95.P78,
    P95.P79,
    P95.P80,
    P95.P81,
    P95.P82,
    P95.P83,
    P95.P84,
    P95.P85,
    P95.P86,
    P95.P87,
    P95.P88,
    P95.P89,
    P95.P90,
    P95.P91,
    P95.P92,
    P95.P93,
    P95.P94,
    P95.P95,
    P95.P96,
    P95.P97,
    P95.P98,
    P95.P99,
    mean.Mean, 
    mean.StandardDeviation, 
FROM
    (
        SELECT
            BackendName,
            ANY_VALUE(P1) AS P1,
            ANY_VALUE(P2) AS P2,
            ANY_VALUE(P3) AS P3,
            ANY_VALUE(P4) AS P4,
            ANY_VALUE(P5) AS P5,
            ANY_VALUE(P6) AS P6,
            ANY_VALUE(P7) AS P7,
            ANY_VALUE(P8) AS P8,
            ANY_VALUE(P9) AS P9,
            ANY_VALUE(P10) AS P10,
            ANY_VALUE(P11) AS P11,
            ANY_VALUE(P12) AS P12,
            ANY_VALUE(P13) AS P13,
            ANY_VALUE(P14) AS P14,
            ANY_VALUE(P15) AS P15,
            ANY_VALUE(P16) AS P16,
            ANY_VALUE(P17) AS P17,
            ANY_VALUE(P18) AS P18,
            ANY_VALUE(P19) AS P19,
            ANY_VALUE(P20) AS P20,
            ANY_VALUE(P21) AS P21,
            ANY_VALUE(P22) AS P22,
            ANY_VALUE(P23) AS P23,
            ANY_VALUE(P24) AS P24,
            ANY_VALUE(P25) AS P25,
            ANY_VALUE(P26) AS P26,
            ANY_VALUE(P27) AS P27,
            ANY_VALUE(P28) AS P28,
            ANY_VALUE(P29) AS P29,
            ANY_VALUE(P30) AS P30,
            ANY_VALUE(P31) AS P31,
            ANY_VALUE(P32) AS P32,
            ANY_VALUE(P33) AS P33,
            ANY_VALUE(P34) AS P34,
            ANY_VALUE(P35) AS P35,
            ANY_VALUE(P36) AS P36,
            ANY_VALUE(P37) AS P37,
            ANY_VALUE(P38) AS P38,
            ANY_VALUE(P39) AS P39,
            ANY_VALUE(P40) AS P40,
            ANY_VALUE(P41) AS P41,
            ANY_VALUE(P42) AS P42,
            ANY_VALUE(P43) AS P43,
            ANY_VALUE(P44) AS P44,
            ANY_VALUE(P45) AS P45,
            ANY_VALUE(P46) AS P46,
            ANY_VALUE(P47) AS P47,
            ANY_VALUE(P48) AS P48,
            ANY_VALUE(P49) AS P49,
            ANY_VALUE(P50) AS P50,
            ANY_VALUE(P51) AS P51,
            ANY_VALUE(P52) AS P52,
            ANY_VALUE(P53) AS P53,
            ANY_VALUE(P54) AS P54,
            ANY_VALUE(P55) AS P55,
            ANY_VALUE(P56) AS P56,
            ANY_VALUE(P57) AS P57,
            ANY_VALUE(P58) AS P58,
            ANY_VALUE(P59) AS P59,
            ANY_VALUE(P60) AS P60,
            ANY_VALUE(P61) AS P61,
            ANY_VALUE(P62) AS P62,
            ANY_VALUE(P63) AS P63,
            ANY_VALUE(P64) AS P64,
            ANY_VALUE(P65) AS P65,
            ANY_VALUE(P66) AS P66,
            ANY_VALUE(P67) AS P67,
            ANY_VALUE(P68) AS P68,
            ANY_VALUE(P69) AS P69,
            ANY_VALUE(P70) AS P70,
            ANY_VALUE(P71) AS P71,
            ANY_VALUE(P72) AS P72,
            ANY_VALUE(P73) AS P73,
            ANY_VALUE(P74) AS P74,
            ANY_VALUE(P75) AS P75,
            ANY_VALUE(P76) AS P76,
            ANY_VALUE(P77) AS P77,
            ANY_VALUE(P78) AS P78,
            ANY_VALUE(P79) AS P79,
            ANY_VALUE(P80) AS P80,
            ANY_VALUE(P81) AS P81,
            ANY_VALUE(P82) AS P82,
            ANY_VALUE(P83) AS P83,
            ANY_VALUE(P84) AS P84,
            ANY_VALUE(P85) AS P85,
            ANY_VALUE(P86) AS P86,
            ANY_VALUE(P87) AS P87,
            ANY_VALUE(P88) AS P88,
            ANY_VALUE(P89) AS P89,
            ANY_VALUE(P90) AS P90,
            ANY_VALUE(P91) AS P91,
            ANY_VALUE(P92) AS P92,
            ANY_VALUE(P93) AS P93,
            ANY_VALUE(P94) AS P94,
            ANY_VALUE(P95) AS P95,
            ANY_VALUE(P96) AS P96,
            ANY_VALUE(P97) AS P97,
            ANY_VALUE(P98) AS P98,
            ANY_VALUE(P99) AS P99,
            FROM (
                SELECT
                    BackendName,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.01) OVER(PARTITION BY BackendDisruption.BackendName) AS P1,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.02) OVER(PARTITION BY BackendDisruption.BackendName) AS P2,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.03) OVER(PARTITION BY BackendDisruption.BackendName) AS P3,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.04) OVER(PARTITION BY BackendDisruption.BackendName) AS P4,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.05) OVER(PARTITION BY BackendDisruption.BackendName) AS P5,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.06) OVER(PARTITION BY BackendDisruption.BackendName) AS P6,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.07) OVER(PARTITION BY BackendDisruption.BackendName) AS P7,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.08) OVER(PARTITION BY BackendDisruption.BackendName) AS P8,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.09) OVER(PARTITION BY BackendDisruption.BackendName) AS P9,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.10) OVER(PARTITION BY BackendDisruption.BackendName) AS P10,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.11) OVER(PARTITION BY BackendDisruption.BackendName) AS P11,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.12) OVER(PARTITION BY BackendDisruption.BackendName) AS P12,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.13) OVER(PARTITION BY BackendDisruption.BackendName) AS P13,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.14) OVER(PARTITION BY BackendDisruption.BackendName) AS P14,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.15) OVER(PARTITION BY BackendDisruption.BackendName) AS P15,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.16) OVER(PARTITION BY BackendDisruption.BackendName) AS P16,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.17) OVER(PARTITION BY BackendDisruption.BackendName) AS P17,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.18) OVER(PARTITION BY BackendDisruption.BackendName) AS P18,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.19) OVER(PARTITION BY BackendDisruption.BackendName) AS P19,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.20) OVER(PARTITION BY BackendDisruption.BackendName) AS P20,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.21) OVER(PARTITION BY BackendDisruption.BackendName) AS P21,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.22) OVER(PARTITION BY BackendDisruption.BackendName) AS P22,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.23) OVER(PARTITION BY BackendDisruption.BackendName) AS P23,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.24) OVER(PARTITION BY BackendDisruption.BackendName) AS P24,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.25) OVER(PARTITION BY BackendDisruption.BackendName) AS P25,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.26) OVER(PARTITION BY BackendDisruption.BackendName) AS P26,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.27) OVER(PARTITION BY BackendDisruption.BackendName) AS P27,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.28) OVER(PARTITION BY BackendDisruption.BackendName) AS P28,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.29) OVER(PARTITION BY BackendDisruption.BackendName) AS P29,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.30) OVER(PARTITION BY BackendDisruption.BackendName) AS P30,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.31) OVER(PARTITION BY BackendDisruption.BackendName) AS P31,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.32) OVER(PARTITION BY BackendDisruption.BackendName) AS P32,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.33) OVER(PARTITION BY BackendDisruption.BackendName) AS P33,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.34) OVER(PARTITION BY BackendDisruption.BackendName) AS P34,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.35) OVER(PARTITION BY BackendDisruption.BackendName) AS P35,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.36) OVER(PARTITION BY BackendDisruption.BackendName) AS P36,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.37) OVER(PARTITION BY BackendDisruption.BackendName) AS P37,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.38) OVER(PARTITION BY BackendDisruption.BackendName) AS P38,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.39) OVER(PARTITION BY BackendDisruption.BackendName) AS P39,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.40) OVER(PARTITION BY BackendDisruption.BackendName) AS P40,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.41) OVER(PARTITION BY BackendDisruption.BackendName) AS P41,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.42) OVER(PARTITION BY BackendDisruption.BackendName) AS P42,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.43) OVER(PARTITION BY BackendDisruption.BackendName) AS P43,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.44) OVER(PARTITION BY BackendDisruption.BackendName) AS P44,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.45) OVER(PARTITION BY BackendDisruption.BackendName) AS P45,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.46) OVER(PARTITION BY BackendDisruption.BackendName) AS P46,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.47) OVER(PARTITION BY BackendDisruption.BackendName) AS P47,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.48) OVER(PARTITION BY BackendDisruption.BackendName) AS P48,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.49) OVER(PARTITION BY BackendDisruption.BackendName) AS P49,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.50) OVER(PARTITION BY BackendDisruption.BackendName) AS P50,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.51) OVER(PARTITION BY BackendDisruption.BackendName) AS P51,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.52) OVER(PARTITION BY BackendDisruption.BackendName) AS P52,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.53) OVER(PARTITION BY BackendDisruption.BackendName) AS P53,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.54) OVER(PARTITION BY BackendDisruption.BackendName) AS P54,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.55) OVER(PARTITION BY BackendDisruption.BackendName) AS P55,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.56) OVER(PARTITION BY BackendDisruption.BackendName) AS P56,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.57) OVER(PARTITION BY BackendDisruption.BackendName) AS P57,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.58) OVER(PARTITION BY BackendDisruption.BackendName) AS P58,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.59) OVER(PARTITION BY BackendDisruption.BackendName) AS P59,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.60) OVER(PARTITION BY BackendDisruption.BackendName) AS P60,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.61) OVER(PARTITION BY BackendDisruption.BackendName) AS P61,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.62) OVER(PARTITION BY BackendDisruption.BackendName) AS P62,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.63) OVER(PARTITION BY BackendDisruption.BackendName) AS P63,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.64) OVER(PARTITION BY BackendDisruption.BackendName) AS P64,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.65) OVER(PARTITION BY BackendDisruption.BackendName) AS P65,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.66) OVER(PARTITION BY BackendDisruption.BackendName) AS P66,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.67) OVER(PARTITION BY BackendDisruption.BackendName) AS P67,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.68) OVER(PARTITION BY BackendDisruption.BackendName) AS P68,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.69) OVER(PARTITION BY BackendDisruption.BackendName) AS P69,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.70) OVER(PARTITION BY BackendDisruption.BackendName) AS P70,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.71) OVER(PARTITION BY BackendDisruption.BackendName) AS P71,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.72) OVER(PARTITION BY BackendDisruption.BackendName) AS P72,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.73) OVER(PARTITION BY BackendDisruption.BackendName) AS P73,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.74) OVER(PARTITION BY BackendDisruption.BackendName) AS P74,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.75) OVER(PARTITION BY BackendDisruption.BackendName) AS P75,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.76) OVER(PARTITION BY BackendDisruption.BackendName) AS P76,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.77) OVER(PARTITION BY BackendDisruption.BackendName) AS P77,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.78) OVER(PARTITION BY BackendDisruption.BackendName) AS P78,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.79) OVER(PARTITION BY BackendDisruption.BackendName) AS P79,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.80) OVER(PARTITION BY BackendDisruption.BackendName) AS P80,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.81) OVER(PARTITION BY BackendDisruption.BackendName) AS P81,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.82) OVER(PARTITION BY BackendDisruption.BackendName) AS P82,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.83) OVER(PARTITION BY BackendDisruption.BackendName) AS P83,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.84) OVER(PARTITION BY BackendDisruption.BackendName) AS P84,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.85) OVER(PARTITION BY BackendDisruption.BackendName) AS P85,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.86) OVER(PARTITION BY BackendDisruption.BackendName) AS P86,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.87) OVER(PARTITION BY BackendDisruption.BackendName) AS P87,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.88) OVER(PARTITION BY BackendDisruption.BackendName) AS P88,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.89) OVER(PARTITION BY BackendDisruption.BackendName) AS P89,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.90) OVER(PARTITION BY BackendDisruption.BackendName) AS P90,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.91) OVER(PARTITION BY BackendDisruption.BackendName) AS P91,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.92) OVER(PARTITION BY BackendDisruption.BackendName) AS P92,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.93) OVER(PARTITION BY BackendDisruption.BackendName) AS P93,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.94) OVER(PARTITION BY BackendDisruption.BackendName) AS P94,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.95) OVER(PARTITION BY BackendDisruption.BackendName) AS P95,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.96) OVER(PARTITION BY BackendDisruption.BackendName) AS P96,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.97) OVER(PARTITION BY BackendDisruption.BackendName) AS P97,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.98) OVER(PARTITION BY BackendDisruption.BackendName) AS P98,
                    PERCENTILE_CONT(BackendDisruption.DisruptionSeconds, 0.99) OVER(PARTITION BY BackendDisruption.BackendName) AS P99,
                FROM
                    DATA_SET_LOCATION.BackendDisruption as BackendDisruption
                INNER JOIN
                    DATA_SET_LOCATION.BackendDisruption_JobRuns as JobRuns on JobRuns.Name = BackendDisruption.JobRunName
                WHERE
                    JobRuns.StartTime BETWEEN TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 10 DAY)
                AND
                    TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 3 DAY)
                AND
                    JobRuns.JobName = @JobName
            )
            GROUP BY
                BackendName
      ) p95
LEFT JOIN
    (
        SELECT
            BackendName,
            AVG(BackendDisruption.DisruptionSeconds) as Mean,
            STDDEV(BackendDisruption.DisruptionSeconds) as StandardDeviation,
            FROM
                DATA_SET_LOCATION.BackendDisruption as BackendDisruption
            INNER JOIN
                DATA_SET_LOCATION.BackendDisruption_JobRuns as JobRuns on JobRuns.Name = BackendDisruption.JobRunName
            WHERE
                JobRuns.StartTime BETWEEN TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 10 DAY)
            AND
                TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 3 DAY)
            AND
                JobRuns.JobName = @JobName
            GROUP BY
                BackendName
      ) mean
ON
    (p95.BackendName = mean.BackendName)
`)
	query := c.client.Query(queryString)
	query.QueryConfig.Parameters = []bigquery.QueryParameter{
		{Name: "JobName", Value: jobName},
	}

	it, err := query.Read(ctx)
	if err != nil {
		return nil, err
	}

	for {
		row := jobrunaggregatorapi.BackendDisruptionStatisticsRow{}
		err := it.Next(&row)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		rows = append(rows, row)
	}

	return rows, nil
}

func (c *ciDataClient) GetLastAggregationForJob(ctx context.Context, frequency, jobName string) (*jobrunaggregatorapi.AggregatedTestRunRow, error) {
	frequencyTable, err := c.tableForFrequency(frequency)
	if err != nil {
		return nil, err
	}
	queryString := strings.Replace(
		`SELECT *
FROM DATA_SET_LOCATION.TABLE_NAME
WHERE TABLE_NAME.JobName = @JobName
ORDER BY TABLE_NAME.AggregationStartDate DESC
LIMIT 1`,
		"TABLE_NAME", frequencyTable, -1)

	queryString = c.dataCoordinates.SubstituteDataSetLocation(queryString)

	query := c.client.Query(queryString)
	query.QueryConfig.Parameters = []bigquery.QueryParameter{
		{Name: "JobName", Value: jobName},
	}
	lastJobRunRow, err := query.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query aggregation table with %q: %w", queryString, err)
	}
	lastJobRun := &jobrunaggregatorapi.AggregatedTestRunRow{}
	err = lastJobRunRow.Next(lastJobRun)
	if err == iterator.Done {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return lastJobRun, nil
}

func (c *ciDataClient) tableForFrequency(frequency string) (string, error) {
	switch frequency {
	case "ByOneWeek":
		return "TestRuns_Summary_Last200Runs", nil

	default:
		return "", fmt.Errorf("unrecognized frequency: %q", frequency)
	}
}

func (c *ciDataClient) ListUnifiedTestRunsForJobAfterDay(ctx context.Context, jobName string, startDay time.Time) (*UnifiedTestRunRowIterator, error) {
	queryString := c.dataCoordinates.SubstituteDataSetLocation(
		`SELECT *
FROM DATA_SET_LOCATION.UnifiedTestRuns
WHERE UnifiedTestRuns.JobRunStartTime >= @TimeCutOff and UnifiedTestRuns.JobName = @JobName
ORDER BY UnifiedTestRuns.JobRunStartTime ASC
`)

	query := c.client.Query(queryString)
	query.QueryConfig.Parameters = []bigquery.QueryParameter{
		{Name: "TimeCutOff", Value: startDay},
		{Name: "JobName", Value: jobName},
	}
	it, err := query.Read(ctx)
	if err != nil {
		return nil, err
	}
	return &UnifiedTestRunRowIterator{delegatedIterator: it}, nil
}

func (c *ciDataClient) ListReleaseTags(ctx context.Context) (sets.String, error) {
	set := sets.String{}
	queryString := c.dataCoordinates.SubstituteDataSetLocation(`SELECT distinct(ReleaseTag) FROM DATA_SET_LOCATION.ReleaseTags`)
	query := c.client.Query(queryString)
	it, err := query.Read(ctx)
	if err != nil {
		return nil, err
	}
	for {
		row := jobrunaggregatorapi.ReleaseRow{}
		err := it.Next(&row)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		set.Insert(row.ReleaseTag)
	}

	return set, nil
}

type UnifiedTestRunRowIterator struct {
	delegatedIterator *bigquery.RowIterator
}

func (it *UnifiedTestRunRowIterator) Next() (*jobrunaggregatorapi.UnifiedTestRunRow, error) {
	ret := &jobrunaggregatorapi.UnifiedTestRunRow{}
	err := it.delegatedIterator.Next(ret)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (c *ciDataClient) GetJobRunForJobNameBeforeTime(ctx context.Context, jobName string, targetTime time.Time) (*jobrunaggregatorapi.JobRunRow, error) {
	queryString := c.dataCoordinates.SubstituteDataSetLocation(
		`SELECT *
FROM DATA_SET_LOCATION.JobRuns
WHERE JobRuns.StartTime <= @TimeCutOff and JobRuns.JobName = @JobName
ORDER BY JobRuns.StartTime DESC
LIMIT 1
`)

	query := c.client.Query(queryString)
	query.QueryConfig.Parameters = []bigquery.QueryParameter{
		{Name: "TimeCutOff", Value: targetTime},
		{Name: "JobName", Value: jobName},
	}
	rowIterator, err := query.Read(ctx)
	if err != nil {
		return nil, err
	}

	ret := &jobrunaggregatorapi.JobRunRow{}
	err = rowIterator.Next(ret)
	if err == iterator.Done {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (c *ciDataClient) GetJobRunForJobNameAfterTime(ctx context.Context, jobName string, targetTime time.Time) (*jobrunaggregatorapi.JobRunRow, error) {
	queryString := c.dataCoordinates.SubstituteDataSetLocation(
		`SELECT *
FROM DATA_SET_LOCATION.JobRuns
WHERE JobRuns.StartTime >= @TimeCutOff and JobRuns.JobName = @JobName
ORDER BY JobRuns.StartTime ASC
LIMIT 1
`)

	query := c.client.Query(queryString)
	query.QueryConfig.Parameters = []bigquery.QueryParameter{
		{Name: "TimeCutOff", Value: targetTime},
		{Name: "JobName", Value: jobName},
	}
	rowIterator, err := query.Read(ctx)
	if err != nil {
		return nil, err
	}

	ret := &jobrunaggregatorapi.JobRunRow{}
	err = rowIterator.Next(ret)
	if err == iterator.Done {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (c *ciDataClient) ListAggregatedTestRunsForJob(ctx context.Context, frequency, jobName string, startDay time.Time) ([]jobrunaggregatorapi.AggregatedTestRunRow, error) {
	frequencyTable, err := c.tableForFrequency(frequency)
	if err != nil {
		return nil, err
	}
	queryString := strings.Replace(
		`SELECT *
FROM DATA_SET_LOCATION.TABLE_NAME
WHERE TABLE_NAME.JobName = @JobName
`,
		"TABLE_NAME", frequencyTable, -1)

	queryString = c.dataCoordinates.SubstituteDataSetLocation(queryString)

	query := c.client.Query(queryString)
	query.QueryConfig.Parameters = []bigquery.QueryParameter{
		{Name: "JobName", Value: jobName},
	}
	rows, err := query.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query job table with %q: %w", queryString, err)
	}
	ret := []jobrunaggregatorapi.AggregatedTestRunRow{}
	for {
		job := &jobrunaggregatorapi.AggregatedTestRunRow{}
		err = rows.Next(job)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		ret = append(ret, *job)
	}

	return ret, nil
}

func GetUTCDay(in time.Time) time.Time {
	year, month, day := in.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
