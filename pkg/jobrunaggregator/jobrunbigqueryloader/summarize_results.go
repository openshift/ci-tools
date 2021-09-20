package jobrunbigqueryloader

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/api/iterator"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type JobRunsBigQuerySummarizerOptions struct {
	Frequency       string
	SummaryDuration time.Duration
	CIDataClient    jobrunaggregatorlib.CIDataClient
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates

	AggregatedTestRunInserter BigQueryInserter
}

func (o *JobRunsBigQuerySummarizerOptions) Run(ctx context.Context) error {
	jobs, err := o.CIDataClient.ListAllJobs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get jobs: %w", err)
	}

	fmt.Printf("Launching threads to upload test runs\n")

	waitGroup := sync.WaitGroup{}
	errCh := make(chan error, len(jobs))
	for i := range jobs {
		job := jobs[i]
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()

			currOptions := JobRunBigQuerySummarizerOptions{
				JobName:                   job.JobName,
				Frequency:                 o.Frequency,
				SummaryDuration:           o.SummaryDuration,
				CIDataClient:              o.CIDataClient,
				AggregatedTestRunInserter: o.AggregatedTestRunInserter,
			}
			err := currOptions.Run(ctx)
			if err != nil {
				errCh <- err
			}
		}()
	}
	waitGroup.Wait()
	close(errCh)

	errs := []error{}
	for err := range errCh {
		errs = append(errs, err)
	}

	return utilerrors.NewAggregate(errs)
}

// JobRunBigQuerySummarizerOptions
// 1. reads the target big query table for the latest date of analysis
// 2. reads the unified test run view since the latest analysis date
// 3. constructs new summary data for each day
type JobRunBigQuerySummarizerOptions struct {
	JobName   string
	Frequency string

	SummaryDuration           time.Duration
	CIDataClient              jobrunaggregatorlib.CIDataClient
	AggregatedTestRunInserter BigQueryInserter
}

