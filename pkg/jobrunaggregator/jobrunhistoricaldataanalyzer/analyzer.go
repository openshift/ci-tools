package jobrunhistoricaldataanalyzer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"text/template"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type JobRunHistoricalDataAnalyzerOptions struct {
	ciDataClient    jobrunaggregatorlib.CIDataClient
	outputFile      string
	newFile         string
	currentFile     string
	dataType        string
	leeway          float64
	targetRelease   string
	previousRelease string
}

func (o *JobRunHistoricalDataAnalyzerOptions) Run(ctx context.Context) error {

	// targetRelease will either be what the caller specified on the CLI, or the most recent release.
	// previousRelease will be the one prior to targetRelease.
	var targetRelease, previousRelease string
	var err error
	if o.targetRelease != "" {
		// If we were given a target release, use that:
		targetRelease = o.targetRelease
		// CLI validates that previous release is set if target release is:
		previousRelease = o.previousRelease
	} else {
		// We check what the current active release version is
		targetRelease, previousRelease, err = fetchCurrentRelease()
		if err != nil {
			return err
		}
	}
	fmt.Printf("Using target release: %s, previous release: %s\n", targetRelease, previousRelease)

	// For tests data type, we don't do comparison - just fetch and write directly
	if o.dataType == "tests" {
		return o.runTestsDataType(ctx, targetRelease)
	}

	// For other data types (alerts, disruptions), continue with comparison logic
	var newHistoricalData []jobrunaggregatorapi.HistoricalData

	currentHistoricalData, err := readHistoricalDataFile(o.currentFile, o.dataType)
	if err != nil {
		return err
	}
	if len(currentHistoricalData) == 0 {
		return fmt.Errorf("current historical data is empty, can not compare")
	}

	switch {
	case o.newFile == "" && o.dataType == "alerts":
		newHistoricalData, err = o.getAlertData(ctx)
		if newHistoricalData == nil {
			return fmt.Errorf("failed while attempting to read Alert Historical Data: %w", err)
		}
	case o.newFile == "" && o.dataType == "disruptions":
		newHistoricalData, err = o.ciDataClient.ListDisruptionHistoricalData(ctx)
		if err != nil {
			return err
		}
	default:
		newHistoricalData, err = readHistoricalDataFile(o.newFile, o.dataType)
		if err != nil {
			return err
		}
	}

	if len(newHistoricalData) == 0 {
		return fmt.Errorf("new historical data is empty, can not compare %w", err)
	}

	// We convert our query data to maps to make it easier to handle
	newDataMap := convertToMap(newHistoricalData)
	currentDataMap := convertToMap(currentHistoricalData)

	previousResult := o.compareAndUpdate(newDataMap, currentDataMap, previousRelease)
	currentResult := o.compareAndUpdate(newDataMap, currentDataMap, targetRelease)
	result := mergeResults(previousResult, currentResult)

	err = o.renderResultFiles(result)
	if err != nil {
		return err
	}

	fmt.Printf("successfully compared (%s) with specified leeway of %.2f%%\n", o.dataType, o.leeway)
	return nil
}

func (o *JobRunHistoricalDataAnalyzerOptions) runTestsDataType(ctx context.Context, release string) error {
	// Hardcoded parameters for test summary query
	const (
		suiteName    = "openshift-tests"
		daysBack     = 30
		minTestCount = 100
	)

	fmt.Printf("Fetching test data for release %s, suite %s, last %d days, min %d test runs\n",
		release, suiteName, daysBack, minTestCount)

	// testSummaries, err := o.ciDataClient.ListTestSummaryByPeriod(ctx, suiteName, release, daysBack, minTestCount)
	testSummaries, err := o.ciDataClient.ListGenericTestSummaryByPeriod(ctx, suiteName, release, daysBack, minTestCount)
	if err != nil {
		return fmt.Errorf("failed to list test summary by period: %w", err)
	}

	if len(testSummaries) == 0 {
		return fmt.Errorf("no test data found for suite %s, release %s", suiteName, release)
	}

	// Write the test summaries directly to the output file as JSON
	// out, err := formatTestOutput(testSummaries)
	out, err := formatGenericTestOutput(testSummaries)
	if err != nil {
		return fmt.Errorf("error formatting test output: %w", err)
	}

	if err := os.WriteFile(o.outputFile, out, 0644); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}

	fmt.Printf("Successfully fetched %d test results and wrote to %s\n", len(testSummaries), o.outputFile)
	return nil
}

