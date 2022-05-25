package jobrunaggregatoranalyzer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v2"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

type testCaseStatus string

const (
	testCaseFailed  testCaseStatus = "failed"
	testCasePassed  testCaseStatus = "passed"
	testCaseSkipped testCaseStatus = "skipped"
)

type baseline interface {
	CheckFailed(ctx context.Context, jobName string, suiteNames []string, testCaseDetails *TestCaseDetails) (status testCaseStatus, message string, err error)
	CheckDisruptionMeanWithinFiveStandardDeviations(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string) (failedJobRunsIDs []string, successfulJobRunIDs []string, status testCaseStatus, message string, err error)
	CheckDisruptionMeanWithinOneStandardDeviation(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string) (failedJobRunsIDs []string, successfulJobRunIDs []string, status testCaseStatus, message string, err error)
	CheckPercentileDisruption(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string, percentile int) (failureJobRunIDs []string, successJobRunIDs []string, status testCaseStatus, message string, err error)
	CheckPercentileRankDisruption(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string, maxDisruptionSeconds int) (failureJobRunIDs []string, successJobRunIDs []string, status testCaseStatus, message string, err error)
}

func assignPassFail(ctx context.Context, jobName string, combined *junit.TestSuites, baselinePassFail baseline) error {
	for _, currTestSuite := range combined.Suites {
		if err := assignPassFailForTestSuite(ctx, jobName, []string{}, currTestSuite, baselinePassFail); err != nil {
			return err
		}

	}

	return nil
}

func assignPassFailForTestSuite(ctx context.Context, jobName string, parentTestSuites []string, combined *junit.TestSuite, baselinePassFail baseline) error {
	failureCount := uint(0)

	currSuiteNames := append(parentTestSuites, combined.Name)
	for _, currTestSuite := range combined.Children {
		if err := assignPassFailForTestSuite(ctx, jobName, currSuiteNames, currTestSuite, baselinePassFail); err != nil {
			return err
		}
		failureCount += currTestSuite.NumFailed
	}

	for i := range combined.TestCases {
		currTestCase := combined.TestCases[i]
		currDetails := &TestCaseDetails{}
		if err := yaml.Unmarshal([]byte(currTestCase.SystemOut), currDetails); err != nil {
			return err
		}

		var status testCaseStatus
		var message string
		var err error
		// TODO once we are ready to stop failing on aggregating the availability tests, we write something here to ignore
		//  the aggregated tests when they fail.  In actuality, this may never be the case, since we're likely to make the
		//  individual tests nearly always pass.
		//if jobrunaggregatorlib.IsDisruptionTest(currTestCase.Name) {
		//}

		status, message, err = baselinePassFail.CheckFailed(ctx, jobName, currSuiteNames, currDetails)
		if err != nil {
			return err
		}

		currDetails.Summary = message
		detailsBytes, err := yaml.Marshal(currDetails)
		if err != nil {
			return err
		}
		currTestCase.SystemOut = string(detailsBytes)

		if status == testCaseFailed {
			currTestCase.FailureOutput = &junit.FailureOutput{
				Message: message,
				Output:  currTestCase.SystemOut,
			}
			failureCount++
		}
	}

	combined.NumFailed = failureCount

	return nil
}

/* the linter won't allow this.  You'll probably need at some point.
type simpleBaseline struct{}

func (*simpleBaseline) FailureMessage(ctx context.Context, suiteNames []string, testCaseDetails *TestCaseDetails) (string, error) {
	if !didTestRun(testCaseDetails) {
		return "", nil
	}

	if len(testCaseDetails.Passes) < 2 {
		return fmt.Sprintf("Passed %d times, failed %d times, skipped %d times: we require at least two passes",
			len(testCaseDetails.Passes),
			len(testCaseDetails.Failures),
			len(testCaseDetails.Skips),
		), nil
	}

	if passPercentage := getWorkingPercentage(testCaseDetails); passPercentage < 60 {
		return fmt.Sprintf("Pass percentage is %d\nPassed %d times, failed %d times, skipped %d times: we require at least 60%% passing",
			int(passPercentage),
			len(testCaseDetails.Passes),
			len(testCaseDetails.Failures),
			len(testCaseDetails.Skips),
		), nil
	}

	return "", nil
}

func getWorkingPercentage(testCaseDetails *TestCaseDetails) float32 {
	// if the same job run has a pass and a fail, then it's a flake.  For now, we will consider those as passes by subtracting
	// them (for now), from the failure count.
	failureCount := len(testCaseDetails.Failures)
	for _, failure := range testCaseDetails.Failures {
		for _, pass := range testCaseDetails.Passes {
			if pass.JobRunID == failure.JobRunID {
				failureCount--
				break
			}
		}
	}

	return float32(len(testCaseDetails.Passes)) / float32(len(testCaseDetails.Passes)+failureCount) * 100.0
}
*/

