package jobrunaggregatoranalyzer

import (
	"fmt"
	"strings"
	"testing"

	fet "github.com/glycerine/golang-fisher-exact"
)

func TestRequiredPassCount(t *testing.T) {
	for numberOfPayloadAttempts := range requiredPassesByPassPercentageByNumberOfAttempts {
		requiredPassCounts := []int{}
		previousRequiredPassCount := numberOfPayloadAttempts

		// count backwards from  100% so we have constantly shrinking counts
		for corpusPassPercentage := 100; corpusPassPercentage >= 0; corpusPassPercentage-- {
			requiredPassCount := 0

			// count backwards from the previous successful count until we get under the 0.5 mark
			for possiblePassCount := previousRequiredPassCount; possiblePassCount >= 0; possiblePassCount-- {
				corpusPassCount := corpusPassPercentage * 7 // assume 400 runs in a week
				corpusFailCount := 700 - corpusPassCount    // assume 400 runs in a week
				payloadFailCount := numberOfPayloadAttempts - possiblePassCount

				_, leftp, _, _ := fet.FisherExactTest(corpusPassCount, possiblePassCount, corpusFailCount, payloadFailCount)
				if leftp < .05 {
					// the count isn't low enough yet
					t.Logf("failed for numberOfPayloadAttempts=%v, corpusPassPercentage=%v, requiredPassCount=%v: actual probability of payload being equal to or better than corpus pass rate is %f",
						numberOfPayloadAttempts, corpusPassPercentage, possiblePassCount, leftp)
				} else {
					requiredPassCount = possiblePassCount
					break
				}

			}
			// prepend the percentage because we're counting the percentages backwards
			requiredPassCounts = append([]int{requiredPassCount}, requiredPassCounts...)
			previousRequiredPassCount = requiredPassCount
		}

		t.Logf("values for %v attempts", numberOfPayloadAttempts)
		for i := 0; i < 10; i++ {
			counts := []string{}
			startingIndex := i * 10
			for j := startingIndex; j < startingIndex+10; j++ {
				counts = append(counts, fmt.Sprintf("%d", requiredPassCounts[j]))
			}
			t.Logf("%v // %v-%v", strings.Join(counts, ", "), startingIndex, startingIndex+9)
		}
		t.Log("")
	}

	for numberOfPayloadAttempts := range requiredPassesByPassPercentageByNumberOfAttempts {
		requiredPassesForCount := requiredPassesByPassPercentageByNumberOfAttempts[numberOfPayloadAttempts]
		for corpusPassPercentage, requiredPassCount := range requiredPassesForCount {
			if requiredPassCount == 0 {
				continue
			}
			corpusPassCount := corpusPassPercentage * 4 // assume 400 runs in a week
			corpusFailCount := 400 - corpusPassCount    // assume 400 runs in a week
			payloadPassCount := requiredPassCount
			payloadFailCount := numberOfPayloadAttempts - requiredPassCount

			_, leftp, _, _ := fet.FisherExactTest(corpusPassCount, payloadPassCount, corpusFailCount, payloadFailCount)
			if leftp < .05 {
				t.Errorf("failed for numberOfPayloadAttempts=%v, corpusPassPercentage=%v, requiredPassCount=%v: actual probability of payload being equal to or better than corpus pass rate is %f",
					numberOfPayloadAttempts, corpusPassPercentage, requiredPassCount, leftp)
			}

			if requiredPassCount == numberOfPayloadAttempts {
				continue
			}
			tooLenientPayloadPassCount := requiredPassCount + 1
			tooLenientPayloadFailCount := numberOfPayloadAttempts - tooLenientPayloadPassCount
			_, leftp, _, _ = fet.FisherExactTest(corpusPassCount, tooLenientPayloadPassCount, corpusFailCount, tooLenientPayloadFailCount)
			// because leftp can be NaN, we need the !
			if !(leftp < .05) {
				t.Errorf("Can be more strict for numberOfPayloadAttempts=%v, corpusPassPercentage=%v, requiredPassCount=%v: actual probability of payload being equal to or better than corpus pass rate is %f",
					numberOfPayloadAttempts, corpusPassPercentage, tooLenientPayloadPassCount, leftp)
			}
		}
	}
}
