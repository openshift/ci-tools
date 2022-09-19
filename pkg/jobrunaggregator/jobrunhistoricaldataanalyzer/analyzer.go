package jobrunhistoricaldataanalyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type JobRunHistoricalDataAnalyzerOptions struct {
	ciDataClient jobrunaggregatorlib.CIDataClient
	outputFile   string
	newFile      string
	currentFile  string
	dataType     string
	leeway       time.Duration
}

type parsedJobData struct {
	noPrevData                            bool          `json:"-"`
	timeDiff                              time.Duration `json:"-"`
	DurationP95                           time.Duration `json:"-"`
	DurationP99                           time.Duration `json:"-"`
	jobrunaggregatorapi.HistoricalDataRow `json:",inline"`
}

func (o *JobRunHistoricalDataAnalyzerOptions) Run(ctx context.Context) error {
	currentHistoricalData := []jobrunaggregatorapi.HistoricalDataRow{}
	newHistoricalData := []jobrunaggregatorapi.HistoricalDataRow{}
	buffer := bytes.Buffer{}

	oldRawData, err := os.ReadFile(o.currentFile)
	if err != nil {
		return fmt.Errorf("failed to open current file %v", err)
	}
	if err := json.Unmarshal(oldRawData, &currentHistoricalData); err != nil {
		return err
	}
	if len(currentHistoricalData) == 0 {
		return fmt.Errorf("current historical data is empty, can not compare")
	}

	buffer.WriteString(fmt.Sprintf("comparing (%s) with old local file (%s)", o.dataType, o.currentFile))
	if o.currentFile == "" && o.dataType == "alerts" {
		newHistoricalData, err = o.ciDataClient.ListAlertHistoricalData(ctx)
		if err != nil {
			return err
		}
		buffer.WriteString(" with new remote data")
	} else if o.newFile == "" && o.dataType == "disruptions" {
		newHistoricalData, err = o.ciDataClient.ListDisruptionHistoricalData(ctx)
		if err != nil {
			return err
		}
		buffer.WriteString(" with new remote data")
	} else {
		newRawData, err := os.ReadFile(o.newFile)
		if err != nil {
			return fmt.Errorf("failed to open new file %v", err)
		}
		if err := json.Unmarshal(newRawData, &newHistoricalData); err != nil {
			return err
		}
		buffer.WriteString(fmt.Sprintf(" with new local file (%s)", o.newFile))
	}

	if len(newHistoricalData) == 0 {
		return fmt.Errorf("new historical data is empty, can not compare")
	}

	currentRelease, previousRelease, err := fetchCurrentRelease()
	if err != nil {
		return err
	}
	fmt.Printf("current release: (%s) previous release: (%s)\n", currentRelease, previousRelease)

	currentVersionUpdate := currentDataContainsPreviousRelease(previousRelease, currentHistoricalData)
	if currentVersionUpdate {
		fmt.Printf("PR will require review, previous release: (%s) found in old dataset\n", previousRelease)
		if err := requireReviewFile("The current data contains previous release version"); err != nil {
			return err
		}
	}

	newDataMap := convertToMap(newHistoricalData)
	currentDataMap := convertToMap(currentHistoricalData)

	// Merge any missing data for this release or create new output data if currently in release upgrade cycle
	outputMergedData := o.mergeMissingData(newDataMap, currentDataMap, currentRelease, currentVersionUpdate)

	fmt.Println(buffer.String())
	outputData, foundIncrease, missingDataSet := o.compare(newDataMap, outputMergedData, currentRelease)
	if foundIncrease > 0 {
		out, _ := formatOutput(outputData, "markdown", o.dataType, o.leeway.String())
		err = prMessageFile(out)
		if err != nil {
			return err
		}
		log := fmt.Sprintf("%d results had increased in duration beyond specified leeway of %s\n", foundIncrease, o.leeway)
		if err := requireReviewFile(log); err != nil {
			return err
		}
		fmt.Print(log)
	}

	if len(missingDataSet) > 0 {
		message := fmt.Sprintf("\n### Note for dataset (%s): \nThese jobs were in the previous data set but not in the new, they were added back.\n%s", o.dataType, missingDataSet)
		if err := prMessageFile([]byte(message)); err != nil {
			return err
		}
	}

	out, err := formatOutput(outputData, "json", o.dataType, o.leeway.String())
	if err != nil {
		return fmt.Errorf("error merging missing release data %s", err)
	}

	if o.outputFile == "" {
		o.outputFile = fmt.Sprintf("results_%s.json", o.dataType)
	}
	err = os.WriteFile(o.outputFile, out, 0644)
	if err != nil {
		return err
	}

	fmt.Printf("successfully compared with specified leeway of %s\n", o.leeway)
	return nil
}

//
func (o *JobRunHistoricalDataAnalyzerOptions) mergeMissingData(newData, oldData map[string]jobrunaggregatorapi.HistoricalDataRow, release string, newReleaseData bool) map[string]jobrunaggregatorapi.HistoricalDataRow {
	mergedMap := map[string]jobrunaggregatorapi.HistoricalDataRow{}
	// If we are in a new release update transition, theres no need to care about the old data
	// We avoid copying them over to the new merged map
	if !newReleaseData {
		// Copy old data into new mergedMap
		for key, old := range oldData {
			if old.Release == release || old.FromRelease == release {
				mergedMap[key] = old
			}
		}
	}

	for key, new := range newData {
		if _, ok := oldData[key]; !ok {
			// Clean out unknowns
			if new.Release == "unknown" || new.FromRelease == "unknown" {
				continue
			} else if new.Release == release || new.FromRelease == release {
				// Only add desired release info
				mergedMap[key] = new
			}
		}
	}
	return mergedMap
}