// weeklyAverageFromTenDays gets the weekly average pass rate from ten days ago so that the latest tests do not
// influence the pass/fail criteria.
type weeklyAverageFromTenDays struct {
	jobName                 string
	startDay                time.Time
	minimumNumberOfAttempts int
	bigQueryClient          jobrunaggregatorlib.CIDataClient

	queryTestRunsOnce        sync.Once
	queryTestRunsErr         error
	aggregatedTestRunsByName map[TestKey]jobrunaggregatorapi.AggregatedTestRunRow

	queryDisruptionOnce sync.Once
	queryDisruptionErr  error
	disruptionByBackend map[string]backendDisruptionStats
}

type TestKey struct {
	TestCaseName          string
	CombinedTestSuiteName string
}

func newWeeklyAverageFromTenDaysAgo(jobName string, startDay time.Time, minimumNumberOfAttempts int, bigQueryClient jobrunaggregatorlib.CIDataClient) baseline {
	tenDayAgo := jobrunaggregatorlib.GetUTCDay(startDay).Add(-10 * 24 * time.Hour)

	return &weeklyAverageFromTenDays{
		jobName:                  jobName,
		startDay:                 tenDayAgo,
		minimumNumberOfAttempts:  minimumNumberOfAttempts,
		bigQueryClient:           bigQueryClient,
		queryTestRunsOnce:        sync.Once{},
		queryTestRunsErr:         nil,
		aggregatedTestRunsByName: nil,
		disruptionByBackend:      make(map[string]backendDisruptionStats),
	}
}

func (a *weeklyAverageFromTenDays) getAggregatedTestRuns(ctx context.Context) (map[TestKey]jobrunaggregatorapi.AggregatedTestRunRow, error) {
	a.queryTestRunsOnce.Do(func() {
		rows, err := a.bigQueryClient.ListAggregatedTestRunsForJob(ctx, "ByOneWeek", a.jobName, a.startDay)
		a.aggregatedTestRunsByName = map[TestKey]jobrunaggregatorapi.AggregatedTestRunRow{}
		if err != nil {
			a.queryTestRunsErr = err
			return
		}
		for i := range rows {
			row := rows[i]
			key := TestKey{
				TestCaseName: row.TestName,
			}
			if row.TestSuiteName.Valid {
				key.CombinedTestSuiteName = row.TestSuiteName.StringVal
			} else {
				key.CombinedTestSuiteName = ""
			}
			a.aggregatedTestRunsByName[key] = row
		}
	})

	return a.aggregatedTestRunsByName, a.queryTestRunsErr
}

func (a *weeklyAverageFromTenDays) getDisruptionByBackend(ctx context.Context) (map[string]backendDisruptionStats, error) {
	a.queryDisruptionOnce.Do(func() {
		rows, err := a.bigQueryClient.GetBackendDisruptionStatisticsByJob(ctx, a.jobName)
		if err != nil {
			a.queryDisruptionErr = err
			return
		}

		a.disruptionByBackend = make(map[string]backendDisruptionStats)
		for i := range rows {
			row := rows[i]
			a.disruptionByBackend[row.BackendName] = constructBackendDisruptionStats(row)
		}

	})

	return a.disruptionByBackend, a.queryDisruptionErr
}

func (a *weeklyAverageFromTenDays) CheckDisruptionMeanWithinFiveStandardDeviations(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string) ([]string, []string, testCaseStatus, string, error) {
	return a.checkDisruptionMean(ctx, jobRunIDToAvailabilityResultForBackend, backend, meanPlusFiveStandardDeviations)
}

func (a *weeklyAverageFromTenDays) CheckDisruptionMeanWithinOneStandardDeviation(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string) ([]string, []string, testCaseStatus, string, error) {
	return a.checkDisruptionMean(ctx, jobRunIDToAvailabilityResultForBackend, backend, meanPlusOneStandardDeviation)
}

type disruptionThresholdFunc func(stats backendDisruptionStats) float64

func meanPlusFiveStandardDeviations(historicalDisruptionStatistic backendDisruptionStats) float64 {
	return historicalDisruptionStatistic.rowData.Mean + (5 * historicalDisruptionStatistic.rowData.StandardDeviation)
}

