package prowgen

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestReleaseVersionFromBranch(t *testing.T) {
	testCases := []struct {
		branch     string
		expected   api.ParsedVersion
		expectedOK bool
	}{
		{branch: "release-4.22", expected: api.ParsedVersion{Major: 4, Minor: 22}, expectedOK: true},
		{branch: "release-4.9", expected: api.ParsedVersion{Major: 4, Minor: 9}, expectedOK: true},
		{branch: "main", expectedOK: false},
		{branch: "master", expectedOK: false},
		{branch: "release-4.22-something", expectedOK: false},
		{branch: "something-release-4.22", expectedOK: false},
		{branch: "release-4", expectedOK: false},
	}
	for _, tc := range testCases {
		t.Run(tc.branch, func(t *testing.T) {
			got, ok := releaseVersionFromBranch(tc.branch)
			if ok != tc.expectedOK {
				t.Fatalf("ok = %v, want %v", ok, tc.expectedOK)
			}
			if ok && got != tc.expected {
				t.Errorf("got %+v, want %+v", got, tc.expected)
			}
		})
	}
}

func TestVersionAtLeast(t *testing.T) {
	testCases := []struct {
		v, other api.ParsedVersion
		expected bool
	}{
		{v: api.ParsedVersion{Major: 4, Minor: 22}, other: api.ParsedVersion{Major: 4, Minor: 22}, expected: true},
		{v: api.ParsedVersion{Major: 4, Minor: 23}, other: api.ParsedVersion{Major: 4, Minor: 22}, expected: true},
		{v: api.ParsedVersion{Major: 4, Minor: 21}, other: api.ParsedVersion{Major: 4, Minor: 22}, expected: false},
		{v: api.ParsedVersion{Major: 5, Minor: 0}, other: api.ParsedVersion{Major: 4, Minor: 22}, expected: true},
		{v: api.ParsedVersion{Major: 3, Minor: 99}, other: api.ParsedVersion{Major: 4, Minor: 0}, expected: false},
	}
	for _, tc := range testCases {
		if got := versionAtLeast(tc.v, tc.other); got != tc.expected {
			t.Errorf("versionAtLeast(%+v, %+v) = %v, want %v", tc.v, tc.other, got, tc.expected)
		}
	}
}

func TestManagedRepoIsManaged(t *testing.T) {
	testCases := []struct {
		name     string
		repo     ManagedRepo
		branch   string
		expected bool
	}{
		{
			name:     "zero value: nothing managed",
			branch:   "main",
			expected: false,
		},
		{
			name:     "allBranches: main is managed",
			repo:     ManagedRepo{AllBranches: true},
			branch:   "main",
			expected: true,
		},
		{
			name:     "allBranches: excludeBranches wins",
			repo:     ManagedRepo{AllBranches: true, ExcludeBranches: []string{"main"}},
			branch:   "main",
			expected: false,
		},
		{
			name:     "fromRelease: main is left alone",
			repo:     ManagedRepo{FromRelease: "4.22"},
			branch:   "main",
			expected: false,
		},
		{
			name:     "fromRelease: older release branch is left alone",
			repo:     ManagedRepo{FromRelease: "4.22"},
			branch:   "release-4.21",
			expected: false,
		},
		{
			name:     "fromRelease: release branch at the cutoff is managed",
			repo:     ManagedRepo{FromRelease: "4.22"},
			branch:   "release-4.22",
			expected: true,
		},
		{
			name:     "fromRelease: release branch after the cutoff is managed",
			repo:     ManagedRepo{FromRelease: "4.22"},
			branch:   "release-4.30",
			expected: true,
		},
		{
			name:     "fromRelease: excludeBranches wins for a specific release branch",
			repo:     ManagedRepo{FromRelease: "4.22", ExcludeBranches: []string{"release-4.25"}},
			branch:   "release-4.25",
			expected: false,
		},
		{
			name:     "fromRelease: invalid value is ignored (branch not managed)",
			repo:     ManagedRepo{FromRelease: "not-a-version"},
			branch:   "release-4.22",
			expected: false,
		},
		{
			name:     "branches: explicit non-release branch is managed",
			repo:     ManagedRepo{Branches: []string{"my-feature-branch"}},
			branch:   "my-feature-branch",
			expected: true,
		},
		{
			name:     "branches: unlisted branch is not managed",
			repo:     ManagedRepo{Branches: []string{"my-feature-branch"}},
			branch:   "main",
			expected: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.repo.isManaged(tc.branch); got != tc.expected {
				t.Errorf("isManaged(%q) = %v, want %v", tc.branch, got, tc.expected)
			}
		})
	}
}

func TestManagedReposConfigIsManaged(t *testing.T) {
	var nilConfig *ManagedReposConfig
	if nilConfig.IsManaged("org/repo", "main") {
		t.Errorf("nil config should not manage anything")
	}

	cfg := &ManagedReposConfig{Repos: map[string]ManagedRepo{
		"org/repo": {AllBranches: true},
	}}
	if !cfg.IsManaged("org/repo", "main") {
		t.Errorf("expected org/repo@main to be managed")
	}
	if cfg.IsManaged("org/other", "main") {
		t.Errorf("expected org/other to not be managed (not in config)")
	}
}