func (o *JobRunHistoricalDataAnalyzerOptions) compare(newData, currentData map[string]jobrunaggregatorapi.HistoricalDataRow, release string) ([]parsedJobData, int, string) {
	foundIncrease := 0
	results := []parsedJobData{}
	for key, new := range newData {
		if new.Release != release || new.FromRelease != release {
			continue
		}

		newP99 := getDurationFromString(new.P99)
		newP95 := getDurationFromString(new.P95)
		d := parsedJobData{}

		if old, ok := currentData[key]; ok {
			oldP99 := getDurationFromString(old.P99)

			d.HistoricalDataRow = new
			d.DurationP99 = newP99
			d.DurationP95 = newP95
			d.Type = o.dataType

			timeDiff := newP99 - oldP99
			if newP99 > oldP99 && timeDiff > o.leeway {
				foundIncrease += 1
				d.timeDiff = timeDiff
			}
			d.noPrevData = oldP99 == 0.0
		} else {
			d.HistoricalDataRow = new
			d.DurationP99 = newP99
			d.DurationP95 = newP95
			d.Type = o.dataType
		}
		results = append(results, d)
	}

	// We add back any missing data that for what ever reason is not part of the new data set
	// For no we'll print a warning in the PR to make note of it
	var buffer bytes.Buffer
	for key, old := range currentData {
		if _, ok := newData[key]; !ok {
			if buffer.Len() == 0 {
				buffer.WriteString("| Name | Release | From | Arch | Network | Platform | Topology |\n")
				buffer.WriteString("| ---- | ------- | ---- | ---- | ------- | -------- |--------- |\n")
			}

			oldP99 := getDurationFromString(old.P99)
			oldP95 := getDurationFromString(old.P95)
			d := parsedJobData{}
			d.HistoricalDataRow = old
			d.DurationP99 = oldP99
			d.DurationP95 = oldP95
			d.Type = o.dataType
			results = append(results, d)
			buffer.WriteString(
				fmt.Sprintf("| %s | %s | %s | %s | %s | %s |%s |\n",
					d.Name,
					d.Release,
					d.FromRelease,
					d.Architecture,
					d.Network,
					d.Platform,
					d.Topology),
			)
		}
	}

	return results, foundIncrease, buffer.String()
}

func convertToMap(data []jobrunaggregatorapi.HistoricalDataRow) map[string]jobrunaggregatorapi.HistoricalDataRow {
	converted := map[string]jobrunaggregatorapi.HistoricalDataRow{}
	for _, v := range data {
		converted[v.GetKey()] = v
	}
	return converted
}

func requireReviewFile(message string) error {
	file, err := os.OpenFile("require_review", os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	_, err = file.WriteString(message)
	return err
}

func prMessageFile(message []byte) error {
	file, err := os.OpenFile("pr_message.md", os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	_, err = file.WriteString(string(message))
	return err
}

func formatOutput(data []parsedJobData, format, datatype, leeway string) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	switch format {
	case "json":
		sort.SliceStable(data, func(i, j int) bool {
			return data[i].GetKey() < data[j].GetKey()
		})
		return json.MarshalIndent(data, "", "  ")
	default:
		sort.SliceStable(data, func(i, j int) bool {
			return data[i].timeDiff > data[j].timeDiff
		})
		var buffer bytes.Buffer
		buffer.WriteString(fmt.Sprintf("%s comparisons were above allowed leeway of `%s`\n", datatype, leeway))
		buffer.WriteString("| Name | Release | From | Arch | Network | Platform | Topology | Time Increase |\n")
		buffer.WriteString("| ---- | ------- | ---- | ---- | ------- | -------- |--------- | ------------- |\n")
		for _, d := range data {
			if d.timeDiff == 0 || d.noPrevData {
				continue
			}
			buffer.WriteString(
				fmt.Sprintf("| %s | %s | %s | %s | %s | %s |%s | %s |\n",
					d.Name,
					d.Release,
					d.FromRelease,
					d.Architecture,
					d.Network,
					d.Platform,
					d.Topology,
					d.timeDiff),
			)
		}
		return buffer.Bytes(), nil
	}
}

func getDurationFromString(floatString string) time.Duration {
	if f, err := strconv.ParseFloat(floatString, 64); err == nil {
		t, err := time.ParseDuration(fmt.Sprintf("%.3fs", f))
		if err != nil {
			return time.Duration(0)
		}
		return t
	} else {
		return time.Duration(0)
	}
}

// If current data contains the previous release, we can assume we are in the time frame of new release branching cycle
// This means we need to trigger a manual review of this PR
func currentDataContainsPreviousRelease(prevVersion string, data []jobrunaggregatorapi.HistoricalDataRow) bool {
	for _, d := range data {
		if d.Release == prevVersion {
			return true
		}
	}
	return false
}

func fetchCurrentRelease() (current string, previous string, err error) {
	sippyRelease := struct {
		Releases []string `json:"releases"`
	}{}
	resp, err := http.DefaultClient.Get("https://sippy.dptools.openshift.org/api/releases")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	if err := json.Unmarshal(data, &sippyRelease); err != nil {
		return "", "", err
	}
	sorted := []int{}
	for _, d := range sippyRelease.Releases {
		if !strings.Contains(d, "4.") {
			continue
		}
		minorVersionString := strings.TrimPrefix(d, "4.")
		minorVersion, err := strconv.Atoi(minorVersionString)
		if err != nil {
			continue
		}
		sorted = append(sorted, minorVersion)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i] > sorted[j]
	})
	current = fmt.Sprintf("4.%d", sorted[0])
	previous = fmt.Sprintf("4.%d", sorted[1])
	return
}