func meanPlusOneStandardDeviation(historicalDisruptionStatistic backendDisruptionStats) float64 {
	return historicalDisruptionStatistic.rowData.Mean + historicalDisruptionStatistic.rowData.StandardDeviation
}

func (a *weeklyAverageFromTenDays) checkDisruptionMean(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string, disruptonThresholdFn disruptionThresholdFunc) ([]string, []string, testCaseStatus, string, error) {
	failedJobRunsIDs := []string{}
	successfulJobRunIDs := []string{}

	historicalDisruption, err := a.getDisruptionByBackend(ctx)
	if err != nil {
		message := fmt.Sprintf("error getting historical disruption data, skipping: %v\n", err)
		failedJobRunsIDs = sets.StringKeySet(jobRunIDToAvailabilityResultForBackend).List()
		return failedJobRunsIDs, successfulJobRunIDs, testCaseSkipped, message, nil
	}
	historicalDisruptionStatistic, ok := historicalDisruption[backend]

	// if we have no data, then we won't have enough indexes, so we get an out of range.
	// this happens when we add new disruption tests, so we just skip instead
	if !ok {
		message := "We have no historical data."
		failureJobRunIDs := sets.StringKeySet(jobRunIDToAvailabilityResultForBackend).List()
		return failureJobRunIDs, []string{}, testCaseSkipped, message, nil
	}

	// If disruption mean (excluding at most 1 outlier) is greater than 10% of the historical mean,
	// the aggregation fails.
	disruptionThreshold := disruptonThresholdFn(historicalDisruptionStatistic)

	// we always allow at least one second
	if disruptionThreshold < 1 {
		disruptionThreshold = 1
	}

	totalRuns := len(jobRunIDToAvailabilityResultForBackend)
	totalDisruption := 0
	max := 0
	for _, disruption := range jobRunIDToAvailabilityResultForBackend {
		totalDisruption += disruption.SecondsUnavailable
		if disruption.SecondsUnavailable > max {
			max = disruption.SecondsUnavailable
		}
	}

	successRuns := []string{} // each string example: jobRunID=5s
	failureRuns := []string{} // each string example: jobRunID=5s
	for jobRunID, disruption := range jobRunIDToAvailabilityResultForBackend {
		if float64(disruption.SecondsUnavailable) > disruptionThreshold {
			failedJobRunsIDs = append(failedJobRunsIDs, jobRunID)
			failureRuns = append(failureRuns, fmt.Sprintf("%s=%ds", jobRunID, disruption.SecondsUnavailable))
		} else {
			successfulJobRunIDs = append(successfulJobRunIDs, jobRunID)
			successRuns = append(successRuns, fmt.Sprintf("%s=%ds", jobRunID, disruption.SecondsUnavailable))
		}
	}

	// We allow one "mulligan" by throwing away at most one outlier > our p95.
	if float64(max) > historicalDisruptionStatistic.rowData.P95 {
		fmt.Printf("%s throwing away one outlier (outlier=%ds p95=%fs)\n", backend, max, historicalDisruptionStatistic.rowData.P95)
		totalRuns--
		totalDisruption -= max
	}
	meanDisruption := float64(totalDisruption) / float64(totalRuns)
	historicalString := fmt.Sprintf("historicalMean=%.2fs standardDeviation=%.2fs failureThreshold=%.2fs historicalP95=%.2fs successes=%v failures=%v",
		historicalDisruptionStatistic.rowData.Mean,
		historicalDisruptionStatistic.rowData.StandardDeviation,
		disruptionThreshold,
		historicalDisruptionStatistic.rowData.P95,
		successRuns,
		failureRuns,
	)
	fmt.Printf("%s disruption calculated for current runs (%s runs=%d totalDisruptionSecs=%ds mean=%.2fs max=%ds)\n",
		backend, historicalString, totalRuns, totalDisruption, meanDisruption, max)

	if meanDisruption > disruptionThreshold {
		return failedJobRunsIDs, successfulJobRunIDs, testCaseFailed, fmt.Sprintf(
			"Failed: Mean disruption of %s is %.2f seconds is more than the failureThreshold of the weekly historical mean from 10 days ago: %s",
			backend,
			meanDisruption,
			historicalString), nil
	}

	return failedJobRunsIDs, successfulJobRunIDs, testCasePassed, fmt.Sprintf(
		"Passed: Mean disruption of %s is %.2f seconds is less than failureThreshold of the weekly historical mean from 10 days ago: %s",
		backend,
		meanDisruption,
		historicalString,
	), nil
}

