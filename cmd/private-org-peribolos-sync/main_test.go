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

	testCases := []struct {
		name          string
		orgRepos      map[string]sets.Set[string]
		onlyOrg       string
		flattenOrgs   []string
		expectedRepos map[string]org.Repo
	}{
		{
			name: "no onlyOrg specified, default orgs are flattened",
			orgRepos: map[string]sets.Set[string]{
				"openshift": sets.New[string]([]string{"repo1", "repo2"}...),
				"testshift": sets.New[string]([]string{"repo3", "repo4"}...),
			},
			onlyOrg:     "",
			flattenOrgs: nil,
			expectedRepos: map[string]org.Repo{
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
				"testshift-repo3": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: repo3"),
					Private:          pntrBool(true),
				},
				"testshift-repo4": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: repo4"),
					Private:          pntrBool(true),
				},
			},
		},
		{
			name: "onlyOrg=openshift, repos from other orgs use prefixed names",
			orgRepos: map[string]sets.Set[string]{
				"openshift": sets.New[string]([]string{"must-gather"}...),
				"migtools":  sets.New[string]([]string{"must-gather", "crane"}...),
			},
			onlyOrg:     "openshift",
			flattenOrgs: nil,
			expectedRepos: map[string]org.Repo{
				"must-gather": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: must-gather"),
					Private:          pntrBool(true),
				},
				"migtools-must-gather": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: must-gather"),
					Private:          pntrBool(true),
				},
				"migtools-crane": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: crane"),
					Private:          pntrBool(true),
				},
			},
		},
		{
			name: "flatten-org specified adds to default flattened orgs",
			orgRepos: map[string]sets.Set[string]{
				"openshift":     sets.New[string]([]string{"installer"}...),
				"migtools":      sets.New[string]([]string{"crane"}...),
				"openshift-eng": sets.New[string]([]string{"ocp-build-data"}...),
				"custom-org":    sets.New[string]([]string{"custom-repo"}...),
			},
			onlyOrg:     "",
			flattenOrgs: []string{"migtools"},
			expectedRepos: map[string]org.Repo{
				"installer": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: installer"),
					Private:          pntrBool(true),
				},
				"crane": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: crane"),
					Private:          pntrBool(true),
				},
				"ocp-build-data": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: ocp-build-data"),
					Private:          pntrBool(true),
				},
				"custom-org-custom-repo": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: custom-repo"),
					Private:          pntrBool(true),
				},
			},
		},
		{
			name: "default flattened orgs keep original names",
			orgRepos: map[string]sets.Set[string]{
				"openshift-eng":      sets.New[string]([]string{"ocp-build-data"}...),
				"operator-framework": sets.New[string]([]string{"operator-sdk"}...),
				"redhat-cne":         sets.New[string]([]string{"cloud-event-proxy"}...),
				"openshift-assisted": sets.New[string]([]string{"assisted-installer"}...),
				"ViaQ":               sets.New[string]([]string{"logging-fluentd"}...),
				"other-org":          sets.New[string]([]string{"some-repo"}...),
			},
			onlyOrg:     "",
			flattenOrgs: nil,
			expectedRepos: map[string]org.Repo{
				"ocp-build-data": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: ocp-build-data"),
					Private:          pntrBool(true),
				},
				"operator-sdk": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: operator-sdk"),
					Private:          pntrBool(true),
				},
				"cloud-event-proxy": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: cloud-event-proxy"),
					Private:          pntrBool(true),
				},
				"assisted-installer": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: assisted-installer"),
					Private:          pntrBool(true),
				},
				"logging-fluentd": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: logging-fluentd"),
					Private:          pntrBool(true),
				},
				"other-org-some-repo": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: some-repo"),
					Private:          pntrBool(true),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repos := generateRepositories(&fakegithub.FakeClient{}, tc.orgRepos, logrus.WithField("destination-org", "testOrg"), tc.onlyOrg, tc.flattenOrgs)
			if !reflect.DeepEqual(repos, tc.expectedRepos) {
				t.Fatal(cmp.Diff(repos, tc.expectedRepos))
			}
		})
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
