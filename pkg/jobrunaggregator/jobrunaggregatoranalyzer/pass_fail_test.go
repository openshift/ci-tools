package jobrunaggregatoranalyzer

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
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
			// Required Passes for 95th percentile is 7
			// 5 Natural Passes
			// graceSeconds = 2
			// The Disruption value that == 7 gets flipped to pass
			name:                 "Test 95th Percentile Fuzzy Pass",
			disruptions:          []int{5, 5, 5, 6, 6, 7, 7, 9, 9, 9},
			thresholdPercentile:  95,
			graceSeconds:         2,
			historicalDisruption: 6,
			status:               testCasePassed,
			failedCount:          3,
			successCount:         7,
			supportsFuzziness:    true,
		},
		{
			// Required Passes for 95th percentile is 7
			// 4 Natural Passes
			// graceSeconds = 1
			// The Disruption values that == 7 get flipped to passes
			name:                 "Test 95th Percentile Multi Fuzzy Pass",
			disruptions:          []int{5, 5, 5, 6, 7, 7, 7, 8, 8, 86},
			thresholdPercentile:  95,
			graceSeconds:         1,
			historicalDisruption: 6,
			status:               testCasePassed,
			failedCount:          3,
			successCount:         7,
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

		// https://prow.ci.openshift.org/view/gs/test-platform-results/logs/aggregated-azure-ovn-upgrade-4.14-micro-release-openshift-release-analysis-aggregator/1671560866929053696
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
			// Required Passes for 80th percentile is 5
			// 5 Natural Passes
			name:                 "Test 80th Percentile Pass",
			disruptions:          []int{0, 0, 1, 1, 1, 2, 2, 2, 2, 2},
			thresholdPercentile:  80,
			graceSeconds:         0,
			historicalDisruption: 1,
			status:               testCasePassed,
			failedCount:          5,
			successCount:         5,
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

func TestCheckFailedWithPityFactor(t *testing.T) {
	tests := []struct {
		name              string
		passes            int
		failures          int
		skips             int
		workingPercentage int
		expectedStatus    testCaseStatus
		description       string
	}{
		// Tests with 95% working percentage (high reliability test)
		{
			name:              "10 attempts, 95% working: 7 passes, 3 failures - passes strict",
			passes:            7,
			failures:          3,
			skips:             0,
			workingPercentage: 95,
			expectedStatus:    testCasePassed,
			description:       "Strict=7, pity=min(10-2,7)=7. 7 passes meets requirement",
		},
		{
			name:              "10 attempts, 95% working: 6 passes, 4 failures - fails",
			passes:            6,
			failures:          4,
			skips:             0,
			workingPercentage: 95,
			expectedStatus:    testCaseFailed,
			description:       "Strict=7, pity=min(10-2,7)=7. 6 passes fails to meet requirement of 7",
		},
		{
			name:              "12 attempts, 95% working: 9 passes, 3 failures - should pass",
			passes:            9,
			failures:          3,
			skips:             0,
			workingPercentage: 95,
			expectedStatus:    testCasePassed,
			description:       "Strict=9, pity=min(12-2,9)=9. 9 passes meets requirement",
		},
		{
			name:              "12 attempts, 95% working: 8 passes, 4 failures - should fail",
			passes:            8,
			failures:          4,
			skips:             0,
			workingPercentage: 95,
			expectedStatus:    testCaseFailed,
			description:       "Strict=9, pity=min(12-2,9)=9. 8 passes fails to meet requirement",
		},

		// Tests with 80% working percentage (moderate reliability test)
		{
			name:              "10 attempts, 80% working: 5 passes, 5 failures - passes strict",
			passes:            5,
			failures:          5,
			skips:             0,
			workingPercentage: 80,
			expectedStatus:    testCasePassed,
			description:       "Strict=5, pity=min(10-2,5)=5. 5 passes meets requirement",
		},
		{
			name:              "10 attempts, 80% working: 4 passes, 6 failures - should fail",
			passes:            4,
			failures:          6,
			skips:             0,
			workingPercentage: 80,
			expectedStatus:    testCaseFailed,
			description:       "Strict=5, pity=min(10-2,5)=5. 4 passes fails to meet requirement",
		},

		// Tests with 70% working percentage (lower reliability test)
		{
			name:              "10 attempts, 70% working: 3 passes, 7 failures - passes strict",
			passes:            3,
			failures:          7,
			skips:             0,
			workingPercentage: 70,
			expectedStatus:    testCasePassed,
			description:       "Strict=3, pity=min(10-2,3)=3. 3 passes meets requirement",
		},
		{
			name:              "10 attempts, 70% working: 2 passes, 8 failures - should fail",
			passes:            2,
			failures:          8,
			skips:             0,
			workingPercentage: 70,
			expectedStatus:    testCaseFailed,
			description:       "Strict=3, pity=min(10-2,3)=3. 2 passes fails to meet requirement",
		},

		// Tests where pity factor doesn't relax (strict requirement is already at or below pity limit)
		{
			name:              "5 attempts, 95% working: 3 passes, 2 failures - passes strict (no relaxation)",
			passes:            3,
			failures:          2,
			skips:             0,
			workingPercentage: 95,
			expectedStatus:    testCasePassed,
			description:       "Strict=3, pity=min(5-2,3)=3. No relaxation, 3 passes meets requirement",
		},
		{
			name:              "5 attempts, 95% working: 1 pass, 4 failures - should fail",
			passes:            1,
			failures:          4,
			skips:             0,
			workingPercentage: 95,
			expectedStatus:    testCaseFailed,
			description:       "Strict=3, pity=min(5-2,3)=3. 1 pass fails requirement even with pity factor",
		},

		// More tests showing pity factor behavior
		{
			name:              "10 attempts, 90% working: 6 passes, 4 failures - passes strict (no relaxation)",
			passes:            6,
			failures:          4,
			skips:             0,
			workingPercentage: 90,
			expectedStatus:    testCasePassed,
			description:       "Strict=6, pity=min(10-2,6)=6. No relaxation, 6 passes meets requirement",
		},

		// Tests with 100% working percentage (perfect reliability expectation)
		{
			name:              "10 attempts, 100% working: 10 passes, 0 failures - passes naturally",
			passes:            10,
			failures:          0,
			skips:             0,
			workingPercentage: 100,
			expectedStatus:    testCasePassed,
			description:       "Strict=9, pity=min(10-2,9)=8. 10 passes exceeds strict requirement",
		},
		{
			name:              "10 attempts, 100% working: 8 passes, 2 failures - passes with pity factor",
			passes:            8,
			failures:          2,
			skips:             0,
			workingPercentage: 100,
			expectedStatus:    testCasePassed,
			description:       "Strict=9, pity=min(10-2,9)=8. 8 passes meets pity requirement (relaxed from 9)",
		},
		{
			name:              "10 attempts, 100% working: 7 passes, 3 failures - should fail",
			passes:            7,
			failures:          3,
			skips:             0,
			workingPercentage: 100,
			expectedStatus:    testCaseFailed,
			description:       "Strict=9, pity=min(10-2,9)=8. 7 passes fails to meet requirement of 8",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			aggregatedTestRuns := map[TestKey]jobrunaggregatorapi.AggregatedTestRunRow{
				{TestCaseName: "test-case", CombinedTestSuiteName: "test-suite"}: {
					WorkingPercentage: float64(test.workingPercentage),
				},
			}

			baseline := &weeklyAverageFromTenDays{
				aggregatedTestRunsByName: aggregatedTestRuns,
			}
			// Mark query as already done to avoid nil bigQueryClient access
			baseline.queryTestRunsOnce.Do(func() {})

			testCaseDetails := &jobrunaggregatorlib.TestCaseDetails{
				Name:          "test-case",
				TestSuiteName: "test-suite",
				Passes:        make([]jobrunaggregatorlib.TestCasePass, test.passes),
				Failures:      make([]jobrunaggregatorlib.TestCaseFailure, test.failures),
				Skips:         make([]jobrunaggregatorlib.TestCaseSkip, test.skips),
			}

			// Create unique job run IDs for passes and failures
			for i := 0; i < test.passes; i++ {
				testCaseDetails.Passes[i] = jobrunaggregatorlib.TestCasePass{
					JobRunID: fmt.Sprintf("pass-%d", i),
				}
			}
			for i := 0; i < test.failures; i++ {
				testCaseDetails.Failures[i] = jobrunaggregatorlib.TestCaseFailure{
					JobRunID: fmt.Sprintf("fail-%d", i),
				}
			}

			status, message, err := baseline.CheckFailed(nil, "test-job", []string{"suite"}, testCaseDetails)

			assert.NoError(t, err, "Should not error for: %s", test.name)
			assert.NotEmpty(t, message, "Should have a message for: %s", test.name)
			assert.Equal(t, test.expectedStatus, status, "%s: %s", test.description, message)
			t.Logf("Test: %s\nStatus: %s\nMessage: %s", test.name, status, message)
		})
	}
}