func (a *weeklyAverageFromTenDays) CheckPercentileDisruption(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string, percentile int) ([]string, []string, testCaseStatus, string, error) {
	historicalDisruption, err := a.getDisruptionByBackend(ctx)
	if err != nil {
		message := fmt.Sprintf("error getting historical disruption data, skipping: %v\n", err)
		failureJobRunIDs := sets.StringKeySet(jobRunIDToAvailabilityResultForBackend).List()
		return failureJobRunIDs, []string{}, testCaseSkipped, message, nil
	}
	historicalDisruptionStatistic, ok := historicalDisruption[backend]

	// if we have no data, then we won't have enough indexes, so we get an out of range.
	// this happens when we add new disruption tests, so we just skip instead
	if !ok {
		message := "We have no historical data."
		failureJobRunIDs := sets.StringKeySet(jobRunIDToAvailabilityResultForBackend).List()
		return failureJobRunIDs, []string{}, testCaseSkipped, message, nil
	}

	return a.checkPercentileDisruption(jobRunIDToAvailabilityResultForBackend, historicalDisruptionStatistic, percentile)
}

func (a *weeklyAverageFromTenDays) checkPercentileDisruption(jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, historicalDisruptionStatistic backendDisruptionStats, thresholdPercentile int) ([]string, []string, testCaseStatus, string, error) {
	failureJobRunIDs := []string{}
	successJobRunIDs := []string{}
	threshold := historicalDisruptionStatistic.percentileByIndex[thresholdPercentile]
	successRuns := []string{} // each string example: jobRunID=5s
	failureRuns := []string{} // each string example: jobRunID=5s

	for jobRunID, disruption := range jobRunIDToAvailabilityResultForBackend {
		if float64(disruption.SecondsUnavailable) > threshold {
			failureJobRunIDs = append(failureJobRunIDs, jobRunID)
			failureRuns = append(failureRuns, fmt.Sprintf("%s=%ds", jobRunID, disruption.SecondsUnavailable))
		} else {
			successJobRunIDs = append(successJobRunIDs, jobRunID)
			successRuns = append(successRuns, fmt.Sprintf("%s=%ds", jobRunID, disruption.SecondsUnavailable))
		}
	}
	numberOfAttempts := len(successJobRunIDs) + len(failureJobRunIDs)
	numberOfPasses := len(successJobRunIDs)
	numberOfFailures := len(failureJobRunIDs)
	workingPercentage := thresholdPercentile // the percentile is our success percentage
	requiredNumberOfPasses := requiredPassesByPassPercentageByNumberOfAttempts[numberOfAttempts][workingPercentage]
	// TODO try to tighten this after we can keep the test in for about a week.
	requiredNumberOfPasses = requiredNumberOfPasses - 2 // subtracting one because our current sample missed by one

	if requiredNumberOfPasses <= 0 {
		message := fmt.Sprintf("Current percentile is so low that we cannot latch, skipping (P%d=%.2fs successes=%v failures=%v)", thresholdPercentile, threshold, successRuns, failureRuns)
		failureJobRunIDs = sets.StringKeySet(jobRunIDToAvailabilityResultForBackend).List()
		return failureJobRunIDs, successJobRunIDs, testCaseSkipped, message, nil
	}

	if numberOfPasses == 0 {
		summary := fmt.Sprintf("Zero successful runs, we require at least one success to pass  (P%d=%.2fs failures=%v)", thresholdPercentile, threshold, failureRuns)
		return failureJobRunIDs, successJobRunIDs, testCaseFailed, summary, nil
	}
	if numberOfAttempts < 3 {
		summary := fmt.Sprintf("We require at least three attempts to pass  (P%d=%.2fs successes=%v failures=%v)",
			thresholdPercentile, threshold, successRuns, failureRuns)
		return failureJobRunIDs, successJobRunIDs, testCaseFailed, summary, nil
	}

	if numberOfPasses < requiredNumberOfPasses {
		summary := fmt.Sprintf("Failed: Passed %d times, failed %d times.  (P%d=%.2fs requiredPasses=%d successes=%v failures=%v)",
			numberOfPasses,
			numberOfFailures,
			thresholdPercentile, threshold,
			requiredNumberOfPasses,
			successRuns, failureRuns,
		)
		return failureJobRunIDs, successJobRunIDs, testCaseFailed, summary, nil
	}

	summary := fmt.Sprintf("Passed: Passed %d times, failed %d times.  (P%d=%.2fs requiredPasses=%d successes=%v failures=%v)",
		numberOfPasses,
		numberOfFailures,
		thresholdPercentile, threshold,
		requiredNumberOfPasses,
		successRuns, failureRuns,
	)
	return failureJobRunIDs, successJobRunIDs, testCasePassed, summary, nil
}