func (o *JobRunHistoricalDataAnalyzerOptions) getAlertData(ctx context.Context) ([]jobrunaggregatorapi.HistoricalData, error) {
	var allKnownAlerts []*jobrunaggregatorapi.KnownAlertRow
	var newHistoricalData []*jobrunaggregatorapi.AlertHistoricalDataRow

	newHistoricalData, err := o.ciDataClient.ListAlertHistoricalData(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list alert historical data: %w", err)
	}
	allKnownAlerts, err = o.ciDataClient.ListAllKnownAlerts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list all known alerts: %w", err)
	}
	// Create a map to quickly access AlertHistoricalDataRow by AlertName
	alertDataMap := make(map[string]*jobrunaggregatorapi.KnownAlertRow)
	for _, jobData := range allKnownAlerts {
		alertDataMap[jobData.AlertName+jobData.AlertNamespace+jobData.Release] = jobData
	}
	// Update the FirstObserved and LastObserved using the map
	for _, alerts := range newHistoricalData {
		jobData, exists := alertDataMap[alerts.AlertName+alerts.AlertNamespace+alerts.Release]
		if exists {
			alerts.FirstObserved = jobData.FirstObserved
			alerts.LastObserved = jobData.LastObserved
		}
	}
	return jobrunaggregatorapi.ConvertToHistoricalData(newHistoricalData), nil
}

func mergeResults(previousResult, currentResult compareResults) compareResults {
	// Append elements from previousResult and currentResult to the mergedResults
	var mergedResults compareResults
	mergedResults.increaseCount = previousResult.increaseCount + currentResult.increaseCount
	mergedResults.decreaseCount = previousResult.decreaseCount + currentResult.decreaseCount
	mergedResults.addedJobs = append(previousResult.addedJobs, currentResult.addedJobs...)
	mergedResults.jobs = append(previousResult.jobs, currentResult.jobs...)
	mergedResults.missingJobs = append(previousResult.missingJobs, currentResult.missingJobs...)
	return mergedResults
}

