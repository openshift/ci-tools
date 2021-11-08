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

type JobLister interface {
	ListAllJobs(ctx context.Context) ([]jobrunaggregatorapi.JobRow, error)
}

type CIDataClient interface {
	JobLister
	AggregationJobClient
	TestRunUploadClient
	DisruptionUploadClient
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
	// the JobRun.Name is always increasing, so we can sort by that name.  The starttime is based on the prowjob
	// time and I don't think that is coordinated.
	queryString := c.dataCoordinates.SubstituteDataSetLocation(
		`
SELECT distinct(JobRuns.Name), JobRuns.StartTime
FROM DATA_SET_LOCATION.JobRuns 
INNER JOIN DATA_SET_LOCATION.TestRuns on TestRuns.JobRunName = JobRuns.Name
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

func (c *ciDataClient) GetLastJobRunWithDisruptionDataForJobName(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error) {
	// the JobRun.Name is always increasing, so we can sort by that name.  The starttime is based on the prowjob
	// time and I don't think that is coordinated.
	// the disruption jobrun table is now distinct, so we will use the disruption jobrun table as authoritative for what data should and should not
	// be uploaded rather than using the absence of backenddisruption itself.  Some jobs don't include this data so we end up
	// inserting many duplicated entries
	queryString := c.dataCoordinates.SubstituteDataSetLocation(
		`
SELECT *
FROM DATA_SET_LOCATION.` + jobrunaggregatorapi.DisruptionJobRunTableName + ` as JobRuns
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
	case "ByOneDay":
		return "TestRunSummaryPerDay", nil
	case "ByOneWeek":
		return "TestRunSummaryPerWeek", nil

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
WHERE TABLE_NAME.AggregationStartDate >= @TimeCutOff AND TABLE_NAME.JobName = @JobName
`,
		"TABLE_NAME", frequencyTable, -1)

	queryString = c.dataCoordinates.SubstituteDataSetLocation(queryString)

	query := c.client.Query(queryString)
	query.QueryConfig.Parameters = []bigquery.QueryParameter{
		{Name: "TimeCutOff", Value: startDay},
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