// getPercentileRank returns the maximum percentile that is at or below the disruptionThresholdSeconds
func getPercentileRank(historicalDisruption backendDisruptionStats, disruptionThresholdSeconds int) int {
	for i := 1; i <= 99; i++ {
		if historicalDisruption.percentileByIndex[i] > float64(disruptionThresholdSeconds) {
			return i - 1
		}
	}
	return 99
}

func (a *weeklyAverageFromTenDays) CheckPercentileRankDisruption(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string, maxDisruptionSeconds int) ([]string, []string, testCaseStatus, string, error) {
	historicalDisruption, err := a.getDisruptionByBackend(ctx)
	if err != nil {
		message := fmt.Sprintf("error getting historical disruption data, skipping: %v\n", err)
		failureJobRunIDs := sets.StringKeySet(jobRunIDToAvailabilityResultForBackend).List()
		return failureJobRunIDs, []string{}, testCaseSkipped, message, nil
	}
	historicalDisruptionStatistic, ok := historicalDisruption[backend]

	// if we have no data, then we won't have enough indexes, so we get an out of range.
	// this happens when we add new disruption tests, so we just skip instead
	if !ok {
		message := "We have no historical data."
		failureJobRunIDs := sets.StringKeySet(jobRunIDToAvailabilityResultForBackend).List()
		return failureJobRunIDs, []string{}, testCaseSkipped, message, nil
	}

	thresholdPercentile := getPercentileRank(historicalDisruptionStatistic, maxDisruptionSeconds)

	return a.checkPercentileDisruption(jobRunIDToAvailabilityResultForBackend, historicalDisruptionStatistic, thresholdPercentile)
}

func (a *weeklyAverageFromTenDays) CheckFailed(ctx context.Context, jobName string, suiteNames []string, testCaseDetails *TestCaseDetails) (testCaseStatus, string, error) {
	if reason := testShouldAlwaysPass(jobName, testCaseDetails.Name, testCaseDetails.TestSuiteName); len(reason) > 0 {
		reason := fmt.Sprintf("always passing %q: %v\n", testCaseDetails.Name, reason)
		return testCasePassed, reason, nil
	}
	if !didTestRun(testCaseDetails) {
		return testCasePassed, "did not run", nil
	}
	numberOfAttempts := getAttempts(testCaseDetails)

	// if most of the job runs skipped this test, then we probably intend to skip the test overall and the failure is actually
	// due to some kind of "couldn't detect that I should skip".
	if len(testCaseDetails.Passes) == 0 && len(testCaseDetails.Skips) > len(testCaseDetails.Failures) {
		return testCasePassed, "probably intended to skip", nil
	}

	numberOfPasses := getNumberOfPasses(testCaseDetails)
	numberOfFailures := getNumberOfFailures(testCaseDetails)
	if numberOfAttempts < a.minimumNumberOfAttempts {
		summary := fmt.Sprintf("Passed %d times, failed %d times, skipped %d times: we require at least %d attempts to have a chance at success",
			numberOfPasses,
			numberOfFailures,
			len(testCaseDetails.Skips),
			a.minimumNumberOfAttempts,
		)
		return testCaseFailed, summary, nil
	}
	if len(testCaseDetails.Passes) < 1 {
		summary := fmt.Sprintf("Passed %d times, failed %d times, skipped %d times: we require at least one pass to consider it a success",
			numberOfPasses,
			numberOfFailures,
			len(testCaseDetails.Skips),
		)
		return testCaseFailed, summary, nil
	}

	aggregatedTestRunsByName, err := a.getAggregatedTestRuns(ctx)
	missingAllHistoricalData := false
	if err != nil {
		fmt.Printf("error getting past reliability data, assume 99%% pass: %v\n", err)
		missingAllHistoricalData = true
	}

	testKey := TestKey{
		TestCaseName:          testCaseDetails.Name,
		CombinedTestSuiteName: testCaseDetails.TestSuiteName,
	}
	averageTestResult, ok := aggregatedTestRunsByName[testKey]
	// the linter requires not setting a default value. This seems strictly worse and more error-prone to me, but
	// I am a slave to the bot.
	var workingPercentage int
	switch {
	case missingAllHistoricalData:
		workingPercentage = 99
	case !ok:
		fmt.Printf("missing historical data for %v, arbitrarily assigning 70%% because David thought it was better than failing\n", testCaseDetails.Name)
		workingPercentage = 70
	default:
		workingPercentage = int(averageTestResult.WorkingPercentage)
	}

	requiredNumberOfPasses := requiredPassesByPassPercentageByNumberOfAttempts[numberOfAttempts][workingPercentage]
	if numberOfPasses < requiredNumberOfPasses {
		summary := fmt.Sprintf("Failed: Passed %d times, failed %d times.  The historical pass rate is %d%%.  The required number of passes is %d.",
			numberOfPasses,
			numberOfFailures,
			workingPercentage,
			requiredNumberOfPasses,
		)
		return testCaseFailed, summary, nil
	}

	return testCasePassed, fmt.Sprintf("Passed: Passed %d times, failed %d times.  The historical pass rate is %d%%.  The required number of passes is %d.",
		numberOfPasses,
		numberOfFailures,
		workingPercentage,
		requiredNumberOfPasses,
	), nil
}

