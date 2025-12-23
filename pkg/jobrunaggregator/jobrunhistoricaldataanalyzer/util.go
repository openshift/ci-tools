package jobrunhistoricaldataanalyzer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

// minJobRuns is the minimum number of runs for which we'll do a comparison in the pull request message.
// if under this, we don't particularly care if you're up or down, though we will still include you in the
// data file, and let origin sort out what to do with that data.
const minJobRuns = 100

func readHistoricalDataFile(filePath, dataType string) ([]jobrunaggregatorapi.HistoricalData, error) {
	currentData, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file at path (%s): %w", filePath, err)
	}

	switch dataType {
	case "alerts":
		historicalData := []*jobrunaggregatorapi.AlertHistoricalDataRow{}
		if err := json.Unmarshal(currentData, &historicalData); err != nil {
			return nil, err
		}
		return jobrunaggregatorapi.ConvertToHistoricalData(historicalData), nil
	default:
		historicalData := []*jobrunaggregatorapi.DisruptionHistoricalDataRow{}
		if err := json.Unmarshal(currentData, &historicalData); err != nil {
			return nil, err
		}
		return jobrunaggregatorapi.ConvertToHistoricalData(historicalData), nil
	}
}

func convertToMap(data []jobrunaggregatorapi.HistoricalData) map[string]jobrunaggregatorapi.HistoricalData {
	converted := make(map[string]jobrunaggregatorapi.HistoricalData)
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

func addToPRMessage(message string) error {
	file, err := os.OpenFile("pr_message.md", os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	_, err = file.WriteString(message)
	return err
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

func fetchCurrentRelease() (current string, previous string, err error) {
	sippyRelease := struct {
		Releases []string `json:"releases"`
	}{}
	resp, err := http.DefaultClient.Get("https://sippy.dptools.openshift.org/api/releases")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
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

func formatTableOutput(data []parsedJobData, filter bool) string {
	sort.SliceStable(data, func(i, j int) bool {
		return data[i].TimeDiffP95 > data[j].TimeDiffP95
	})
	var buffer bytes.Buffer
	buffer.WriteString("| Name | Release | From | Arch | Network | Platform | Topology | Job Results | P95 | P95 % Increase | P99 | P99 % Increase |\n")
	buffer.WriteString("| ---- | ------- | ---- | ---- | ------- | -------- |--------- | ----------- | --- | -------------- | --- | -------------- |\n")
	for _, d := range data {
		if (d.JobResults < minJobRuns || d.TimeDiffP99 == 0) && filter {
			continue
		}
		buffer.WriteString(
			fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %d | %s| %.2f%% | %s | %.2f%% \n",
				d.GetName(),
				d.GetJobData().Release,
				d.GetJobData().FromRelease,
				d.GetJobData().Architecture,
				d.GetJobData().Network,
				d.GetJobData().Platform,
				d.GetJobData().Topology,
				d.JobResults,
				d.DurationP95,
				d.PercentTimeDiffP95,
				d.DurationP99,
				d.PercentTimeDiffP99,
			),
		)
	}
	return buffer.String()
}

func formatOutput(data []parsedJobData, format string) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	collectedResults := make([]jobrunaggregatorapi.HistoricalData, len(data))
	for i, v := range data {
		collectedResults[i] = v.HistoricalData
	}
	switch format {
	case "json":
		sort.SliceStable(collectedResults, func(i, j int) bool {
			return collectedResults[i].GetKey() < collectedResults[j].GetKey()
		})
		return json.MarshalIndent(collectedResults, "", "  ")
	default:
		return nil, fmt.Errorf("invalid output format (%s)", format)
	}
}

func formatTestOutput(data []jobrunaggregatorapi.TestSummaryByPeriodRow) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	// Sort by release, failure count desc, test name
	sort.SliceStable(data, func(i, j int) bool {
		if data[i].Release != data[j].Release {
			return data[i].Release < data[j].Release
		}
		if data[i].TotalFailureCount != data[j].TotalFailureCount {
			return data[i].TotalFailureCount > data[j].TotalFailureCount
		}
		return data[i].TestName < data[j].TestName
	})
	return json.MarshalIndent(data, "", "  ")
}
