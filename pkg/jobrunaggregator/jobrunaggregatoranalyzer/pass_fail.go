package jobrunaggregatoranalyzer

import (
	"context"
	"fmt"
	"strconv"
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

const newReleaseJobRunsThreshold = 100
const fallBackMessagePrefix = "(Using previous release historical data from job: %s)\n"

type baseline interface {
	CheckFailed(ctx context.Context, jobName string, suiteNames []string, testCaseDetails *jobrunaggregatorlib.TestCaseDetails) (status testCaseStatus, message string, err error)
	CheckDisruptionMeanWithinFiveStandardDeviations(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend, masterNodesUpdated string) (failedJobRunsIDs []string, successfulJobRunIDs []string, status testCaseStatus, message string, err error)
	CheckDisruptionMeanWithinOneStandardDeviation(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend, masterNodesUpdated string) (failedJobRunsIDs []string, successfulJobRunIDs []string, status testCaseStatus, message string, err error)
	CheckPercentileDisruption(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult,
		backend string, percentile int, fixedGraceSeconds int, masterNodesUpdated string) (failureJobRunIDs []string, successJobRunIDs []string, status testCaseStatus, message string, err error)
	CheckPercentileRankDisruption(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string, maxDisruptionSeconds int, masterNodesUpdated string) (failureJobRunIDs []string, successJobRunIDs []string, status testCaseStatus, message string, err error)
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
		currDetails := &jobrunaggregatorlib.TestCaseDetails{}
		if err := yaml.Unmarshal([]byte(currTestCase.SystemOut), currDetails); err != nil {
			return err
		}

		var status testCaseStatus
		var message string
		var err error
		// TODO once we are ready to stop failing on aggregating the availability tests, we write something here to ignore
		//  the aggregated tests when they fail.  In actuality, this may never be the case, since we're likely to make the
		//  individual tests nearly always pass.
		// if jobrunaggregatorlib.IsDisruptionTest(currTestCase.Name) {
		// }

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
	fallBackJobName     string
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

func getMajor(in string) (int, error) {
	major, err := strconv.ParseInt(strings.Split(in, ".")[0], 10, 32)
	if err != nil {
		return 0, err
	}
	return int(major), err
}

func getMinor(in string) (int, error) {
	minor, err := strconv.ParseInt(strings.Split(in, ".")[1], 10, 32)
	if err != nil {
		return 0, err
	}
	return int(minor), err
}

func normalizeJobName(jobName, fromRelease, toRelease string) string {
	newJobName := strings.Replace(jobName, toRelease, "", -1)
	return strings.Replace(newJobName, fromRelease, "", -1)
}

func (a *weeklyAverageFromTenDays) getNormalizedFallBackJobName(ctx context.Context, jobName string) (string, error) {
	allJobs, err := a.bigQueryClient.ListAllJobs(ctx)
	if err != nil {
		return jobName, err
	}
	var targetFromRelease, targetToRelease string
	var job *jobrunaggregatorapi.JobRow
	for _, j := range allJobs {
		if j.JobName == jobName {
			job = &j
			break
		}
	}
	if job != nil {
		if len(job.FromRelease) > 0 {
			fromReleaseMajor, err1 := getMajor(job.FromRelease)
			fromReleaseMinor, err2 := getMinor(job.FromRelease)
			if err1 != nil || err2 != nil {
				fmt.Printf("Error parsing from release %s. Will not fall back to previous release data.\n", job.FromRelease)
				return jobName, nil
			}
			targetFromRelease = fmt.Sprintf("%d.%d", fromReleaseMajor, fromReleaseMinor-1)
		}
		if len(job.Release) > 0 {
			toReleaseMajor, err1 := getMajor(job.Release)
			toReleaseMinor, err2 := getMinor(job.Release)
			if err1 != nil || err2 != nil {
				fmt.Printf("Error parsing release %s. Will not fall back to previous release data.\n", job.Release)
				return jobName, nil
			}
			targetToRelease = fmt.Sprintf("%d.%d", toReleaseMajor, toReleaseMinor-1)
		}

		normalizedJobName := normalizeJobName(job.JobName, job.FromRelease, job.Release)
		for _, j := range allJobs {
			if j.Architecture == job.Architecture &&
				j.Topology == job.Topology &&
				j.Network == job.Network &&
				j.Platform == job.Platform &&
				j.FromRelease == targetFromRelease &&
				j.Release == targetToRelease &&
				j.IPMode == job.IPMode &&
				normalizeJobName(j.JobName, j.FromRelease, j.Release) == normalizedJobName {
				return j.JobName, nil
			}
		}
	}
	return jobName, nil
}

func (a *weeklyAverageFromTenDays) getDisruptionByBackend(ctx context.Context, masterNodesUpdated string) (map[string]backendDisruptionStats, string, error) {
	a.queryDisruptionOnce.Do(func() {
		jobName := a.jobName
		count, err := a.bigQueryClient.GetBackendDisruptionRowCountByJob(ctx, jobName, masterNodesUpdated)
		if err != nil {
			a.queryDisruptionErr = err
			return
		}
		if count < newReleaseJobRunsThreshold {
			jobName, err = a.getNormalizedFallBackJobName(ctx, a.jobName)
			if err != nil {
				a.queryDisruptionErr = err
				return
			}
			a.fallBackJobName = jobName
		}
		rows, err := a.bigQueryClient.GetBackendDisruptionStatisticsByJob(ctx, jobName, masterNodesUpdated)
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

	return a.disruptionByBackend, a.fallBackJobName, a.queryDisruptionErr
}

func (a *weeklyAverageFromTenDays) CheckDisruptionMeanWithinFiveStandardDeviations(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string, masterNodesUpdated string) ([]string, []string, testCaseStatus, string, error) {
	return a.checkDisruptionMean(ctx, jobRunIDToAvailabilityResultForBackend, backend, meanPlusFiveStandardDeviations, masterNodesUpdated)
}

func (a *weeklyAverageFromTenDays) CheckDisruptionMeanWithinOneStandardDeviation(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string, masterNodesUpdated string) ([]string, []string, testCaseStatus, string, error) {
	return a.checkDisruptionMean(ctx, jobRunIDToAvailabilityResultForBackend, backend, meanPlusOneStandardDeviation, masterNodesUpdated)
}

type disruptionThresholdFunc func(stats backendDisruptionStats) float64

func meanPlusFiveStandardDeviations(historicalDisruptionStatistic backendDisruptionStats) float64 {
	return historicalDisruptionStatistic.rowData.Mean + (5 * historicalDisruptionStatistic.rowData.StandardDeviation)
}

func meanPlusOneStandardDeviation(historicalDisruptionStatistic backendDisruptionStats) float64 {
	return historicalDisruptionStatistic.rowData.Mean + historicalDisruptionStatistic.rowData.StandardDeviation
}

func (a *weeklyAverageFromTenDays) checkDisruptionMean(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string, disruptonThresholdFn disruptionThresholdFunc, masterNodesUpdated string) ([]string, []string, testCaseStatus, string, error) {
	failedJobRunsIDs := []string{}
	successfulJobRunIDs := []string{}

	historicalDisruption, fallBackJobName, err := a.getDisruptionByBackend(ctx, masterNodesUpdated)
	messagePrefix := ""
	if len(fallBackJobName) > 0 {
		messagePrefix = fmt.Sprintf(fallBackMessagePrefix, fallBackJobName)
	}
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
			"%s\nFailed: Mean disruption of %s is %.2f seconds is more than the failureThreshold of the weekly historical mean from 10 days ago: %s",
			messagePrefix,
			backend,
			meanDisruption,
			historicalString), nil
	}

	return failedJobRunsIDs, successfulJobRunIDs, testCasePassed, fmt.Sprintf(
		"%s\nPassed: Mean disruption of %s is %.2f seconds is less than failureThreshold of the weekly historical mean from 10 days ago: %s",
		messagePrefix,
		backend,
		meanDisruption,
		historicalString,
	), nil
}

func (a *weeklyAverageFromTenDays) CheckPercentileDisruption(
	ctx context.Context,
	jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult,
	backend string,
	percentile int,
	fixedGraceSeconds int,
	masterNodesUpdated string) ([]string, []string, testCaseStatus, string, error) {

	historicalDisruption, fallBackJobName, err := a.getDisruptionByBackend(ctx, masterNodesUpdated)
	if err != nil {
		message := fmt.Sprintf("error getting historical disruption data, skipping: %v\n", err)
		failureJobRunIDs := sets.StringKeySet(jobRunIDToAvailabilityResultForBackend).List()
		return failureJobRunIDs, []string{}, testCaseSkipped, message, nil
	}
	messagePrefix := ""
	if len(fallBackJobName) > 0 {
		messagePrefix = fmt.Sprintf(fallBackMessagePrefix, fallBackJobName)
	}
	historicalDisruptionStatistic, ok := historicalDisruption[backend]

	// if we have no data, then we won't have enough indexes, so we get an out of range.
	// this happens when we add new disruption tests, so we just skip instead
	if !ok {
		message := "We have no historical data."
		failureJobRunIDs := sets.StringKeySet(jobRunIDToAvailabilityResultForBackend).List()
		return failureJobRunIDs, []string{}, testCaseSkipped, message, nil
	}

	failureJobRunIDs, successJobRunIDs, testCaseFailed, summary := a.checkPercentileDisruptionWithGrace(
		jobRunIDToAvailabilityResultForBackend, historicalDisruptionStatistic, percentile, fixedGraceSeconds)
	return failureJobRunIDs, successJobRunIDs, testCaseFailed, messagePrefix + summary, nil
}

func (a *weeklyAverageFromTenDays) checkPercentileDisruptionWithGrace(jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, historicalDisruptionStatistic backendDisruptionStats, thresholdPercentile int, fixedGraceSeconds int) ([]string, []string, testCaseStatus, string) {
	historicalThreshold := historicalDisruptionStatistic.percentileByIndex[thresholdPercentile]
	requiredNumberOfPasses, noGraceFailureJobRunIDs, noGraceSuccessJobRunIDs, noGraceTestCasePassed, noGraceSummary := a.innerCheckPercentileDisruptionWithGrace(jobRunIDToAvailabilityResultForBackend, historicalThreshold, thresholdPercentile, 0)
	numberOfPasses := len(noGraceSuccessJobRunIDs)
	if numberOfPasses >= requiredNumberOfPasses {
		return noGraceFailureJobRunIDs, noGraceSuccessJobRunIDs, noGraceTestCasePassed, noGraceSummary
	}

	thresholdWithGrace := historicalThreshold + float64(fixedGraceSeconds)

	_, withGraceFailureJobRunIDs, withGraceSuccessJobRunIDs, withGraceTestCasePassed, withGraceSummary :=
		a.innerCheckPercentileDisruptionWithGrace(jobRunIDToAvailabilityResultForBackend, thresholdWithGrace, thresholdPercentile, fixedGraceSeconds)
	return withGraceFailureJobRunIDs, withGraceSuccessJobRunIDs, withGraceTestCasePassed, withGraceSummary
}

func (a *weeklyAverageFromTenDays) checkPercentileDisruptionWithoutGrace(jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, historicalDisruptionStatistic backendDisruptionStats, thresholdPercentile int) ([]string, []string, testCaseStatus, string) {
	historicalThreshold := historicalDisruptionStatistic.percentileByIndex[thresholdPercentile]
	_, noGraceFailureJobRunIDs, noGraceSuccessJobRunIDs, noGraceTestCasePassed, noGraceSummary := a.innerCheckPercentileDisruptionWithGrace(jobRunIDToAvailabilityResultForBackend, historicalThreshold, thresholdPercentile, 0)
	return noGraceFailureJobRunIDs, noGraceSuccessJobRunIDs, noGraceTestCasePassed, noGraceSummary
}

func (a *weeklyAverageFromTenDays) innerCheckPercentileDisruptionWithGrace(
	jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult,
	threshold float64, thresholdPercentile int, graceSeconds int) (int, []string, []string, testCaseStatus, string) {
	failureJobRunIDs := []string{}
	successJobRunIDs := []string{}
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
	// We need to come back and revisit the possibility of removing this adjustment.
	requiredNumberOfPasses = requiredNumberOfPasses - 1 // subtracting one because our current sample missed by one

	if requiredNumberOfPasses <= 0 {
		message := fmt.Sprintf("Current percentile is so low that we cannot latch, skipping (P%d=%.2fs successes=%v failures=%v)", thresholdPercentile, threshold, successRuns, failureRuns)
		failureJobRunIDs = sets.StringKeySet(jobRunIDToAvailabilityResultForBackend).List()
		return requiredNumberOfPasses, failureJobRunIDs, successJobRunIDs, testCaseSkipped, message
	}

	if numberOfPasses == 0 {
		summary := fmt.Sprintf("Zero successful runs, we require at least one success to pass  (P%d=%.2fs failures=%v)", thresholdPercentile, threshold, failureRuns)
		return requiredNumberOfPasses, failureJobRunIDs, successJobRunIDs, testCaseFailed, summary
	}
	if numberOfAttempts < 3 {
		summary := fmt.Sprintf("We require at least three attempts to pass  (P%d=%.2fs successes=%v failures=%v)",
			thresholdPercentile, threshold, successRuns, failureRuns)
		return requiredNumberOfPasses, failureJobRunIDs, successJobRunIDs, testCaseFailed, summary
	}

	graceAdded := ""
	if graceSeconds > 0 {
		graceAdded = fmt.Sprintf("(grace=%d) ", graceSeconds)
	}

	if numberOfPasses < requiredNumberOfPasses {
		summary := fmt.Sprintf("Failed: Passed %d times, failed %d times.  (P%d=%.2fs %srequiredPasses=%d successes=%v failures=%v)",
			numberOfPasses,
			numberOfFailures,
			thresholdPercentile, threshold, graceAdded,
			requiredNumberOfPasses,
			successRuns, failureRuns,
		)
		return requiredNumberOfPasses, failureJobRunIDs, successJobRunIDs, testCaseFailed, summary
	}

	summary := fmt.Sprintf("Passed: Passed %d times, failed %d times.  (P%d=%.2fs %srequiredPasses=%d successes=%v failures=%v)",
		numberOfPasses,
		numberOfFailures,
		thresholdPercentile, threshold, graceAdded,
		requiredNumberOfPasses,
		successRuns, failureRuns,
	)
	return requiredNumberOfPasses, failureJobRunIDs, successJobRunIDs, testCasePassed, summary
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

func (a *weeklyAverageFromTenDays) CheckPercentileRankDisruption(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string, maxDisruptionSeconds int, masterNodesUpdated string) ([]string, []string, testCaseStatus, string, error) {
	historicalDisruption, fallBackJobName, err := a.getDisruptionByBackend(ctx, masterNodesUpdated)
	if err != nil {
		message := fmt.Sprintf("error getting historical disruption data, skipping: %v\n", err)
		failureJobRunIDs := sets.StringKeySet(jobRunIDToAvailabilityResultForBackend).List()
		return failureJobRunIDs, []string{}, testCaseSkipped, message, nil
	}

	messagePrefix := ""
	if len(fallBackJobName) > 0 {
		messagePrefix = fmt.Sprintf(fallBackMessagePrefix, fallBackJobName)
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

	failureJobRunIDs, successJobRunIDs, testCasePassed, summary := a.checkPercentileDisruptionWithoutGrace(jobRunIDToAvailabilityResultForBackend, historicalDisruptionStatistic, thresholdPercentile)
	return failureJobRunIDs, successJobRunIDs, testCasePassed, messagePrefix + summary, nil
}

func (a *weeklyAverageFromTenDays) CheckFailed(ctx context.Context, jobName string, suiteNames []string, testCaseDetails *jobrunaggregatorlib.TestCaseDetails) (testCaseStatus, string, error) {
	if reason := testShouldAlwaysPass(jobName, testCaseDetails.Name, testCaseDetails.TestSuiteName); len(reason) > 0 {
		reason := fmt.Sprintf("always passing %q: %v\n", testCaseDetails.Name, reason)
		return testCasePassed, reason, nil
	}
	if !didTestRun(testCaseDetails) {
		return testCasePassed, "did not run", nil
	}

	if reason := testShouldNeverFail(testCaseDetails.Name); len(reason) > 0 && len(testCaseDetails.Failures) > 0 {
		return testCaseFailed, reason, nil
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

// testShouldNeverFail returns a reason if a test should never fail, or empty string if we should proceed with
// normal test analysis.
func testShouldNeverFail(testName string) string {
	if testName == "Undiagnosed panic detected in pod" {
		return "Pods must not panic"
	}

	return ""
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

func didTestRun(testCaseDetails *jobrunaggregatorlib.TestCaseDetails) bool {
	if len(testCaseDetails.Passes) == 0 && len(testCaseDetails.Failures) == 0 {
		return false
	}
	return true
}

func getAttempts(testCaseDetails *jobrunaggregatorlib.TestCaseDetails) int {
	// if the same job run has a pass and a fail, or multiple passes, it only counts as a single attempt.
	jobRunsThatAttempted := sets.New[string]()
	for _, failure := range testCaseDetails.Failures {
		jobRunsThatAttempted.Insert(failure.JobRunID)
	}
	for _, pass := range testCaseDetails.Passes {
		jobRunsThatAttempted.Insert(pass.JobRunID)
	}

	return len(jobRunsThatAttempted)
}

func getNumberOfPasses(testCaseDetails *jobrunaggregatorlib.TestCaseDetails) int {
	// if the same job run has multiple passes, it only counts as a single pass.
	jobRunsThatPassed := sets.New[string]()
	for _, pass := range testCaseDetails.Passes {
		jobRunsThatPassed.Insert(pass.JobRunID)
	}

	return len(jobRunsThatPassed)
}

func getNumberOfFailures(testCaseDetails *jobrunaggregatorlib.TestCaseDetails) int {
	return len(getFailedJobNames(testCaseDetails))
}

func getFailedJobNames(testCaseDetails *jobrunaggregatorlib.TestCaseDetails) sets.Set[string] {
	// if the same job run has multiple failures, it only counts as a single failure.
	jobRunsThatFailed := sets.New[string]()
	for _, failure := range testCaseDetails.Failures {
		jobRunsThatFailed.Insert(failure.JobRunID)
	}

	jobRunsThatPassed := sets.New[string]()
	for _, pass := range testCaseDetails.Passes {
		jobRunsThatPassed.Insert(pass.JobRunID)
	}

	jobRunsThatFailed = jobRunsThatFailed.Difference(jobRunsThatPassed)

	return jobRunsThatFailed
}
