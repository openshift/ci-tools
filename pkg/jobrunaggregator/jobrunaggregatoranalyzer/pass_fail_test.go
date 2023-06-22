package jobrunaggregatoranalyzer

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

func createJobRunIDToAvailabilityResultForBackend(disruptions []int) map[string]jobrunaggregatorlib.AvailabilityResult {
	jobRunIDToAvailabilityResultForBackend := make(map[string]jobrunaggregatorlib.AvailabilityResult)
	for i, disruption := range disruptions {
		jobRunIDToAvailabilityResultForBackend[fmt.Sprintf("run_%d", i)] = jobrunaggregatorlib.AvailabilityResult{SecondsUnavailable: disruption}
	}

	return jobRunIDToAvailabilityResultForBackend
}

func TestCheckPercentileDisruption(t *testing.T) {
	weeklyAverageFromTenDays := weeklyAverageFromTenDays{}

	tests := []struct {
		name                 string
		disruptions          []int
		thresholdPercentile  int
		historicalDisruption float64
		status               testCaseStatus
		failedCount          int
		successCount         int
	}{
		{
			name:                 "Test 95th Percentile 0's Pass",
			disruptions:          []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			thresholdPercentile:  95,
			historicalDisruption: 7,
			status:               testCasePassed,
			failedCount:          0,
			successCount:         10,
		},
		{
			name:                 "Test 95th Percentile Fuzzy Pass",
			disruptions:          []int{5, 5, 5, 6, 7, 7, 8, 8, 9, 9},
			thresholdPercentile:  95,
			historicalDisruption: 6,
			status:               testCasePassed,
			failedCount:          4,
			successCount:         6,
		},
		{
			name:                 "Test 95th Percentile Multi Fuzzy Pass",
			disruptions:          []int{5, 5, 5, 6, 7, 7, 7, 8, 8, 86},
			thresholdPercentile:  95,
			historicalDisruption: 6,
			status:               testCasePassed,
			failedCount:          3,
			successCount:         7,
		},
		{
			name:                 "Test 95th Percentile Multi Fuzzy Fail",
			disruptions:          []int{5, 5, 6, 5, 7, 8, 8, 9, 9, 86},
			thresholdPercentile:  95,
			historicalDisruption: 6,
			status:               testCaseFailed,
			failedCount:          5,
			successCount:         5,
		},
		{
			name:                 "Test 80th Percentile Pass",
			disruptions:          []int{0, 0, 1, 1, 2, 2, 2, 2, 2, 2},
			thresholdPercentile:  80,
			historicalDisruption: 1,
			status:               testCasePassed,
			failedCount:          6,
			successCount:         4,
		},
		{
			name:                 "Test 80th Percentile Xtra Fuzzy Pass",
			disruptions:          []int{0, 0, 1, 2, 2, 6, 3, 4, 5, 8},
			thresholdPercentile:  80,
			historicalDisruption: 1,
			status:               testCasePassed,
			failedCount:          5,
			successCount:         5,
		},
		{
			name:                 "Test 80th Percentile Fuzzy Fail",
			disruptions:          []int{0, 0, 2, 2, 2, 2, 2, 2, 2, 2},
			thresholdPercentile:  80,
			historicalDisruption: 1,
			status:               testCaseFailed,
			failedCount:          8,
			successCount:         2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			jobRunIDToAvailabilityResultForBackend := createJobRunIDToAvailabilityResultForBackend(test.disruptions)
			historicalDisruptionStatistic := backendDisruptionStats{
				percentileByIndex: make([]float64, 100),
			}
			historicalDisruptionStatistic.percentileByIndex[test.thresholdPercentile] = test.historicalDisruption

			failureJobRunIDs, successJobRunIDs, status, summary := weeklyAverageFromTenDays.checkPercentileDisruption(jobRunIDToAvailabilityResultForBackend, historicalDisruptionStatistic, test.thresholdPercentile)

			assert.NotNil(t, summary, "Invalid summary for: %s", test.name)
			assert.Equal(t, test.failedCount, len(failureJobRunIDs), "Invalid failed test cont for: %s", test.name)
			assert.Equal(t, test.successCount, len(successJobRunIDs), "Invalid success test cont for: %s", test.name)
			assert.Equal(t, test.status, status, "Invalid success test cont for: %s", test.name)
		})
	}
}
