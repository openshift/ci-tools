package config

import (
	"testing"
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
			info:      &Info{Org: "anotherOrg", Repo: "anotherRepo"},
			expected:  false,
		},
		{
			id:        "org is in whitelist but not repo",
			whitelist: WhitelistConfig{Whitelist: map[string][]string{"openshift": {"repo1, repo2"}}},
			info:      &Info{Org: "openshift", Repo: "anotherRepo"},
			expected:  false,
		},
		{
			id:        "org/repo is in whitelist",
			whitelist: WhitelistConfig{Whitelist: map[string][]string{"openshift": {"repo1", "repo2"}}},
			info:      &Info{Org: "openshift", Repo: "repo1"},
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
