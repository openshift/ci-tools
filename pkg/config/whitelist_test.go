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
			id: "org/repo is not in whitelist",
			whitelist: WhitelistConfig{
				Whitelist: map[string]map[string][]string{
					"openshift": {
						"repo1": {"master", "release-4.x"},
						"repo2": {"master", "release-4.x"},
					},
				},
			},
			info:     &Info{Org: "anotherOrg", Repo: "anotherRepo", Branch: "anotherBranch"},
			expected: false,
		},
		{
			id: "org is in whitelist but not repo",
			whitelist: WhitelistConfig{
				Whitelist: map[string]map[string][]string{
					"openshift": {
						"repo1": {"master", "release-4.x"},
						"repo2": {"master", "release-4.x"},
					},
				},
			},
			info:     &Info{Org: "openshift", Repo: "anotherRepo"},
			expected: false,
		},
		{
			id: "org/repo is in whitelist but not the branch",
			whitelist: WhitelistConfig{
				Whitelist: map[string]map[string][]string{
					"openshift": {
						"repo1": {"master", "release-4.x"},
						"repo2": {"master", "release-4.x"},
					},
				},
			},
			info:     &Info{Org: "openshift", Repo: "repo1", Branch: "anotherBranch"},
			expected: false,
		},
		{
			id: "org/repo/branch is in whitelist",
			whitelist: WhitelistConfig{
				Whitelist: map[string]map[string][]string{
					"openshift": {
						"repo1": {"master", "release-4.x"},
						"repo2": {"master", "release-4.x"},
					},
				},
			},
			info:     &Info{Org: "openshift", Repo: "repo1", Branch: "release-4.x"},
			expected: true,
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
