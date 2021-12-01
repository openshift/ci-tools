package jobrunaggregatoranalyzer

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v2"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

type baseline interface {
	CheckFailed(ctx context.Context, suiteNames []string, testCaseDetails *TestCaseDetails) (failed bool, message string, err error)
	CheckDisruptionMeanWithinTwoStandardDeviations(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string) (failedJobRunsIDs []string, successfulJobRunIDs []string, failed bool, message string, err error)
	CheckDisruptionMeanWithinOneStandardDeviation(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string) (failedJobRunsIDs []string, successfulJobRunIDs []string, failed bool, message string, err error)
	CheckP95Disruption(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string) (failureJobRunIDs []string, successJobRunIDs []string, failed bool, message string, err error)
}

func assignPassFail(ctx context.Context, combined *junit.TestSuites, baselinePassFail baseline) error {
	for _, currTestSuite := range combined.Suites {
		if err := assignPassFailForTestSuite(ctx, []string{}, currTestSuite, baselinePassFail); err != nil {
			return err
		}

	}

	return nil
}

func assignPassFailForTestSuite(ctx context.Context, parentTestSuites []string, combined *junit.TestSuite, baselinePassFail baseline) error {
	failureCount := uint(0)

	currSuiteNames := append(parentTestSuites, combined.Name)
	for _, currTestSuite := range combined.Children {
		if err := assignPassFailForTestSuite(ctx, currSuiteNames, currTestSuite, baselinePassFail); err != nil {
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

		var failed bool
		var message string
		var err error
		// TODO once we are ready to stop failing on aggregating the availability tests, we write something here to ignore
		//  the aggregated tests when they fail.  In actuality, this may never be the case, since we're likely to make the
		//  individual tests nearly always pass.
		//if jobrunaggregatorlib.IsDisruptionTest(currTestCase.Name) {
		//}

		failed, message, err = baselinePassFail.CheckFailed(ctx, currSuiteNames, currDetails)
		if err != nil {
			return err
		}

		currDetails.Summary = message
		detailsBytes, err := yaml.Marshal(currDetails)
		if err != nil {
			return err
		}
		currTestCase.SystemOut = string(detailsBytes)

		if !failed {
			continue
		}
		currTestCase.FailureOutput = &junit.FailureOutput{
			Message: message,
			Output:  currTestCase.SystemOut,
		}
		failureCount++
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
	aggregatedTestRunsByName map[string]jobrunaggregatorapi.AggregatedTestRunRow

	queryDisruptionOnce sync.Once
	queryDisruptionErr  error
	disruptionByBackend map[string]jobrunaggregatorapi.BackendDisruptionStatisticsRow
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
		disruptionByBackend:      make(map[string]jobrunaggregatorapi.BackendDisruptionStatisticsRow),
	}
}

func (a *weeklyAverageFromTenDays) getAggregatedTestRuns(ctx context.Context) (map[string]jobrunaggregatorapi.AggregatedTestRunRow, error) {
	a.queryTestRunsOnce.Do(func() {
		rows, err := a.bigQueryClient.ListAggregatedTestRunsForJob(ctx, "ByOneWeek", a.jobName, a.startDay)
		a.aggregatedTestRunsByName = map[string]jobrunaggregatorapi.AggregatedTestRunRow{}
		if err != nil {
			a.queryTestRunsErr = err
			return
		}
		for i := range rows {
			row := rows[i]
			a.aggregatedTestRunsByName[row.TestName] = row
		}
	})

	return a.aggregatedTestRunsByName, a.queryTestRunsErr
}

func (a *weeklyAverageFromTenDays) getDisruptionByBackend(ctx context.Context) (map[string]jobrunaggregatorapi.BackendDisruptionStatisticsRow, error) {
	a.queryDisruptionOnce.Do(func() {
		rows, err := a.bigQueryClient.GetBackendDisruptionStatisticsByJob(ctx, a.jobName)
		if err != nil {
			a.queryDisruptionErr = err
			return
		}

		a.disruptionByBackend = make(map[string]jobrunaggregatorapi.BackendDisruptionStatisticsRow)
		for i := range rows {
			row := rows[i]
			a.disruptionByBackend[row.BackendName] = row
		}

	})

	return a.disruptionByBackend, a.queryDisruptionErr
}

func (a *weeklyAverageFromTenDays) CheckDisruptionMeanWithinTwoStandardDeviations(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string) ([]string, []string, bool, string, error) {
	return a.checkDisruptionMean(ctx, jobRunIDToAvailabilityResultForBackend, backend, meanPlusTwoStandardDeviations)
}

func (a *weeklyAverageFromTenDays) CheckDisruptionMeanWithinOneStandardDeviation(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string) ([]string, []string, bool, string, error) {
	return a.checkDisruptionMean(ctx, jobRunIDToAvailabilityResultForBackend, backend, meanPlusOneStandardDeviation)
}

type disruptionThresholdFunc func(jobrunaggregatorapi.BackendDisruptionStatisticsRow) float64

func meanPlusTwoStandardDeviations(historicalDisruptionStatistic jobrunaggregatorapi.BackendDisruptionStatisticsRow) float64 {
	return historicalDisruptionStatistic.Mean + (2 * historicalDisruptionStatistic.StandardDeviation)
}

func meanPlusOneStandardDeviation(historicalDisruptionStatistic jobrunaggregatorapi.BackendDisruptionStatisticsRow) float64 {
	return historicalDisruptionStatistic.Mean + historicalDisruptionStatistic.StandardDeviation
}

func (a *weeklyAverageFromTenDays) checkDisruptionMean(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string, disruptonThresholdFn disruptionThresholdFunc) ([]string, []string, bool, string, error) {
	failedJobRunsIDs := []string{}
	successfulJobRunIDs := []string{}

	missingAllHistoricalData := false
	historicalDisruption, err := a.getDisruptionByBackend(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting past disruption data, assume 1s disruption allowed: %v\n", err)
		missingAllHistoricalData = true
	}
	historicalDisruptionStatistic := historicalDisruption[backend]

	// If disruption mean (excluding at most 1 outlier) is greater than 10% of the historical mean,
	// the aggregation fails.
	disruptionThreshold := float64(1)
	if !missingAllHistoricalData {
		disruptionThreshold = disruptonThresholdFn(historicalDisruptionStatistic)
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
	for jobRunID, disruption := range jobRunIDToAvailabilityResultForBackend {
		if float64(disruption.SecondsUnavailable) > disruptionThreshold {
			failedJobRunsIDs = append(failedJobRunsIDs, jobRunID)
		} else {
			successfulJobRunIDs = append(successfulJobRunIDs, jobRunID)
		}
	}

	// We allow one "mulligan" by throwing away at most one outlier > our p95.
	if float64(max) > historicalDisruptionStatistic.P95 {
		fmt.Printf("%s throwing away one outlier (outlier=%ds p95=%fs)\n", backend, max, historicalDisruptionStatistic.P95)
		totalRuns--
		totalDisruption -= max
	}
	meanDisruption := float64(totalDisruption) / float64(totalRuns)
	historicalString := fmt.Sprintf("historicalMean=%.2fs standardDeviation=%.2fs failureThreshold=%.2fs historicalP95=%.2fs",
		historicalDisruptionStatistic.Mean,
		historicalDisruptionStatistic.StandardDeviation,
		disruptionThreshold,
		historicalDisruptionStatistic.P95)
	fmt.Printf("%s disruption calculated for current runs (%s runs=%d totalDisruptionSecs=%ds mean=%.2fs max=%ds)\n",
		backend, historicalString, totalRuns, totalDisruption, meanDisruption, max)

	if meanDisruption > disruptionThreshold {
		return failedJobRunsIDs, successfulJobRunIDs, true, fmt.Sprintf(
			"Mean disruption of %s is %.2f seconds, which is more than the failureThreshold of the weekly historical mean from 10 days ago: %s, marking this as a failure",
			backend,
			meanDisruption,
			historicalString), nil
	}

	return failedJobRunsIDs, successfulJobRunIDs, false, fmt.Sprintf(
		"Mean disruption of %s is %.2f seconds, which is no more than failureThreshold of the weekly historical mean from 10 days ago: %s. This is OK.",
		backend,
		meanDisruption,
		historicalString,
	), nil
}

func (a *weeklyAverageFromTenDays) CheckP95Disruption(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string) ([]string, []string, bool, string, error) {
	failureJobRunIDs := []string{}
	successJobRunIDs := []string{}

	historicalDisruption, err := a.getDisruptionByBackend(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting past disruption data, assume 0s disruption allowed: %v\n", err)
	}
	historicalDisruptionStatistic := historicalDisruption[backend]

	for jobRunID, disruption := range jobRunIDToAvailabilityResultForBackend {
		if float64(disruption.SecondsUnavailable) > historicalDisruptionStatistic.P95 {
			failureJobRunIDs = append(failureJobRunIDs, jobRunID)
		} else {
			successJobRunIDs = append(successJobRunIDs, jobRunID)
		}
	}
	numberOfAttempts := len(successJobRunIDs) + len(failureJobRunIDs)
	numberOfPasses := len(successJobRunIDs)
	numberOfFailures := len(failureJobRunIDs)

	if numberOfPasses == 0 {
		summary := fmt.Sprintf("Zero successful runs, we require at least one success to pass.  P95=%.2fs", historicalDisruptionStatistic.P95)
		return failureJobRunIDs, successJobRunIDs, true, summary, nil
	}
	if numberOfAttempts < 3 {
		summary := fmt.Sprintf("We require at least three attempts to pass.  P95=%.2fs", historicalDisruptionStatistic.P95)
		return failureJobRunIDs, successJobRunIDs, true, summary, nil
	}

	workingPercentage := 95 // P95 should be a 95% success rate on all runs
	requiredNumberOfPasses := requiredPassesByPassPercentageByNumberOfAttempts[numberOfAttempts][workingPercentage]
	// TODO try to tighten this after we can keep the test in for about a week.
	requiredNumberOfPasses = requiredNumberOfPasses - 1 // subtracting one because our current sample missed by one
	if numberOfPasses < requiredNumberOfPasses {
		summary := fmt.Sprintf("Failed: Passed %d times, failed %d times.  The historical P95=%.2fs.  The required number of passes is %d.",
			numberOfPasses,
			numberOfFailures,
			historicalDisruptionStatistic.P95,
			requiredNumberOfPasses,
		)
		return failureJobRunIDs, successJobRunIDs, true, summary, nil
	}

	summary := fmt.Sprintf("Passed: Passed %d times, failed %d times.  The historical P95=%.2fs.  The required number of passes is %d.",
		numberOfPasses,
		numberOfFailures,
		historicalDisruptionStatistic.P95,
		requiredNumberOfPasses,
	)
	return failureJobRunIDs, successJobRunIDs, false, summary, nil
}

func (a *weeklyAverageFromTenDays) CheckFailed(ctx context.Context, suiteNames []string, testCaseDetails *TestCaseDetails) (bool, string, error) {
	if testShouldAlwaysPass(testCaseDetails.Name) {
		fmt.Printf("always passing %q\n", testCaseDetails.Name)
		return false, "always passing", nil
	}
	if !didTestRun(testCaseDetails) {
		return false, "did not run", nil
	}
	numberOfAttempts := getAttempts(testCaseDetails)

	// if most of the job runs skipped this test, then we probably intend to skip the test overall and the failure is actually
	// due to some kind of "couldn't detect that I should skip".
	if len(testCaseDetails.Passes) == 0 && len(testCaseDetails.Skips) > len(testCaseDetails.Failures) {
		return false, "probably intended to skip", nil
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
		return true, summary, nil
	}
	if len(testCaseDetails.Passes) < 1 {
		summary := fmt.Sprintf("Passed %d times, failed %d times, skipped %d times: we require at least one pass to consider it a success",
			numberOfPasses,
			numberOfFailures,
			len(testCaseDetails.Skips),
		)
		return true, summary, nil
	}

	aggregatedTestRunsByName, err := a.getAggregatedTestRuns(ctx)
	missingAllHistoricalData := false
	if err != nil {
		fmt.Printf("error getting past reliability data, assume 99%% pass: %v", err)
		missingAllHistoricalData = true
	}

	averageTestResult, ok := aggregatedTestRunsByName[testCaseDetails.Name]
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
		workingPercentage = averageTestResult.WorkingPercentage
	}

	requiredNumberOfPasses := requiredPassesByPassPercentageByNumberOfAttempts[numberOfAttempts][workingPercentage]
	if numberOfPasses < requiredNumberOfPasses {
		summary := fmt.Sprintf("Failed: Passed %d times, failed %d times.  The historical pass rate is %d%%.  The required number of passes is %d.",
			numberOfPasses,
			numberOfFailures,
			workingPercentage,
			requiredNumberOfPasses,
		)
		return true, summary, nil
	}

	return false, fmt.Sprintf("Passed: Passed %d times, failed %d times.  The historical pass rate is %d%%.  The required number of passes is %d.",
		numberOfPasses,
		numberOfFailures,
		workingPercentage,
		requiredNumberOfPasses,
	), nil
}

func testShouldAlwaysPass(name string) bool {
	if strings.HasPrefix(name, "Run multi-stage test ") {
		switch {
		// used to aggregate overall upgrade result for a single job.  Since we aggregated all the junits, we don't care about this
		// sub-aggregation. The analysis job runs can all fail on different tests, but the aggregated job will succeed.
		case strings.HasSuffix(name, "-openshift-e2e-test container test"):
			return true
		}
	}
	if strings.Contains(name, `Cluster should remain functional during upgrade`) {
		// this test is a side-effect of other tests.  For the purpose of aggregation, we can have each individual job run
		// fail this test, but the aggregated output can be successful.
		return true
	}

	return false
}

// these are the required number of passes for a given percentage of historical pass rate.  One var each for
// number of attempts in the aggregated job.
var (
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
