package config

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestIsWhiteListed(t *testing.T) {
	testCases := []struct {
		id        string
		whitelist WhitelistConfig
		info      *Info
		expected  bool
	}{
		{
			id:        "org/repo is not in whitelist",
			whitelist: WhitelistConfig{Whitelist: map[string][]string{"openshift": {"repo1, repo2"}}},
			info:      &Info{Metadata: api.Metadata{Org: "anotherOrg", Repo: "anotherRepo"}},
			expected:  false,
		},
		{
			id:        "org is in whitelist but not repo",
			whitelist: WhitelistConfig{Whitelist: map[string][]string{"openshift": {"repo1, repo2"}}},
			info:      &Info{Metadata: api.Metadata{Org: "openshift", Repo: "anotherRepo"}},
			expected:  false,
		},
		{
			id:        "org/repo is in whitelist",
			whitelist: WhitelistConfig{Whitelist: map[string][]string{"openshift": {"repo1", "repo2"}}},
			info:      &Info{Metadata: api.Metadata{Org: "openshift", Repo: "repo1"}},
			expected:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			if actual, expected := tc.whitelist.IsWhitelisted(tc.info), tc.expected; actual != expected {
				t.Fatalf("expected %v got %v", expected, actual)
			}
		})
	}
}

func TestReposFromWhitelist(t *testing.T) {
	testCases := []struct {
		name     string
		wc       WhitelistConfig
		expected []string
	}{
		{
			name:     "empty whitelist",
			wc:       WhitelistConfig{},
			expected: []string{},
		},
		{
			name: "single org with repos",
			wc: WhitelistConfig{Whitelist: map[string][]string{
				"openshift": {"repo-a", "repo-b"},
			}},
			expected: []string{"openshift/repo-a", "openshift/repo-b"},
		},
		{
			name: "multiple orgs",
			wc: WhitelistConfig{Whitelist: map[string][]string{
				"openshift": {"repo-a"},
				"other-org": {"repo-x"},
			}},
			expected: []string{"openshift/repo-a", "other-org/repo-x"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := reposFromWhitelist(tc.wc)
			resultList := result.UnsortedList()
			expectedSet := make(map[string]bool)
			for _, e := range tc.expected {
				expectedSet[e] = true
			}
			if len(resultList) != len(tc.expected) {
				t.Fatalf("expected %d repos, got %d: %v", len(tc.expected), len(resultList), resultList)
			}
			for _, r := range resultList {
				if !expectedSet[r] {
					t.Errorf("unexpected repo: %s", r)
				}
			}
		})
	}
}

func TestAddedWhitelistRepos(t *testing.T) {
	testCases := []struct {
		name     string
		oldWL    WhitelistConfig
		newWL    WhitelistConfig
		expected []string
	}{
		{
			name:     "no changes",
			oldWL:    WhitelistConfig{Whitelist: map[string][]string{"org": {"repo"}}},
			newWL:    WhitelistConfig{Whitelist: map[string][]string{"org": {"repo"}}},
			expected: nil,
		},
		{
			name:     "repo added to existing org",
			oldWL:    WhitelistConfig{Whitelist: map[string][]string{"org": {"repo-a"}}},
			newWL:    WhitelistConfig{Whitelist: map[string][]string{"org": {"repo-a", "repo-b"}}},
			expected: []string{"org/repo-b"},
		},
		{
			name:     "new org added",
			oldWL:    WhitelistConfig{Whitelist: map[string][]string{"org-a": {"repo"}}},
			newWL:    WhitelistConfig{Whitelist: map[string][]string{"org-a": {"repo"}, "org-b": {"repo"}}},
			expected: []string{"org-b/repo"},
		},
		{
			name:     "repo removed is not reported",
			oldWL:    WhitelistConfig{Whitelist: map[string][]string{"org": {"repo-a", "repo-b"}}},
			newWL:    WhitelistConfig{Whitelist: map[string][]string{"org": {"repo-a"}}},
			expected: nil,
		},
		{
			name:     "old whitelist empty means all entries are new",
			oldWL:    WhitelistConfig{},
			newWL:    WhitelistConfig{Whitelist: map[string][]string{"org": {"repo-a", "repo-b"}}},
			expected: []string{"org/repo-a", "org/repo-b"},
		},
		{
			name:     "both empty",
			oldWL:    WhitelistConfig{},
			newWL:    WhitelistConfig{},
			expected: nil,
		},
		{
			name:     "multiple repos added across orgs",
			oldWL:    WhitelistConfig{Whitelist: map[string][]string{"org-a": {"existing"}}},
			newWL:    WhitelistConfig{Whitelist: map[string][]string{"org-a": {"existing", "new-a"}, "org-b": {"new-b"}}},
			expected: []string{"org-a/new-a", "org-b/new-b"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := addedWhitelistRepos(tc.oldWL, tc.newWL)
			if diff := cmp.Diff(tc.expected, result); diff != "" {
				t.Fatalf("unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}
