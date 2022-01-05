package jobrunaggregatorapi

import "fmt"

func init() {
	// Some variables exist as documentation of how various tables and views are built.  The linter complains about dead code.
	// This creates fake references.
	rand := fairDiceRoll()
	if rand == -1 {
		fmt.Print(unifiedBackendDisruptionSchema)
		fmt.Print(testRunsUnifiedLast200JobRunsSchema)
		fmt.Print(testRunsUnifiedTestRunsForLast200JobRunsSchema)
		fmt.Print(testRunsUnifiedTestRunsSingleResultForLast200JobRunsSchema)
		fmt.Print(testRunsSummaryLast200RunsSchema)
	}
}

func fairDiceRoll() int {
	return 4 // chosen by fair dice roll.  guaranteed to be random
}
