package jobrunaggregatoranalyzer

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/util/sets"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

func (o *JobRunAggregatorAnalyzerOptions) CalculateDisruptionTestSuite(ctx context.Context, jobName string, finishedJobsToAggregate []jobrunaggregatorapi.JobRunInfo) (*junit.TestSuite, error) {
	disruptionJunitSuite := &junit.TestSuite{
		Name:      "BackendDisruption",
		TestCases: []*junit.TestCase{},
	}

	jobRunIDToBackendNameToAvailabilityResult, err := getDisruptionByJobRunID(ctx, finishedJobsToAggregate)
	switch {
	case len(jobRunIDToBackendNameToAvailabilityResult) < 3 && err != nil:
		return nil, err
	case len(jobRunIDToBackendNameToAvailabilityResult) < 3 && err == nil:
		return nil, fmt.Errorf("david has to fill this in to fail the aggregation but not the commmand")

	default:
		// ignore the errors if we have at least three results
		fmt.Fprintf(os.Stderr, "Could not fetch backend disruption data for all runs %v", err)
	}

	allBackends := getAllDisruptionBackendNames(jobRunIDToBackendNameToAvailabilityResult)
	for _, backendName := range allBackends.List() {
		jobRunIDToAvailabilityResultForBackend := getDisruptionForBackend(jobRunIDToBackendNameToAvailabilityResult, backendName)
		historicalStats, failed, message, err := o.passFailCalculator.CheckDisruption(ctx, jobRunIDToAvailabilityResultForBackend, backendName)
		if err != nil {
			return nil, err
		}

		junitTestCase := &junit.TestCase{
			Name: fmt.Sprintf("%s should remain available", backendName),
		}
		disruptionJunitSuite.TestCases = append(disruptionJunitSuite.TestCases, junitTestCase)

		currDetails := TestCaseDetails{
			Name:    junitTestCase.Name,
			Summary: message,
		}
		for jobRunID := range jobRunIDToAvailabilityResultForBackend {
			currAvailabilityStat := jobRunIDToAvailabilityResultForBackend[jobRunID]
			humanURL := jobrunaggregatorapi.GetHumanURL(jobName, jobRunID)
			gcsArtifactURL := jobrunaggregatorapi.GetGCSArtifactURL(jobName, jobRunID)
			overMean := float64(currAvailabilityStat.SecondsUnavailable) > historicalStats.mean
			overP95 := float64(currAvailabilityStat.SecondsUnavailable) > historicalStats.p95
			switch {
			case overP95:
				currDetails.Failures = append(currDetails.Failures, TestCaseFailure{
					JobRunID:       jobRunID,
					HumanURL:       humanURL,
					GCSArtifactURL: gcsArtifactURL,
				})

			case overMean: // this will mark it as a flake in higher layers
				currDetails.Failures = append(currDetails.Failures, TestCaseFailure{
					JobRunID:       jobRunID,
					HumanURL:       humanURL,
					GCSArtifactURL: gcsArtifactURL,
				})
				currDetails.Passes = append(currDetails.Passes, TestCasePass{
					JobRunID:       jobRunID,
					HumanURL:       humanURL,
					GCSArtifactURL: gcsArtifactURL,
				})

			default: // this will mark as success only
				currDetails.Passes = append(currDetails.Passes, TestCasePass{
					JobRunID:       jobRunID,
					HumanURL:       humanURL,
					GCSArtifactURL: gcsArtifactURL,
				})
			}
		}

		currDetails.Summary = message
		detailsBytes, err := yaml.Marshal(currDetails)
		if err != nil {
			return nil, err
		}
		junitTestCase.SystemOut = string(detailsBytes)

		if !failed {
			continue
		}
		junitTestCase.FailureOutput = &junit.FailureOutput{
			Message: message,
			Output:  junitTestCase.SystemOut,
		}
		disruptionJunitSuite.NumFailed++

	}

	return disruptionJunitSuite, nil
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
			fmt.Fprintf(os.Stderr, "Could not fetch backend disruption data for %s", jobRun.GetJobRunID())
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