// compareAndUpdate This will compare the recently pulled information and compare it to the currently existing data.
// We run our comparison by first checking if we are in a newReleaseEvent, meaning master is now pointing on a new release, then we clean out the current data and generate the results new.
//
// If we're in a normal cycle, we then run through our regular comparisons, for P95 and P99 (we only count jobs for P95).
// We check if the old P95 and/or P99 values are higher, than the new, by calculating the time difference and the percentage difference.
// If a new value is higher AND the percent difference is higher than the leeway desired, we count (for P95 only) that as an increase and record it as part of the results,
// we also record the decreases.
//
// Once we've completed recording the results of the compare, we then do a check to see which jobs were removed and we make a note of those jobs to present.
// The missing jobs are not added back to the final list, the final list is always driven by the new data supplied for comparison gathered from Big Query.
func (o *JobRunHistoricalDataAnalyzerOptions) compareAndUpdate(newData, currentData map[string]jobrunaggregatorapi.HistoricalData, release string) compareResults {
	increaseCountP99 := 0
	decreaseCountP99 := 0
	results := []parsedJobData{}
	added := []string{}
	for key, new := range newData {

		// We only care about the current active release data, we skip all others
		if new.GetJobData().Release != release {
			continue
		}

		newP99 := getDurationFromString(new.GetP99())
		newP95 := getDurationFromString(new.GetP95())
		newP75 := getDurationFromString(new.GetP75())
		newP50 := getDurationFromString(new.GetP50())
		d := parsedJobData{}

		// If the current data contains the new data, check and record the time diff
		if old, ok := currentData[key]; ok {
			oldP95 := getDurationFromString(old.GetP95())
			oldP99 := getDurationFromString(old.GetP99())

			d.HistoricalData = new
			d.DurationP99 = newP99
			d.DurationP95 = newP95
			d.DurationP75 = newP75
			d.DurationP50 = newP50
			d.JobResults = new.GetJobRuns()

			timeDiffP95 := newP95 - oldP95
			timeDiffP99 := newP99 - oldP99
			percentDiffP95 := 0.0
			if oldP95 != 0 {
				percentDiffP95 = (float64(timeDiffP95) / float64(oldP95)) * 100
			}
			percentDiffP99 := 0.0
			if oldP99 != 0 {
				percentDiffP99 = (float64(timeDiffP99) / float64(oldP99)) * 100
			}
			if newP95 > oldP95 && percentDiffP95 > o.leeway {
				d.TimeDiffP95 = timeDiffP95
				d.PercentTimeDiffP95 = percentDiffP95
				d.PrevP95 = oldP95
			}
			if newP99 > oldP99 && percentDiffP99 > o.leeway {
				increaseCountP99 += 1
				d.TimeDiffP99 = timeDiffP99
				d.PercentTimeDiffP99 = percentDiffP99
				d.PrevP99 = oldP99
			}
			if newP99 < oldP99 {
				decreaseCountP99 += 1
			}
		} else {
			d.HistoricalData = new
			d.DurationP99 = newP99
			d.DurationP95 = newP95
			d.DurationP75 = newP75
			d.DurationP50 = newP50
			added = append(added, key)
		}

		results = append(results, d)
	}

	// Some of these comparisons show that sometimes specific runs are removed from the current data set
	// We take note of them here and bubble up that information
	missingJobs := []parsedJobData{}
	for key, old := range currentData {
		if _, ok := newData[key]; !ok {
			d := parsedJobData{}
			d.HistoricalData = old
			missingJobs = append(missingJobs, d)
		}
	}

	return compareResults{
		increaseCount: increaseCountP99,
		decreaseCount: decreaseCountP99,
		addedJobs:     added,
		jobs:          results,
		missingJobs:   missingJobs,
	}
}

func (o *JobRunHistoricalDataAnalyzerOptions) renderResultFiles(result compareResults) error {
	funcs := map[string]any{
		"formatTableOutput": formatTableOutput,
	}
	prTempl, err := template.New("").Funcs(funcs).Parse(prTemplate)
	if err != nil {
		return err
	}

	args := struct {
		DataType       string
		Leeway         string
		IncreasedCount int
		DecreasedCount int
		AddedJobs      []string
		MissingJobs    []parsedJobData
		Jobs           []parsedJobData
	}{
		DataType:       o.dataType,
		Leeway:         fmt.Sprintf("%.2f%%", o.leeway),
		IncreasedCount: result.increaseCount,
		DecreasedCount: result.decreaseCount,
		AddedJobs:      result.addedJobs,
		MissingJobs:    result.missingJobs,
		Jobs:           result.jobs,
	}

	if result.increaseCount > 0 {
		log := fmt.Sprintf("(%s) had (%d) results increased in duration beyond specified leeway of %.2f%%\n", o.dataType, result.increaseCount, o.leeway)
		if err := requireReviewFile(log); err != nil {
			return err
		}
	}

	if result.decreaseCount > 0 {
		log := fmt.Sprintf("(%s) had (%d) results decreased in duration beyond specified leeway of %.2f%%\n", o.dataType, result.decreaseCount, o.leeway)
		if err := requireReviewFile(log); err != nil {
			return err
		}
	}

	buff := bytes.Buffer{}
	if err := prTempl.Execute(&buff, args); err != nil {
		return err
	}

	if err := addToPRMessage(buff.String()); err != nil {
		return err
	}

	out, err := formatOutput(result.jobs, "json")
	if err != nil {
		return fmt.Errorf("error merging missing release data %w", err)
	}

	return os.WriteFile(o.outputFile, out, 0644)
}
