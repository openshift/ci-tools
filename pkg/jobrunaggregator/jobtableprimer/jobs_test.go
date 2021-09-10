package jobtableprimer

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestForDuplicates(t *testing.T) {
	seen := sets.NewString()
	for _, curr := range jobsToAnalyze {
		if seen.Has(curr.JobName) {
			t.Error(curr.JobName)
		}
		seen.Insert(curr.JobName)
	}
}
