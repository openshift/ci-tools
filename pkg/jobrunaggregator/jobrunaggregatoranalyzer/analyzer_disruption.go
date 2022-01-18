package jobrunaggregatoranalyzer

import (
	"context"
	"fmt"
	"os"
	"path"

	"gopkg.in/yaml.v2"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

func (o *JobRunAggregatorAnalyzerOptions) CalculateDisruptionTestSuite(ctx context.Context, jobGCSBucketRoot string, finishedJobsToAggregate []jobrunaggregatorapi.JobRunInfo) (*junit.TestSuite, error) {
	disruptionJunitSuite := &junit.TestSuite{
		Name:      "BackendDisruption",
		TestCases: []*junit.TestCase{},
	}
	collectedDataTestCase := &junit.TestCase{
		Name: "should collect disruption data",
	}
	disruptionJunitSuite.TestCases = append(disruptionJunitSuite.TestCases, collectedDataTestCase)

	jobRunIDToBackendNameToAvailabilityResult, err := getDisruptionByJobRunID(ctx, finishedJobsToAggregate)
	if jobRunIDToBackendNameToAvailabilityResult != nil {
		rawDataBytes, err := yaml.Marshal(jobRunIDToBackendNameToAvailabilityResult)
		if err != nil {
			collectedDataTestCase.SystemOut = string(rawDataBytes)
		}
	}
	switch {
	case len(jobRunIDToBackendNameToAvailabilityResult) < 3 && err != nil:
		return nil, err
	case len(jobRunIDToBackendNameToAvailabilityResult) < 3 && err == nil:
		collectedDataTestCase.FailureOutput = &junit.FailureOutput{
			Message: "not enough data to aggregate",
			Output:  collectedDataTestCase.SystemOut,
		}
		disruptionJunitSuite.NumFailed++
		return disruptionJunitSuite, nil

	default:
		// ignore the errors if we have at least three results
		fmt.Fprintf(os.Stderr, "Could not fetch backend disruption data for all runs %v\n", err)
	}

	testCaseNamePatternToDisruptionCheckFn := map[string]disruptionJunitCheckFunc{
		"%s mean disruption should be less than historical plus two standard deviations": o.passFailCalculator.CheckDisruptionMeanWithinTwoStandardDeviations,
		// TODO add a SKIP mechanism to disruptionJunitCheckFunc instead of the fail bool
		//"%s mean disruption should be less than historical plus one standard deviation":  o.passFailCalculator.CheckDisruptionMeanWithinOneStandardDeviation,
		"%s disruption P70 should not be worse":  checkPercentileDisruption(o.passFailCalculator, 70), // for 7 attempts, this  gives us a latch on getting worse
		"%s disruption P85 should not be worse":  checkPercentileDisruption(o.passFailCalculator, 85), // for 5 attempts, this gives us a latch on getting worse.
		"%s disruption P95 should not be worse":  checkPercentileDisruption(o.passFailCalculator, 95),
		"%s zero-disruption should not be worse": checkPercentileRankDisruption(o.passFailCalculator, 0),
	}

	for _, testCaseNamePattern := range sets.StringKeySet(testCaseNamePatternToDisruptionCheckFn).List() {
		disruptionCheckFn := testCaseNamePatternToDisruptionCheckFn[testCaseNamePattern]

		allBackends := getAllDisruptionBackendNames(jobRunIDToBackendNameToAvailabilityResult)
		for _, backendName := range allBackends.List() {
			jobRunIDToAvailabilityResultForBackend := getDisruptionForBackend(jobRunIDToBackendNameToAvailabilityResult, backendName)
			failedJobRunIDs, successfulJobRunIDs, _, message, err := disruptionCheckFn(ctx, jobRunIDToAvailabilityResultForBackend, backendName)
			if err != nil {
				return nil, err
			}

			// we are correcting our disruption numbers, so we are forcing everything to skipped until 12/15
			status := testCaseSkipped

			testCaseName := fmt.Sprintf(testCaseNamePattern, backendName)
			testSuiteName := "aggregated-disruption"
			junitTestCase, err := disruptionToJUnitTestCase(testCaseName, testSuiteName, jobGCSBucketRoot, failedJobRunIDs, successfulJobRunIDs, status, message)
			if err != nil {
				return nil, err
			}
			disruptionJunitSuite.TestCases = append(disruptionJunitSuite.TestCases, junitTestCase)

			if status == testCaseFailed {
				disruptionJunitSuite.NumFailed++
			}
		}
	}

	return disruptionJunitSuite, nil
}

func checkPercentileDisruption(passFailCalculator baseline, percentile int) disruptionJunitCheckFunc {
	return func(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string) (failedJobRunsIDs []string, successfulJobRunIDs []string, status testCaseStatus, message string, err error) {
		return passFailCalculator.CheckPercentileDisruption(ctx, jobRunIDToAvailabilityResultForBackend, backend, percentile)
	}
}

func checkPercentileRankDisruption(passFailCalculator baseline, maxDisruptionSeconds int) disruptionJunitCheckFunc {
	return func(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string) (failedJobRunsIDs []string, successfulJobRunIDs []string, status testCaseStatus, message string, err error) {
		return passFailCalculator.CheckPercentileRankDisruption(ctx, jobRunIDToAvailabilityResultForBackend, backend, maxDisruptionSeconds)
	}
}

type disruptionJunitCheckFunc func(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend string) (failedJobRunsIDs []string, successfulJobRunIDs []string, status testCaseStatus, message string, err error)

func disruptionToJUnitTestCase(testCaseName, testSuiteName, jobGCSBucketRoot string, failedJobRunIDs, successfulJobRunIDs []string, status testCaseStatus, message string) (*junit.TestCase, error) {
	junitTestCase := &junit.TestCase{
		Name: testCaseName,
	}

	currDetails := TestCaseDetails{
		Name:          junitTestCase.Name,
		TestSuiteName: testSuiteName,
		Summary:       message,
	}
	for _, jobRunID := range failedJobRunIDs {
		humanURL := jobrunaggregatorapi.GetHumanURLForLocation(path.Join(jobGCSBucketRoot, jobRunID))
		gcsArtifactURL := jobrunaggregatorapi.GetGCSArtifactURLForLocation(path.Join(jobGCSBucketRoot, jobRunID))
		currDetails.Failures = append(currDetails.Failures, TestCaseFailure{
			JobRunID:       jobRunID,
			HumanURL:       humanURL,
			GCSArtifactURL: gcsArtifactURL,
		})
	}
	for _, jobRunID := range successfulJobRunIDs {
		humanURL := jobrunaggregatorapi.GetHumanURLForLocation(path.Join(jobGCSBucketRoot, jobRunID))
		gcsArtifactURL := jobrunaggregatorapi.GetGCSArtifactURLForLocation(path.Join(jobGCSBucketRoot, jobRunID))
		currDetails.Passes = append(currDetails.Passes, TestCasePass{
			JobRunID:       jobRunID,
			HumanURL:       humanURL,
			GCSArtifactURL: gcsArtifactURL,
		})
	}

	currDetails.Summary = message
	detailsBytes, err := yaml.Marshal(currDetails)
	if err != nil {
		return nil, err
	}
	junitTestCase.SystemOut = string(detailsBytes)

	switch status {
	case testCaseFailed:
		junitTestCase.FailureOutput = &junit.FailureOutput{
			Message: message,
			Output:  junitTestCase.SystemOut,
		}
	case testCaseSkipped:
		junitTestCase.SkipMessage = &junit.SkipMessage{
			Message: message,
		}
	}

	return junitTestCase, nil
}

// getDisruptionByJobRunID returns a map of map[jobRunID] to map[backend-name]availabilityResult
func getDisruptionByJobRunID(ctx context.Context, finishedJobsToAggregate []jobrunaggregatorapi.JobRunInfo) (map[string]map[string]jobrunaggregatorlib.AvailabilityResult, error) {
	jobRunIDToBackendNameToAvailabilityResult := map[string]map[string]jobrunaggregatorlib.AvailabilityResult{}

	errs := []error{}
	for i := range finishedJobsToAggregate {
		jobRun := finishedJobsToAggregate[i]
		rawBackendDisruptionData, err := jobRun.GetOpenShiftTestsFilesWithPrefix(ctx, "backend-disruption")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(rawBackendDisruptionData) == 0 {
			fmt.Fprintf(os.Stderr, "Could not fetch backend disruption data for %s\n", jobRun.GetJobRunID())
			continue
		}

		disruptionData := jobrunaggregatorlib.GetServerAvailabilityResultsFromDirectData(rawBackendDisruptionData)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		jobRunIDToBackendNameToAvailabilityResult[jobRun.GetJobRunID()] = disruptionData
	}

	return jobRunIDToBackendNameToAvailabilityResult, utilerrors.NewAggregate(errs)
}

// getDisruptionForBackend returns a map of jobrunid to the availabilityresult for the specified backend
func getDisruptionForBackend(jobRunIDToBackendNameToAvailabilityResult map[string]map[string]jobrunaggregatorlib.AvailabilityResult, backend string) map[string]jobrunaggregatorlib.AvailabilityResult {
	jobRunIDToAvailabilityResultForBackend := map[string]jobrunaggregatorlib.AvailabilityResult{}
	for jobRunID := range jobRunIDToBackendNameToAvailabilityResult {
		backendToAvailabilityForJobRunID := jobRunIDToBackendNameToAvailabilityResult[jobRunID]
		availability, ok := backendToAvailabilityForJobRunID[backend]
		if !ok {
			continue
		}
		jobRunIDToAvailabilityResultForBackend[jobRunID] = availability
	}
	return jobRunIDToAvailabilityResultForBackend
}

func getAllDisruptionBackendNames(jobRunIDToBackendNameToAvailabilityResult map[string]map[string]jobrunaggregatorlib.AvailabilityResult) sets.String {
	ret := sets.String{}
	ret.Insert(jobrunaggregatorlib.RequiredDisruptionTests().List()...)
	for _, curr := range jobRunIDToBackendNameToAvailabilityResult {
		ret.Insert(sets.StringKeySet(curr).List()...)
	}
	return ret
}
