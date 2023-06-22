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
			// Required Passes for 95th percentile is 6
			// no disruption so it passes
			name:                 "Test 95th Percentile 0's Pass",
			disruptions:          []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			thresholdPercentile:  95,
			historicalDisruption: 7,
			status:               testCasePassed,
			failedCount:          0,
			successCount:         10,
		},
		{
			// Required Passes for 95th percentile is 6
			// 5 Natural Passes
			// Multiplier (6 - 5) == 1
			// Fuzz Threshold ((historicalDisruption / 5) + 1) = 2
			// The Disruption value that == 7 gets flipped to pass
			name:                 "Test 95th Percentile Fuzzy Pass",
			disruptions:          []int{5, 5, 5, 6, 6, 7, 9, 9, 9, 9},
			thresholdPercentile:  95,
			historicalDisruption: 6,
			status:               testCasePassed,
			failedCount:          4,
			successCount:         6,
		},
		{
			// Required Passes for 95th percentile is 6
			// 4 Natural Passes
			// Multiplier (6 - 4) == 2
			// Fuzz Threshold ((historicalDisruption / 5) + 1) = 2
			// The Disruption values that == 7 get flipped to passes
			name:                 "Test 95th Percentile Multi Fuzzy Pass",
			disruptions:          []int{5, 5, 5, 6, 7, 7, 8, 8, 8, 86},
			thresholdPercentile:  95,
			historicalDisruption: 6,
			status:               testCasePassed,
			failedCount:          4,
			successCount:         6,
		},
		{
			// Required Passes for 95th percentile is 6
			// 4 Natural Passes
			// Multiplier (6 - 4) == 2
			// Fuzz Threshold ((historicalDisruption / 5) + 1) = 2
			// The Disruption value that == 7 gets flipped to pass
			// But the 8s and above do not, so we don't have enough passes
			name:                 "Test 95th Percentile Multi Fuzzy Fail",
			disruptions:          []int{5, 5, 5, 6, 7, 8, 8, 9, 9, 86},
			thresholdPercentile:  95,
			historicalDisruption: 6,
			status:               testCaseFailed,
			failedCount:          5,
			successCount:         5,
		},
		{
			// Required Passes for 80th percentile is 4
			// 4 Natural Passes
			name:                 "Test 80th Percentile Pass",
			disruptions:          []int{0, 0, 1, 1, 2, 2, 2, 2, 2, 2},
			thresholdPercentile:  80,
			historicalDisruption: 1,
			status:               testCasePassed,
			failedCount:          6,
			successCount:         4,
		},
		{
			// Required Passes for 80th percentile is 4
			// 3 Natural Passes
			// Multiplier (4-3) == 1
			// Fuzz Threshold ((historicalDisruption / 5) + 1) = 1
			// The Disruption values that == 2 get flipped to passes
			name:                 "Test 80th Percentile Xtra Fuzzy Pass",
			disruptions:          []int{0, 0, 1, 2, 2, 6, 3, 4, 5, 8},
			thresholdPercentile:  80,
			historicalDisruption: 1,
			status:               testCasePassed,
			failedCount:          5,
			successCount:         5,
		},
		{
			// Required Passes for 80th percentile is 4
			// 2 Natural Passes
			// Multiplier (4 - 2) == 2
			// Fuzz Threshold ((historicalDisruption / 5) + 1) = 1
			// There are no disruption values that get flipped since (2-1)*2 = 2 and our Fuzz Threshold is 1
			// But the 8s and above do not so we don't have enough passes
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
