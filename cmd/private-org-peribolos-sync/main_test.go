package main

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/config/org"
	"sigs.k8s.io/prow/pkg/github/fakegithub"

	"github.com/openshift/ci-tools/pkg/config"
)

func TestGenerateRepositories(t *testing.T) {
	pntrBool := func(b bool) *bool { return &b }
	pntrString := func(s string) *string { return &s }

	orgRepos := map[string]sets.Set[string]{
		"openshift": sets.New[string]([]string{"repo1", "repo2"}...),
		"testshift": sets.New[string]([]string{"repo3", "repo4"}...),
	}

	expectedRepos := map[string]org.Repo{
		"repo1": {
			HasProjects:      pntrBool(false),
			AllowSquashMerge: pntrBool(false),
			AllowMergeCommit: pntrBool(false),
			AllowRebaseMerge: pntrBool(false),
			Description:      pntrString("Test Repo: repo1"),
			Private:          pntrBool(true),
		},
		"repo2": {
			HasProjects:      pntrBool(false),
			AllowSquashMerge: pntrBool(false),
			AllowMergeCommit: pntrBool(false),
			AllowRebaseMerge: pntrBool(false),
			Description:      pntrString("Test Repo: repo2"),
			Private:          pntrBool(true),
		},
		"repo3": {
			HasProjects:      pntrBool(false),
			AllowSquashMerge: pntrBool(false),
			AllowMergeCommit: pntrBool(false),
			AllowRebaseMerge: pntrBool(false),
			Description:      pntrString("Test Repo: repo3"),
			Private:          pntrBool(true),
		},
		"repo4": {
			HasProjects:      pntrBool(false),
			AllowSquashMerge: pntrBool(false),
			AllowMergeCommit: pntrBool(false),
			AllowRebaseMerge: pntrBool(false),
			Description:      pntrString("Test Repo: repo4"),
			Private:          pntrBool(true),
		},
	}

	repos := generateRepositories(&fakegithub.FakeClient{}, orgRepos, logrus.WithField("destination-org", "testOrg"))
	if !reflect.DeepEqual(repos, expectedRepos) {
		t.Fatal(cmp.Diff(repos, expectedRepos))
	}
}

func TestGetReposForPrivateOrg(t *testing.T) {
	testCases := []struct {
		name            string
		whitelistConfig config.WhitelistConfig
		onlyOrg         string
		expectedRepos   map[string]sets.Set[string]
	}{
		{
			name: "whitelist allows repos from other orgs",
			whitelistConfig: config.WhitelistConfig{
				Whitelist: map[string][]string{"org2": {"repo3"}},
			},
			onlyOrg: "org1",
			expectedRepos: map[string]sets.Set[string]{
				"org1": sets.New("repo1"),
				"org2": sets.New("repo3"),
			},
		},
		{
			name:    "no whitelist only includes official image repos from specified org",
			onlyOrg: "org1",
			expectedRepos: map[string]sets.Set[string]{
				"org1": sets.New("repo1"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orgRepos, err := getReposForPrivateOrg("testdata", tc.whitelistConfig, tc.onlyOrg)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tc.expectedRepos, orgRepos); diff != "" {
				t.Fatal(diff)
			}

		})
	}
}
