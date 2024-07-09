package rehearse

import (
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/config"
)

func TestDetermineSubsetToRehearse(t *testing.T) {
	allowUnexported := cmp.AllowUnexported(prowconfig.Brancher{}, prowconfig.RegexpChangeMatcher{}, prowconfig.Presubmit{})

	testCases := []struct {
		id                   string
		presubmitsToRehearse []*prowconfig.Presubmit
		rehearsalLimit       int
		expected             []*prowconfig.Presubmit
	}{
		{
			id: "under the limit - no changes expected",
			presubmitsToRehearse: []*prowconfig.Presubmit{
				{JobBase: prowconfig.JobBase{Name: "rehearsal-1", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-2", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-3", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
			},
			rehearsalLimit: 5,
			expected: []*prowconfig.Presubmit{
				{JobBase: prowconfig.JobBase{Name: "rehearsal-1", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-2", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-3", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
			},
		},
		{
			id: "equal with the limit - no changes expected",
			presubmitsToRehearse: []*prowconfig.Presubmit{
				{JobBase: prowconfig.JobBase{Name: "rehearsal-1", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-2", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-3", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
			},
			rehearsalLimit: 3,
			expected: []*prowconfig.Presubmit{
				{JobBase: prowconfig.JobBase{Name: "rehearsal-1", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-2", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-3", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
			},
		},
		{
			id: "over the limit (one source)- changes expected",
			presubmitsToRehearse: []*prowconfig.Presubmit{
				{JobBase: prowconfig.JobBase{Name: "rehearsal-1", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-2", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-3", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-4", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-5", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-6", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-7", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-8", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-9", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-10", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
			},
			rehearsalLimit: 5,
			expected: []*prowconfig.Presubmit{
				{JobBase: prowconfig.JobBase{Name: "rehearsal-1", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-2", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-3", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-4", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-5", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
			},
		},
		{
			id: "over the limit (multiple sources)- changes expected",
			presubmitsToRehearse: []*prowconfig.Presubmit{
				{JobBase: prowconfig.JobBase{Name: "rehearsal-1", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-2", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-3", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-4", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-5", Labels: map[string]string{config.SourceTypeLabel: "changedTemplate"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-6", Labels: map[string]string{config.SourceTypeLabel: "changedTemplate"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-7", Labels: map[string]string{config.SourceTypeLabel: "changedTemplate"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-8", Labels: map[string]string{config.SourceTypeLabel: "changedTemplate"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-9", Labels: map[string]string{config.SourceTypeLabel: "changedRegistryContent"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-10", Labels: map[string]string{config.SourceTypeLabel: "changedRegistryContent"}}},
			},
			rehearsalLimit: 5,
			expected: []*prowconfig.Presubmit{
				{JobBase: prowconfig.JobBase{Name: "rehearsal-1", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-10", Labels: map[string]string{config.SourceTypeLabel: "changedRegistryContent"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-3", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-5", Labels: map[string]string{config.SourceTypeLabel: "changedTemplate"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-9", Labels: map[string]string{config.SourceTypeLabel: "changedRegistryContent"}}},
			},
		},
		{
			id: "summary of the maximum allowed jobs per source is lower that the rehearse limit (rounding inherent in integer division)",
			presubmitsToRehearse: []*prowconfig.Presubmit{
				{JobBase: prowconfig.JobBase{Name: "rehearsal-1", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-2", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-3", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-4", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-5", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-6", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-7", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-8", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-9", Labels: map[string]string{config.SourceTypeLabel: "changedTemplate"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-10", Labels: map[string]string{config.SourceTypeLabel: "changedTemplate"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-11", Labels: map[string]string{config.SourceTypeLabel: "changedTemplate"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-12", Labels: map[string]string{config.SourceTypeLabel: "changedTemplate"}}},
			},
			rehearsalLimit: 10,
			expected: []*prowconfig.Presubmit{
				{JobBase: prowconfig.JobBase{Name: "rehearsal-1", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-10", Labels: map[string]string{config.SourceTypeLabel: "changedTemplate"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-11", Labels: map[string]string{config.SourceTypeLabel: "changedTemplate"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-12", Labels: map[string]string{config.SourceTypeLabel: "changedTemplate"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-2", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-3", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-5", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-6", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-7", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-9", Labels: map[string]string{config.SourceTypeLabel: "changedTemplate"}}},
			},
		},
		{
			id: "all sources are represented even when initial sets are skewed",
			presubmitsToRehearse: []*prowconfig.Presubmit{
				{JobBase: prowconfig.JobBase{Name: "rehearsal-1", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-2", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-3", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-4", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
			},
			rehearsalLimit: 2,
			expected: []*prowconfig.Presubmit{
				{JobBase: prowconfig.JobBase{Name: "rehearsal-1", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				{JobBase: prowconfig.JobBase{Name: "rehearsal-2", Labels: map[string]string{config.SourceTypeLabel: "changedPeriodic"}}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			actual := determineSubsetToRehearse(tc.presubmitsToRehearse, tc.rehearsalLimit)
			sort.Slice(actual, func(a, b int) bool { return actual[a].Name < actual[b].Name })

			if diff := cmp.Diff(actual, tc.expected, allowUnexported); diff != "" {
				t.Errorf("Presubmit list differs from expected: %s", diff)
			}

		})
	}
}

func TestFilterJobsByRequested(t *testing.T) {
	testCases := []struct {
		name                   string
		requested              []string
		presubmits             config.Presubmits
		periodics              config.Periodics
		expectedPresubmits     config.Presubmits
		expectedPeriodics      config.Periodics
		expectedUnaffectedJobs []string
	}{
		{
			name:      "one job requested",
			requested: []string{"presubmit-test"},
			presubmits: config.Presubmits{
				"repo": {
					{JobBase: prowconfig.JobBase{Name: "presubmit-test", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
					{JobBase: prowconfig.JobBase{Name: "some-other-test", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				},
			},
			periodics: config.Periodics{
				"some-periodic": {JobBase: prowconfig.JobBase{Name: "some-periodic", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
			},
			expectedPresubmits: config.Presubmits{
				"repo": {
					{JobBase: prowconfig.JobBase{Name: "presubmit-test", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				},
			},
			expectedPeriodics: config.Periodics{},
		},
		{
			name:      "multiple jobs requested",
			requested: []string{"presubmit-test", "some-periodic"},
			presubmits: config.Presubmits{
				"repo": {
					{JobBase: prowconfig.JobBase{Name: "presubmit-test", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
					{JobBase: prowconfig.JobBase{Name: "some-other-test", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				},
			},
			periodics: config.Periodics{
				"some-periodic": {JobBase: prowconfig.JobBase{Name: "some-periodic", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
			},
			expectedPresubmits: config.Presubmits{
				"repo": {
					{JobBase: prowconfig.JobBase{Name: "presubmit-test", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				},
			},
			expectedPeriodics: config.Periodics{
				"some-periodic": {JobBase: prowconfig.JobBase{Name: "some-periodic", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
			},
		},
		{
			name:      "one unaffected job requested",
			requested: []string{"non-existent-test"},
			presubmits: config.Presubmits{
				"repo": {
					{JobBase: prowconfig.JobBase{Name: "presubmit-test", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
					{JobBase: prowconfig.JobBase{Name: "some-other-test", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				},
			},
			periodics: config.Periodics{
				"some-periodic": {JobBase: prowconfig.JobBase{Name: "some-periodic", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
			},
			expectedPresubmits:     config.Presubmits{},
			expectedPeriodics:      config.Periodics{},
			expectedUnaffectedJobs: []string{"non-existent-test"},
		},
		{
			name:      "one job and one unaffected job requested",
			requested: []string{"presubmit-test", "non-existent-test"},
			presubmits: config.Presubmits{
				"repo": {
					{JobBase: prowconfig.JobBase{Name: "presubmit-test", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
					{JobBase: prowconfig.JobBase{Name: "some-other-test", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				},
			},
			periodics: config.Periodics{
				"some-periodic": {JobBase: prowconfig.JobBase{Name: "some-periodic", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
			},
			expectedPresubmits: config.Presubmits{
				"repo": {
					{JobBase: prowconfig.JobBase{Name: "presubmit-test", Labels: map[string]string{config.SourceTypeLabel: "changedPresubmit"}}},
				},
			},
			expectedPeriodics:      config.Periodics{},
			expectedUnaffectedJobs: []string{"non-existent-test"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			filteredPresubmits, filteredPeriodics, unaffectedJobs := FilterJobsByRequested(tc.requested, tc.presubmits, tc.periodics, logrus.NewEntry(logrus.StandardLogger()))
			if diff := cmp.Diff(tc.expectedPresubmits, filteredPresubmits, ignoreUnexported); diff != "" {
				t.Fatalf("filteredPresubmits don't match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedPeriodics, filteredPeriodics, ignoreUnexported); diff != "" {
				t.Fatalf("filteredPeriodics don't match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedUnaffectedJobs, unaffectedJobs); diff != "" {
				t.Fatalf("unaffectedJobs don't match expected, diff: %s", diff)
			}
		})
	}
}