var testsRequiringHistoryRewrite = make(map[testCoordinates]string)

type testCoordinates struct {
	jobName       string
	testName      string
	testSuiteName string
}

func init() {
	/* This is an example for creating testCoordinates for the same test from multiple jobs
	for _, jobName := range []string{
		"periodic-ci-openshift-release-master-ci-4.10-e2e-azure-ovn-upgrade",
		"periodic-ci-openshift-release-master-ci-4.10-upgrade-from-stable-4.9-e2e-azure-upgrade",
	} {
		testsRequiringHistoryRewrite[testCoordinates{
			jobName:       jobName,
			testName:      "[sig-network-edge] Application behind service load balancer with PDB remains available using new connections",
			testSuiteName: "Cluster upgrade",
		}] = "history correction on kube update, expires 2022-01-24 - https://bugzilla.redhat.com/show_bug.cgi?id=2040715"
	}*/

	testsRequiringHistoryRewrite[testCoordinates{
		jobName:       "periodic-ci-openshift-release-master-ci-4.10-upgrade-from-stable-4.9-e2e-aws-ovn-upgrade",
		testName:      "[sig-network] pods should successfully create sandboxes by other",
		testSuiteName: "openshift-tests-upgrade",
	}] = "Test has been failing for a longtime but went undetected"
}

// testShouldAlwaysPass returns a reason if a test should be skipped and considered always passing, or empty
// string if we should proceed with normal test analysis.
func testShouldAlwaysPass(jobName, testName, testSuiteName string) string {
	coordinates := testCoordinates{
		jobName:       jobName,
		testName:      testName,
		testSuiteName: testSuiteName,
	}

	if reason := testsRequiringHistoryRewrite[coordinates]; len(reason) > 0 {
		return reason
	}

	if testSuiteName == "step graph" {
		// examples: "step graph.Run multi-stage test post phase"
		return "step graph tests are added by ci and are not useful for aggregation"
	}

	if strings.Contains(testName, `Cluster should remain functional during upgrade`) {
		// this test is a side-effect of other tests.  For the purpose of aggregation, we can have each individual job run
		// fail this test, but the aggregated output can be successful.
		return "this test is a side-effect of other tests"
	}

	if strings.HasSuffix(testName, "-gather-azure-cli container test") {
		// this is only for gathering artifacts.
		return "used only to collect artifacts"
	}

	if testName == "initialize" {
		// initialize test appears only when job run fails so job run will fail anyway
		//
		return "ignore initialize"
	}

	return ""
}