func TestInnerCheckPercentileDisruptionWithPityFactor(t *testing.T) {
	weeklyAverage := &weeklyAverageFromTenDays{}

	tests := []struct {
		name                 string
		disruptions          []int
		thresholdPercentile  int // also used as workingPercentage in innerCheckPercentileDisruptionWithGrace
		historicalDisruption float64
		graceSeconds         int
		expectedStatus       testCaseStatus
		expectedMinPasses    int
		description          string
	}{
		// Note: In innerCheckPercentileDisruptionWithGrace, workingPercentage = thresholdPercentile
		// So a P95 test uses 95% as the working percentage for calculating required passes
		{
			name:                 "10 attempts, P95 (95% working): 8 passes, 2 failures - exceeds requirement",
			disruptions:          []int{0, 0, 0, 1, 1, 1, 1, 1, 5, 5},
			thresholdPercentile:  95,
			historicalDisruption: 2.0,
			graceSeconds:         0,
			expectedStatus:       testCasePassed,
			expectedMinPasses:    7, // strict=7, pity=min(10-2,7)=7 (no relaxation)
			description:          "Strict=7, pity=min(10-2,7)=7. 8 passes exceeds requirement",
		},
		{
			name:                 "10 attempts, P95 (95% working): 7 passes, 3 failures - meets requirement",
			disruptions:          []int{0, 0, 0, 1, 1, 1, 1, 5, 5, 5},
			thresholdPercentile:  95,
			historicalDisruption: 2.0,
			graceSeconds:         0,
			expectedStatus:       testCasePassed,
			expectedMinPasses:    7, // strict=7, pity=min(10-2,7)=7 (no relaxation)
			description:          "Strict=7, pity=min(10-2,7)=7. 7 passes meets requirement exactly",
		},
		{
			name:                 "6 attempts, P80 (80% working): 4 passes, 2 failures - exceeds requirement",
			disruptions:          []int{0, 0, 1, 1, 5, 5},
			thresholdPercentile:  80,
			historicalDisruption: 2.0,
			graceSeconds:         0,
			expectedStatus:       testCasePassed,
			expectedMinPasses:    2, // strict=2, pity=min(6-2,2)=2 (no relaxation)
			description:          "Strict=2, pity=min(6-2,2)=2. 4 passes exceeds requirement",
		},
		{
			name:                 "12 attempts, P95 (95% working): 10 passes, 2 failures - exceeds requirement",
			disruptions:          []int{0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 5, 5},
			thresholdPercentile:  95,
			historicalDisruption: 2.0,
			graceSeconds:         0,
			expectedStatus:       testCasePassed,
			expectedMinPasses:    9, // strict=9, pity=min(12-2,9)=9 (no relaxation)
			description:          "Strict=9, pity=min(12-2,9)=9. 10 passes exceeds requirement",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jobRunIDToAvailabilityResultForBackend := createJobRunIDToAvailabilityResultForBackend(test.disruptions)
			historicalDisruptionStatistic := backendDisruptionStats{
				percentileByIndex: make([]float64, 100),
			}
			historicalDisruptionStatistic.percentileByIndex[test.thresholdPercentile] = test.historicalDisruption

			requiredPasses, failureJobRunIDs, successJobRunIDs, status, summary :=
				weeklyAverage.innerCheckPercentileDisruptionWithGrace(
					jobRunIDToAvailabilityResultForBackend,
					test.historicalDisruption,
					test.thresholdPercentile,
					test.graceSeconds,
				)

			t.Logf("Test: %s\nRequired: %d, Passes: %d, Failures: %d\nStatus: %s\nSummary: %s",
				test.name, requiredPasses, len(successJobRunIDs), len(failureJobRunIDs), status, summary)

			assert.Equal(t, test.expectedStatus, status, "%s: %s", test.description, summary)
			assert.Equal(t, test.expectedMinPasses, requiredPasses,
				"Required passes should match expected for: %s", test.name)
			assert.Equal(t, len(test.disruptions), len(successJobRunIDs)+len(failureJobRunIDs),
				"Total attempts should equal successes + failures")
		})
	}
}