func (o *JobRunBigQuerySummarizerOptions) Run(ctx context.Context) error {
	fmt.Print(o.prefixLog("Reading existing data from job runs of type.\n"))

	firstAggregatedDay := false
	currAggregatedStartDay := jobrunaggregatorlib.GetUTCDay(time.Now().UTC().Add(-1 * 365 * 24 * time.Hour))
	lastUnifiedTestRun, err := o.CIDataClient.GetLastAggregationForJob(ctx, o.Frequency, o.JobName)
	switch {
	case err != nil:
		return fmt.Errorf("failed reading first row in aggregation table: %w", err)
	case lastUnifiedTestRun == nil:
		fmt.Print(o.prefixLog("No existing data.\n"))
		firstAggregatedDay = true
	default:
		currAggregatedStartDay = jobrunaggregatorlib.GetUTCDay(lastUnifiedTestRun.AggregationStartDate.Add(24 * time.Hour))
		fmt.Print(o.prefixLog("Found existing result, starting day is %v.\n"), currAggregatedStartDay)
	}

	fmt.Print(o.prefixLog("Querying unified test run data\n"))
	interestingUnifiedRows, err := o.CIDataClient.ListUnifiedTestRunsForJobAfterDay(ctx, o.JobName, currAggregatedStartDay)
	if err != nil {
		return err
	}

	outOfRows := false
	needToSetDay := firstAggregatedDay
	testRunsToAggregate := []*jobrunaggregatorapi.UnifiedTestRunRow{}
	nextTestRunsToAggregate := []*jobrunaggregatorapi.UnifiedTestRunRow{}
	nextAggregatedStartDay := currAggregatedStartDay
	i := 0
	for !outOfRows {
		currAggregatedStartDay = nextAggregatedStartDay
		nextAggregatedStartDay = currAggregatedStartDay.Add(24 * time.Hour)
		lastSummarizationDay := currAggregatedStartDay.Add(o.SummaryDuration)

		// we track the previous because sometimes the days overlap
		previousTestRunsToAggregate := testRunsToAggregate
		testRunsToAggregate = []*jobrunaggregatorapi.UnifiedTestRunRow{}
		for i := range previousTestRunsToAggregate {
			curr := previousTestRunsToAggregate[i]
			if curr.JobRunStartTime.Before(currAggregatedStartDay) {
				continue
			}
			testRunsToAggregate = append(testRunsToAggregate, curr)
		}
		// now add the one for this summarization that we read to decide the previous one was finished.
		testRunsToAggregate = append(testRunsToAggregate, nextTestRunsToAggregate...)
		nextTestRunsToAggregate = []*jobrunaggregatorapi.UnifiedTestRunRow{}

		fmt.Printf(o.prefixLog("Processing for next interval starting at %v ending at %v, carrying over %d rows\n"), currAggregatedStartDay, lastSummarizationDay, len(testRunsToAggregate))

		newRowsRead := 0
		for {
			newRowsRead++
			currUnifiedRow, err := interestingUnifiedRows.Next()
			if err == iterator.Done {
				outOfRows = true
				break
			}
			if err != nil {
				return err
			}
			currDay := jobrunaggregatorlib.GetUTCDay(currUnifiedRow.JobRunStartTime)
			if needToSetDay {
				currAggregatedStartDay = currDay
				nextAggregatedStartDay = currDay.Add(24 * time.Hour)
				lastSummarizationDay = currAggregatedStartDay.Add(o.SummaryDuration)
				fmt.Printf(o.prefixLog("Force set the interval: %v to %v\n"), currAggregatedStartDay, lastSummarizationDay)
				needToSetDay = false
			}
			if currDay == lastSummarizationDay || currDay.After(lastSummarizationDay) {
				fmt.Printf(o.prefixLog("  finished collecting for: %v to %v\n"), currAggregatedStartDay, currDay)
				nextTestRunsToAggregate = append(nextTestRunsToAggregate, currUnifiedRow)
				newRowsRead--
				break
			}

			testRunsToAggregate = append(testRunsToAggregate, currUnifiedRow)
		}
		fmt.Printf(o.prefixLog("  read %d new rows, have total %d\n"), newRowsRead, len(testRunsToAggregate))

		if outOfRows {
			fmt.Print(o.prefixLog("Out of rows without seeing end of the summary duration.  Returning without a write.\n"))
			break
		}

		// if we're not out of rows, then we have collected all the data to aggregate
		// this map ensures that as we go through this list, we only allow a *single* result for each test,jobRun tuple
		// it also handles setting "flaked"
		testJobRunToStatus := map[testJobRunKey]string{}
		jobName := ""
		clusterName := ""
		for _, currTestRun := range testRunsToAggregate {
			if currTestRun.TestStatus == "Skipped" || currTestRun.TestStatus == "Unknown" {
				continue
			}

			key := testJobRunKey{
				TestName:   currTestRun.TestName,
				JobRunName: currTestRun.JobRunName,
			}
			existingStatus := testJobRunToStatus[key]
			newStatus := existingStatus
			switch existingStatus {
			case "Passed":
				if currTestRun.TestStatus == "Failed" {
					newStatus = "Flaked"
				}
			case "Failed":
				if currTestRun.TestStatus == "Passed" {
					newStatus = "Flaked"
				}
			default:
				newStatus = currTestRun.TestStatus
			}
			testJobRunToStatus[key] = newStatus

			jobName = currTestRun.JobName

			// this isn't the correct way to determine dominant, we'd have to track each cluster and count.
			// TODO fix this calculation
			clusterName = currTestRun.Cluster
		}
		fmt.Printf(o.prefixLog("  have %d records to summarize\n"), len(testJobRunToStatus))

		// Now we count the status based on the test name
		testToAggregation := map[string]jobrunaggregatorapi.AggregatedTestRunRow{}
		for key, status := range testJobRunToStatus {
			currAggregation := testToAggregation[key.TestName]
			switch status {
			case "Passed":
				currAggregation.PassCount++
			case "Failed":
				currAggregation.FailCount++
			case "Flaked":
				currAggregation.FlakeCount++
			default:
				return fmt.Errorf("that's weird: %q", status)
			}
			totalRuns := currAggregation.PassCount + currAggregation.FailCount + currAggregation.FlakeCount
			notFailedTotal := currAggregation.PassCount + currAggregation.FlakeCount

			currAggregation.PassPercentage = currAggregation.PassCount * 100 / totalRuns
			currAggregation.WorkingPercentage = notFailedTotal * 100 / totalRuns

			testToAggregation[key.TestName] = currAggregation
		}

		toWrite := []jobrunaggregatorapi.AggregatedTestRunRow{}
		for _, testName := range sets.StringKeySet(testToAggregation).List() {
			//fmt.Printf("  uploading %q\n", testName)
			currAggregation := testToAggregation[testName]
			currAggregation.AggregationStartDate = currAggregatedStartDay
			currAggregation.TestName = testName
			currAggregation.JobName = jobName
			currAggregation.DominantCluster = clusterName

			toWrite = append(toWrite, currAggregation)
		}

		fmt.Printf(o.prefixLog("  about to upload %d records\n"), len(toWrite))
		if err := o.AggregatedTestRunInserter.Put(ctx, toWrite); err != nil {
			return err
		}
		fmt.Printf(o.prefixLog("Successfully uploaded %d records\n"), len(toWrite))

		// for now, only do 100 days
		i++
		if i > 100 {
			break
		}
	}

	return nil
}

func (o *JobRunBigQuerySummarizerOptions) prefixLog(format string) string {
	prefix := fmt.Sprintf("job/%v %s: ", o.JobName, o.Frequency)
	return prefix + " " + format
}

type testJobRunKey struct {
	TestName   string
	JobRunName string
}
