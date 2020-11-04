package main

import (
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/gofuzz"

	"k8s.io/test-infra/prow/config"
)

// TestDeduplicateTideQueriesDoesntLoseData simply uses deduplicateTideQueries
// on a single fuzzed tidequery, which should never result in any change as
// there is nothing that could be deduplicated. This is mostly to ensure we
// don't forget to change our code when new fields get added to the type.
func TestDeduplicateTideQueriesDoesntLoseData(t *testing.T) {
	for i := 0; i < 100; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			query := config.TideQuery{}
			fuzz.New().Fuzz(&query)
			result, err := deduplicateTideQueries(config.TideQueries{query})
			if err != nil {
				t.Fatalf("error: %v", err)
			}

			if diff := cmp.Diff(result[0], query); diff != "" {
				t.Errorf("result differs from initial query: %s", diff)
			}
		})
	}
}

func TestDeduplicateTideQueries(t *testing.T) {
	testCases := []struct {
		name     string
		in       config.TideQueries
		expected config.TideQueries
	}{
		{
			name: "No overlap",
			in: config.TideQueries{
				{Orgs: []string{"openshift"}, Labels: []string{"merge-me"}},
				{Orgs: []string{"openshift-priv"}, Labels: []string{"merge-me-differently"}},
			},
			expected: config.TideQueries{
				{Orgs: []string{"openshift"}, Labels: []string{"merge-me"}},
				{Orgs: []string{"openshift-priv"}, Labels: []string{"merge-me-differently"}},
			},
		},
		{
			name: "Queries get deduplicated",
			in: config.TideQueries{
				{Orgs: []string{"openshift"}, Labels: []string{"merge-me"}},
				{Orgs: []string{"openshift-priv"}, Labels: []string{"merge-me"}},
			},
			expected: config.TideQueries{{Orgs: []string{"openshift", "openshift-priv"}, Labels: []string{"merge-me"}}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := deduplicateTideQueries(tc.in)
			if err != nil {
				t.Fatalf("failed: %v", err)
			}
			if diff := cmp.Diff(result, tc.expected); diff != "" {
				t.Errorf("Result differs from expected: %v", diff)
			}
		})
	}
}
