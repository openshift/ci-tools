package jobrunhistoricaldataanalyzer

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"os"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type JobRunHistoricalDataAnalyzerOptions struct {
	ciDataClient jobrunaggregatorlib.CIDataClient
	outputFile   string
	newFile      string
	currentFile  string
	dataType     string
	leeway       float64
}

func (o *JobRunHistoricalDataAnalyzerOptions) Run(ctx context.Context) error {
	newHistoricalData := []jobrunaggregatorapi.HistoricalDataRow{}

	// We check what the current active release version is
	currentRelease, previousRelease, err := fetchCurrentRelease()
	if err != nil {
		return err
	}

	currentHistoricalData, err := readHistoricalDataFile(o.currentFile)
	if err != nil {
		return err
	}
	if len(currentHistoricalData) == 0 {
		return fmt.Errorf("current historical data is empty, can not compare")
	}

	switch {
	case o.newFile == "" && o.dataType == "alerts":
		newHistoricalData, err = o.ciDataClient.ListAlertHistoricalData(ctx)
		if err != nil {
			return err
		}
	case o.newFile == "" && o.dataType == "disruptions":
		newHistoricalData, err = o.ciDataClient.ListDisruptionHistoricalData(ctx)
		if err != nil {
			return err
		}
	default:
		newHistoricalData, err = readHistoricalDataFile(o.newFile)
		if err != nil {
			return err
		}
	}

	if len(newHistoricalData) == 0 {
		return fmt.Errorf("new historical data is empty, can not compare")
	}

	// We check to make sure the current data set doesn't contain the previous release version
	// If that's the case, then we are in a new release event (i.e. a new branch has been cut)
	// We then write to a `require_review` file to record why a review would be required.
	newReleaseUpdate := currentDataContainsPreviousRelease(previousRelease, currentHistoricalData)
	if newReleaseUpdate {
		if err := requireReviewFile("The current data contains previous release version"); err != nil {
			return err
		}
	}

	// We convert our query data to maps to make it easier to handle
	newDataMap := convertToMap(newHistoricalData)
	currentDataMap := convertToMap(currentHistoricalData)

	result := o.compareAndUpdate(newDataMap, currentDataMap, currentRelease, newReleaseUpdate)

	err = o.renderResultFiles(result)
	if err != nil {
		return err
	}

	fmt.Printf("successfully compared (%s) with specified leeway of %.2f%%\n", o.dataType, o.leeway)
	return nil
}

// compareAndUpdate This will compare the recently pulled information and compare it to the currently existing data.
// We run our comparison by first checking if we are in a newReleaseEvent, meaning master is now pointing on a new release, then we clean out the current data and generate the results new.
//
// If we re in a normal cycle, we then run through our regular comparisons, the primary one being P99.
// We check if the old P99 value is higher than the new by calculating the time difference and the percentage difference.
// If a new value is higher AND the percent difference is higher than the leeway desired, we count that as an increase and record it as part of the results,
// we also record the decreases.
//
// Once we've completed recording the results of the compare, we then do a check to see which jobs were removed and we make a note of those jobs to present.
// The missing jobs are not added back to the final list, the final list is always driven by the new data supplied for comparison gathered from Big Query.
func (o *JobRunHistoricalDataAnalyzerOptions) compareAndUpdate(newData, currentData map[string]jobrunaggregatorapi.HistoricalDataRow, release string, newReleaseEvent bool) compareResults {
	// If we're in a new release event, we don't care about the current data
	if newReleaseEvent {
		currentData = make(map[string]jobrunaggregatorapi.HistoricalDataRow)
	}
	increaseCount := 0
	decreaseCount := 0
	results := []parsedJobData{}
	added := []string{}
	for key, new := range newData {

		// We only care about the current active release data, we skip all others
		if new.Release != release {
			continue
		}

		newP99 := getDurationFromString(new.P99)
		newP95 := getDurationFromString(new.P95)
		d := parsedJobData{}

		// If the current data contains the new data, check and record the time diff
		if old, ok := currentData[key]; ok {
			oldP99 := getDurationFromString(old.P99)

			d.HistoricalDataRow = new
			d.DurationP99 = newP99
			d.DurationP95 = newP95

			timeDiff := newP99 - oldP99
			percentDiff := 0.0
			if oldP99 != 0 {
				percentDiff = (float64(timeDiff) / float64(oldP99)) * 100
			}
			if newP99 > oldP99 && percentDiff > o.leeway {
				increaseCount += 1
				d.TimeDiff = timeDiff
				d.PercentTimeDiff = percentDiff
				d.PrevP99 = oldP99
			}
			if newP99 < oldP99 {
				decreaseCount += 1
			}
		} else {
			d.HistoricalDataRow = new
			d.DurationP99 = newP99
			d.DurationP95 = newP95
			added = append(added, key)
		}
		d.Type = o.dataType

		results = append(results, d)
	}

	// Some of these comparisons show that sometimes specific runs are removed from the current data set
	// We take note of them here and bubble up that information
	missingJobs := []parsedJobData{}
	for key, old := range currentData {
		// If we're in a new event, we don't bother checking since all the current data should now in the new set
		if newReleaseEvent {
			break
		}
		if _, ok := newData[key]; !ok {
			d := parsedJobData{}
			d.HistoricalDataRow = old
			missingJobs = append(missingJobs, d)
		}
	}

	return compareResults{
		increaseCount: increaseCount,
		decreaseCount: decreaseCount,
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

	buff := bytes.Buffer{}
	if err := prTempl.Execute(&buff, args); err != nil {
		return err
	}

	if err := addToPRMessage(buff.String()); err != nil {
		return err
	}

	out, err := formatOutput(result.jobs, "json")
	if err != nil {
		return fmt.Errorf("error merging missing release data %s", err)
	}

	return os.WriteFile(o.outputFile, out, 0644)
}
