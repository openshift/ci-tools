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
		graceSeconds         int
		thresholdPercentile  int
		historicalDisruption float64
		status               testCaseStatus
		failedCount          int
		successCount         int
		supportsFuzziness    bool
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
			supportsFuzziness:    true,
		},
		{
			// Required Passes for 95th percentile is 6
			// all over on disruption so it fails
			name:                 "Test 95th Percentile all 1 over Fail",
			disruptions:          []int{8, 8, 8, 8, 8, 8, 8, 8, 8, 8},
			thresholdPercentile:  95,
			historicalDisruption: 7,
			status:               testCaseFailed,
			failedCount:          10,
			successCount:         0,
			supportsFuzziness:    true,
		},
		{
			// Required Passes for 95th percentile is 6
			// 5 Natural Passes
			// graceSeconds = 2
			// The Disruption value that == 7 gets flipped to pass
			name:                 "Test 95th Percentile Fuzzy Pass",
			disruptions:          []int{5, 5, 5, 6, 6, 7, 9, 9, 9, 9},
			thresholdPercentile:  95,
			graceSeconds:         2,
			historicalDisruption: 6,
			status:               testCasePassed,
			failedCount:          4,
			successCount:         6,
			supportsFuzziness:    true,
		},
		{
			// Required Passes for 95th percentile is 6
			// 4 Natural Passes
			// graceSeconds = 1
			// The Disruption values that == 7 get flipped to passes
			name:                 "Test 95th Percentile Multi Fuzzy Pass",
			disruptions:          []int{5, 5, 5, 6, 7, 7, 8, 8, 8, 86},
			thresholdPercentile:  95,
			graceSeconds:         1,
			historicalDisruption: 6,
			status:               testCasePassed,
			failedCount:          4,
			successCount:         6,
			supportsFuzziness:    true,
		},
		{
			// Required Passes for 95th percentile is 6
			// 4 Natural Passes
			// graceSeconds = 1
			// The Disruption value that == 7 gets flipped to pass
			// But the 8s and above do not, so we don't have enough passes
			name:                 "Test 95th Percentile Multi Fuzzy Fail",
			disruptions:          []int{5, 5, 5, 6, 7, 8, 8, 9, 9, 86},
			thresholdPercentile:  95,
			graceSeconds:         1,
			historicalDisruption: 6,
			status:               testCaseFailed,
			failedCount:          5,
			successCount:         5,
			supportsFuzziness:    true,
		},
		{
			// Required Passes for 95th percentile is 6
			// 4 Natural Passes
			// graceSeconds = 10
			// The Disruption values that are < 113 get flipped to pass
			// The 113 and above does not, but we have enough passes
			name:                 "Test 95th Percentile Big Disruption Multi Fuzzy Pass",
			disruptions:          []int{99, 105, 101, 102, 101, 108, 108, 112, 113, 186},
			thresholdPercentile:  95,
			graceSeconds:         10,
			historicalDisruption: 102,
			status:               testCasePassed,
			failedCount:          2,
			successCount:         8,
			supportsFuzziness:    true,
		},
		{
			// Required Passes for 95th percentile is 6
			// 4 Natural Passes
			// graceSeconds = 10
			// The Disruption values that are < 113 get flipped to pass
			// The 113 and above does not, so we don't have enough passes
			name:                 "Test 95th Percentile Big Disruption Multi Fuzzy Failed",
			disruptions:          []int{99, 105, 101, 102, 101, 147, 113, 113, 113, 186},
			graceSeconds:         10,
			thresholdPercentile:  95,
			historicalDisruption: 102,
			status:               testCaseFailed,
			failedCount:          5,
			successCount:         5,
			supportsFuzziness:    true,
		},
		{
			// Required Passes for 95th percentile is 6
			// 4 Natural Passes
			// graceSeconds = 5
			// The Disruption values that are < 108 get flipped to pass
			// But the 108s and above do not, so we don't have enough passes
			name:                 "Test 95th Percentile Big Disruption Multi Fuzzy High Multiplier Fail",
			disruptions:          []int{99, 105, 101, 107, 107, 108, 108, 109, 109, 186},
			graceSeconds:         5,
			thresholdPercentile:  95,
			historicalDisruption: 102,
			status:               testCaseFailed,
			failedCount:          5,
			successCount:         5,
			supportsFuzziness:    true,
		},

		// https://prow.ci.openshift.org/view/gs/origin-ci-test/logs/aggregated-azure-ovn-upgrade-4.14-micro-release-openshift-release-analysis-aggregator/1671560866929053696
		//		{Failed: Passed 3 times, failed 6 times.  (P85=1.30s requiredPasses=4
		//		successes=[1671334508823056384=1s 1671334507992584192=0s 1671334507128557568=0s]
		//		failures=[1671334503768920064=5s 1671334504733609984=5s 1671334506289696768=2s
		//		1671334509678694400=2s 1671334505455030272=3s 1671334510496583680=4s])
		//		name: kube-api-new-connections disruption P85 should not be worse
		{
			// Required Passes for 85th percentile is 4
			name:                 "Test 85th Percentile Pass",
			disruptions:          []int{1, 0, 0, 5, 5, 2, 2, 3, 4},
			thresholdPercentile:  85,
			graceSeconds:         1,
			historicalDisruption: 1.30,
			status:               testCasePassed,
			failedCount:          4,
			successCount:         5,
			supportsFuzziness:    true,
		},
		{
			// Required Passes for 85th percentile is 4
			// Compare same case as above but with fuzzy matching disabled to ensure the flag works properly
			name:                 "Test 85th Percentile Fail no fuzzy matching",
			disruptions:          []int{1, 0, 0, 5, 5, 2, 2, 3, 4},
			thresholdPercentile:  85,
			graceSeconds:         0,
			historicalDisruption: 1.30,
			status:               testCaseFailed,
			failedCount:          6,
			successCount:         3,
			supportsFuzziness:    false,
		},

		// we don't want fuzzy matching for the zero-disruption should not be worse tests
		// so the supportsFuzziness flag is false
		// openshift-api-reused-connections zero-disruption should not be worse
		//
		// Failed: Passed 2 times, failed 7 times. (P81=0.00s requiredPasses=3
		// successes=[1671560859370917888=0s 1671560863581999104=0s]
		// failures=[1671560858532057088=4s 1671560863154180096=4s 1671560857697390592=1s 1671560860734066688=5s
		// 1671560861057028096=1s 1671560866081804288=1s 1671560856858529792=2s])
		{
			// Required Passes for 81th percentile is 3
			// Validate failure with fuzzy matching disabled
			name:                 "Test 81th Percentile zero disruption should not be worse, no fuzzy matching",
			disruptions:          []int{0, 0, 4, 4, 1, 5, 1, 1, 2},
			thresholdPercentile:  81,
			historicalDisruption: 0,
			status:               testCaseFailed,
			failedCount:          7,
			successCount:         2,
			supportsFuzziness:    false,
		},
		{
			// Required Passes for 80th percentile is 4
			// 4 Natural Passes
			name:                 "Test 80th Percentile Pass",
			disruptions:          []int{0, 0, 1, 1, 2, 2, 2, 2, 2, 2},
			thresholdPercentile:  80,
			graceSeconds:         0,
			historicalDisruption: 1,
			status:               testCasePassed,
			failedCount:          6,
			successCount:         4,
			supportsFuzziness:    true,
		},
		{
			// Required Passes for 80th percentile is 4
			// 3 Natural Passes
			// graceSeconds = 1
			// The Disruption values that == 2 get flipped to passes
			name:                 "Test 80th Percentile Xtra Fuzzy Pass",
			disruptions:          []int{0, 0, 1, 2, 2, 6, 3, 4, 5, 8},
			thresholdPercentile:  80,
			graceSeconds:         1,
			historicalDisruption: 1,
			status:               testCasePassed,
			failedCount:          5,
			successCount:         5,
			supportsFuzziness:    true,
		},
		{
			// Required Passes for 80th percentile is 4
			// 2 Natural Passes
			// graceSeconds = 0
			// There are no disruption values that get flipped since (2-1)*2 = 2 and our Fuzz Threshold is 1
			// But the 8s and above do not so we don't have enough passes
			name:                 "Test 80th Percentile Fuzzy Fail",
			disruptions:          []int{0, 0, 2, 2, 2, 2, 2, 2, 2, 2},
			thresholdPercentile:  80,
			graceSeconds:         0,
			historicalDisruption: 1,
			status:               testCaseFailed,
			failedCount:          8,
			successCount:         2,
			supportsFuzziness:    true,
		},
		{
			// Required Passes for 80th percentile is 6
			// all disruption, so it fails
			name:                 "Test 80th Percentile all 1 over Fail",
			disruptions:          []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
			thresholdPercentile:  80,
			historicalDisruption: 0,
			status:               testCaseFailed,
			failedCount:          10,
			successCount:         0,
			supportsFuzziness:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			jobRunIDToAvailabilityResultForBackend := createJobRunIDToAvailabilityResultForBackend(test.disruptions)
			historicalDisruptionStatistic := backendDisruptionStats{
				percentileByIndex: make([]float64, 100),
			}
			historicalDisruptionStatistic.percentileByIndex[test.thresholdPercentile] = test.historicalDisruption

			var failureJobRunIDs []string
			var successJobRunIDs []string
			var status testCaseStatus
			var summary string
			if test.supportsFuzziness {
				failureJobRunIDs, successJobRunIDs, status, summary = weeklyAverageFromTenDays.checkPercentileDisruptionWithGrace(
					jobRunIDToAvailabilityResultForBackend, historicalDisruptionStatistic, test.thresholdPercentile, test.graceSeconds)
			} else {
				failureJobRunIDs, successJobRunIDs, status, summary = weeklyAverageFromTenDays.checkPercentileDisruptionWithoutGrace(
					jobRunIDToAvailabilityResultForBackend, historicalDisruptionStatistic, test.thresholdPercentile)
			}

			assert.NotNil(t, summary, "Invalid summary for: %s", test.name)
			t.Logf("summary = %s", summary)
			assert.Equal(t, test.failedCount, len(failureJobRunIDs), "Invalid failed test count for: %s", test.name)
			assert.Equal(t, test.successCount, len(successJobRunIDs), "Invalid success test count for: %s", test.name)
			assert.Equal(t, test.status, status, "Invalid success test count for: %s", test.name)
		})
	}
}