// these are the required number of passes for a given percentage of historical pass rate.  One var each for
// number of attempts in the aggregated job.
// data here is generated with required-pass-rate.py
var (
	requiredPassesByPassPercentageFor_12_Attempts = []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 0-9
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 10-19
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 20-29
		0, 0, 0, 0, 0, 1, 1, 1, 1, 1, // 30-39
		1, 1, 1, 1, 1, 2, 2, 2, 2, 2, // 40-49
		2, 2, 2, 2, 3, 3, 3, 3, 3, 3, // 50-59
		3, 3, 4, 4, 4, 4, 4, 4, 4, 4, // 60-69
		5, 5, 5, 5, 5, 5, 5, 6, 6, 6, // 70-79
		6, 6, 6, 7, 7, 7, 7, 7, 7, 8, // 80-89
		8, 8, 8, 8, 9, 9, 9, 9, 10, 10, // 90-99
		11,
	}
	requiredPassesByPassPercentageFor_11_Attempts = []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 0-9
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 10-19
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 20-29
		0, 0, 0, 0, 0, 0, 0, 0, 1, 1, // 30-39
		1, 1, 1, 1, 1, 1, 1, 1, 2, 2, // 40-49
		2, 2, 2, 2, 2, 2, 2, 2, 3, 3, // 50-59
		3, 3, 3, 3, 3, 3, 4, 4, 4, 4, // 60-69
		4, 4, 4, 4, 5, 5, 5, 5, 5, 5, // 70-79
		5, 6, 6, 6, 6, 6, 6, 7, 7, 7, // 80-89
		7, 7, 7, 8, 8, 8, 8, 8, 9, 9, // 90-99
		10,
	}
	requiredPassesByPassPercentageFor_10_Attempts = []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 0-9
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 10-19
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 20-29
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 30-39
		0, 1, 1, 1, 1, 1, 1, 1, 1, 1, // 40-49
		1, 1, 2, 2, 2, 2, 2, 2, 2, 2, // 50-59
		2, 2, 3, 3, 3, 3, 3, 3, 3, 3, // 60-69
		3, 4, 4, 4, 4, 4, 4, 4, 4, 5, // 70-79
		5, 5, 5, 5, 5, 5, 6, 6, 6, 6, // 80-89
		6, 6, 7, 7, 7, 7, 7, 7, 8, 8, // 90-99
		9, // 100
	}
	requiredPassesByPassPercentageFor_09_Attempts = []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 0-9
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 10-19
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 20-29
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 30-39
		0, 0, 0, 0, 1, 1, 1, 1, 1, 1, // 40-49
		1, 1, 1, 1, 1, 1, 2, 2, 2, 2, // 50-59
		2, 2, 2, 2, 2, 2, 2, 3, 3, 3, // 60-69
		3, 3, 3, 3, 3, 3, 4, 4, 4, 4, // 70-79
		4, 4, 4, 4, 5, 5, 5, 5, 5, 5, // 80-89
		5, 6, 6, 6, 6, 6, 6, 7, 7, 7, // 90-99
		8, // 100
	}
	requiredPassesByPassPercentageFor_08_Attempts = []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 0-9
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 10-19
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 20-29
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 30-39
		0, 0, 0, 0, 0, 0, 0, 0, 1, 1, // 40-49
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, // 50-59
		1, 2, 2, 2, 2, 2, 2, 2, 2, 2, // 60-69
		2, 2, 3, 3, 3, 3, 3, 3, 3, 3, // 70-79
		3, 3, 4, 4, 4, 4, 4, 4, 4, 4, // 80-89
		5, 5, 5, 5, 5, 5, 6, 6, 6, 6, // 90-99
		7, // 100
	}
	requiredPassesByPassPercentageFor_07_Attempts = []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 0-9
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 10-19
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 20-29
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 30-39
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 40-49
		0, 0, 0, 1, 1, 1, 1, 1, 1, 1, // 50-59
		1, 1, 1, 1, 1, 1, 1, 2, 2, 2, // 60-69
		2, 2, 2, 2, 2, 2, 2, 2, 2, 3, // 70-79
		3, 3, 3, 3, 3, 3, 3, 3, 4, 4, // 80-89
		4, 4, 4, 4, 4, 5, 5, 5, 5, 5, // 90-99
		6, // 100
	}
	requiredPassesByPassPercentageFor_06_Attempts = []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 0-9
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 10-19
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 20-29
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 30-39
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 40-49
		0, 0, 0, 0, 0, 0, 0, 0, 0, 1, // 50-59
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, // 60-69
		1, 1, 1, 1, 2, 2, 2, 2, 2, 2, // 70-79
		2, 2, 2, 2, 2, 2, 3, 3, 3, 3, // 80-89
		3, 3, 3, 3, 3, 4, 4, 4, 4, 4, // 90-99
		5, // 100
	}
	requiredPassesByPassPercentageFor_05_Attempts = []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 0-9
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 10-19
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 20-29
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 30-39
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 40-49
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 50-59
		0, 0, 0, 0, 0, 0, 0, 1, 1, 1, // 60-69
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, // 70-79
		1, 1, 2, 2, 2, 2, 2, 2, 2, 2, // 80-89
		2, 2, 2, 2, 3, 3, 3, 3, 3, 3, // 90-99
		4, // 100
	}
	requiredPassesByPassPercentageFor_04_Attempts = []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 0-9
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 10-19
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 20-29
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 30-39
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 40-49
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 50-59
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 60-69
		0, 0, 0, 0, 0, 0, 1, 1, 1, 1, // 70-79
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, // 80-89
		1, 2, 2, 2, 2, 2, 2, 2, 2, 3, // 90-99
		3, // 100
	}
	requiredPassesByPassPercentageFor_03_Attempts = []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 0-9
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 10-19
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 20-29
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 30-39
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 40-49
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 50-59
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 60-69
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 70-79
		0, 0, 0, 0, 0, 0, 0, 1, 1, 1, // 80-89
		1, 1, 1, 1, 1, 1, 1, 1, 1, 2, // 90-99
		2, // 100
	}
	requiredPassesByPassPercentageFor_02_Attempts = []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 0-9
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 10-19
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 20-29
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 30-39
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 40-49
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 50-59
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 60-69
		0, 0, 0, 0, 0, 0, 0, 0, 0, 1, // 70-79
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, // 80-89
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, // 90-99
		1, // 100
	}
	requiredPassesByPassPercentageFor_01_Attempts = []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 00-09
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 10-19
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 20-29
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 30-39
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 40-49
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 50-59
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 60-69
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 70-79
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 80-89
		0, 0, 0, 0, 0, 0, 1, 1, 1, 1, // 90-99
		1, // 100
	}
	requiredPassesByPassPercentageFor_00_Attempts = []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 00-09
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 10-19
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 20-29
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 30-39
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 40-49
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 50-59
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 60-69
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 70-79
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 80-89
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 90-99
		0, // 100
	}

	requiredPassesByPassPercentageByNumberOfAttempts = [][]int{
		requiredPassesByPassPercentageFor_00_Attempts,
		requiredPassesByPassPercentageFor_01_Attempts,
		requiredPassesByPassPercentageFor_02_Attempts,
		requiredPassesByPassPercentageFor_03_Attempts,
		requiredPassesByPassPercentageFor_04_Attempts,
		requiredPassesByPassPercentageFor_05_Attempts,
		requiredPassesByPassPercentageFor_06_Attempts,
		requiredPassesByPassPercentageFor_07_Attempts,
		requiredPassesByPassPercentageFor_08_Attempts,
		requiredPassesByPassPercentageFor_09_Attempts,
		requiredPassesByPassPercentageFor_10_Attempts,
		requiredPassesByPassPercentageFor_11_Attempts,
		requiredPassesByPassPercentageFor_12_Attempts,
	}
)

