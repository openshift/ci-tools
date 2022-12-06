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

// releaseTransitionDataMinThresholdRatio defines the minimum threshold ratio we require to
// make the transition from using old release data to using new release data. During a release
// transition (new release cut), it will take time for the new release job run data to reach to
// certain required size to be meaningful to use. During this transition period, we continue
// using data collected from the old release for disruption and alert comparisons.
const releaseTransitionDataMinThresholdRatio = 0.6

type JobRunHistoricalDataAnalyzerOptions struct {
	ciDataClient jobrunaggregatorlib.CIDataClient
	outputFile   string
	newFile      string
	currentFile  string
	dataType     string
	leeway       float64
}

func (o *JobRunHistoricalDataAnalyzerOptions) Run(ctx context.Context) error {
	var newHistoricalData []jobrunaggregatorapi.HistoricalData

	// We check what the current active release version is
	currentRelease, previousRelease, err := fetchCurrentRelease()
	if err != nil {
		return err
	}

	currentHistoricalData, err := readHistoricalDataFile(o.currentFile, o.dataType)
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
		newHistoricalData, err = readHistoricalDataFile(o.newFile, o.dataType)
		if err != nil {
			return err
		}
	}

	if len(newHistoricalData) == 0 {
		return fmt.Errorf("new historical data is empty, can not compare")
	}

	// We convert our query data to maps to make it easier to handle
	newDataMap := convertToMap(newHistoricalData)
	currentDataMap := convertToMap(currentHistoricalData)

	// We check to see if we need to transition to use new release data. We decide based on:
	// 1. The current data set contains the previous release version
	// 2. We have enough data count from the new release
	// If that's the case, we will transition to use data set from the new release.
	// We also write to a `require_review` file to record why a review would be required.
	newReleaseUpdate := currentDataContainsPreviousRelease(previousRelease, currentHistoricalData)
	if newReleaseUpdate {
		newReleaseDataCount := 0
		for _, data := range newDataMap {
			if data.GetJobData().Release == currentRelease {
				newReleaseDataCount++
			}
		}
		if float64(newReleaseDataCount) < float64(len(currentDataMap))*releaseTransitionDataMinThresholdRatio {
			// Not enough data for the new release, continue using old release data
			fmt.Printf("We are in release transition from %s to %s. We continue to use old release data set for the following reason:\n"+
				"- The number of new release data set need to reach at least %d percent of the number of the old release data set.\n"+
				"- Currently we have only %d new release data set and the required number is %d based on old release data set count of %d\n",
				previousRelease, currentRelease, int(releaseTransitionDataMinThresholdRatio*100), newReleaseDataCount,
				int(float64(len(currentDataMap))*releaseTransitionDataMinThresholdRatio), len(currentDataMap))
			newReleaseUpdate = false
			currentRelease = previousRelease
		} else {
			msg := fmt.Sprintf("We are transitioning to use data set from new release %s for the following reason:\n"+
				"- The number of new release data set has reached at least %d percent of the number of the old release data set.\n"+
				"- Currently we have %d new release data set and the required number is %d based on old release data set count of %d\n",
				currentRelease, int(releaseTransitionDataMinThresholdRatio*100), newReleaseDataCount,
				int(float64(len(currentDataMap))*releaseTransitionDataMinThresholdRatio), len(currentDataMap))
			if err := requireReviewFile(msg); err != nil {
				return err
			}
		}
	}

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
// If we're in a normal cycle, we then run through our regular comparisons, for P95 and P99 (we only count jobs for P95).
// We check if the old P95 and/or P99 values are higher, than the new, by calculating the time difference and the percentage difference.
// If a new value is higher AND the percent difference is higher than the leeway desired, we count (for P95 only) that as an increase and record it as part of the results,
// we also record the decreases.
//
// Once we've completed recording the results of the compare, we then do a check to see which jobs were removed and we make a note of those jobs to present.
// The missing jobs are not added back to the final list, the final list is always driven by the new data supplied for comparison gathered from Big Query.
func (o *JobRunHistoricalDataAnalyzerOptions) compareAndUpdate(newData, currentData map[string]jobrunaggregatorapi.HistoricalData, release string, newReleaseEvent bool) compareResults {
	// If we're in a new release event, we don't care about the current data
	if newReleaseEvent {
		currentData = make(map[string]jobrunaggregatorapi.HistoricalData, 0)
	}
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
		d := parsedJobData{}

		// If the current data contains the new data, check and record the time diff
		if old, ok := currentData[key]; ok {
			oldP95 := getDurationFromString(old.GetP95())
			oldP99 := getDurationFromString(old.GetP99())

			d.HistoricalData = new
			d.DurationP99 = newP99
			d.DurationP95 = newP95
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
			added = append(added, key)
		}

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
			d.HistoricalData = old
			missingJobs = append(missingJobs, d)
		}
	}

	return compareResults{
		increaseCount:   increaseCountP99,
		decreaseCount:   decreaseCountP99,
		addedJobs:       added,
		jobs:            results,
		missingJobs:     missingJobs,
		newReleaseEvent: newReleaseEvent,
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
		NewReleaseData bool
		IncreasedCount int
		DecreasedCount int
		AddedJobs      []string
		MissingJobs    []parsedJobData
		Jobs           []parsedJobData
	}{
		DataType:       o.dataType,
		Leeway:         fmt.Sprintf("%.2f%%", o.leeway),
		NewReleaseData: result.newReleaseEvent,
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