func didTestRun(testCaseDetails *TestCaseDetails) bool {
	if len(testCaseDetails.Passes) == 0 && len(testCaseDetails.Failures) == 0 {
		return false
	}
	return true
}

func getAttempts(testCaseDetails *TestCaseDetails) int {
	// if the same job run has a pass and a fail, or multiple passes, it only counts as a single attempt.
	jobRunsThatAttempted := sets.NewString()
	for _, failure := range testCaseDetails.Failures {
		jobRunsThatAttempted.Insert(failure.JobRunID)
	}
	for _, pass := range testCaseDetails.Passes {
		jobRunsThatAttempted.Insert(pass.JobRunID)
	}

	return len(jobRunsThatAttempted)
}

func getNumberOfPasses(testCaseDetails *TestCaseDetails) int {
	// if the same job run has multiple passes, it only counts as a single pass.
	jobRunsThatPassed := sets.NewString()
	for _, pass := range testCaseDetails.Passes {
		jobRunsThatPassed.Insert(pass.JobRunID)
	}

	return len(jobRunsThatPassed)
}

func getNumberOfFailures(testCaseDetails *TestCaseDetails) int {
	return len(getFailedJobNames(testCaseDetails))
}

func getFailedJobNames(testCaseDetails *TestCaseDetails) sets.String {
	// if the same job run has multiple failures, it only counts as a single failure.
	jobRunsThatFailed := sets.NewString()
	for _, failure := range testCaseDetails.Failures {
		jobRunsThatFailed.Insert(failure.JobRunID)
	}

	jobRunsThatPassed := sets.NewString()
	for _, pass := range testCaseDetails.Passes {
		jobRunsThatPassed.Insert(pass.JobRunID)
	}

	jobRunsThatFailed = jobRunsThatFailed.Difference(jobRunsThatPassed)

	return jobRunsThatFailed
}
