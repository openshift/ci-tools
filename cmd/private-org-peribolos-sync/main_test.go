package main

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/config/org"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/github/fakegithub"

	"github.com/openshift/ci-tools/pkg/config"
)

func TestGenerateRepositories(t *testing.T) {
	pntrBool := func(b bool) *bool { return &b }
	privateVisibility := github.RepoVisibilityPrivate
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
					Visibility:       &privateVisibility,
				},
				"repo2": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: repo2"),
					Visibility:       &privateVisibility,
				},
				"testshift-repo3": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: repo3"),
					Visibility:       &privateVisibility,
				},
				"testshift-repo4": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: repo4"),
					Visibility:       &privateVisibility,
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
					Visibility:       &privateVisibility,
				},
				"migtools-must-gather": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: must-gather"),
					Visibility:       &privateVisibility,
				},
				"migtools-crane": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: crane"),
					Visibility:       &privateVisibility,
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
					Visibility:       &privateVisibility,
				},
				"crane": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: crane"),
					Visibility:       &privateVisibility,
				},
				"ocp-build-data": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: ocp-build-data"),
					Visibility:       &privateVisibility,
				},
				"custom-org-custom-repo": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: custom-repo"),
					Visibility:       &privateVisibility,
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
					Visibility:       &privateVisibility,
				},
				"operator-sdk": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: operator-sdk"),
					Visibility:       &privateVisibility,
				},
				"cloud-event-proxy": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: cloud-event-proxy"),
					Visibility:       &privateVisibility,
				},
				"assisted-installer": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: assisted-installer"),
					Visibility:       &privateVisibility,
				},
				"logging-fluentd": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: logging-fluentd"),
					Visibility:       &privateVisibility,
				},
				"other-org-some-repo": {
					HasProjects:      pntrBool(false),
					AllowSquashMerge: pntrBool(false),
					AllowMergeCommit: pntrBool(false),
					AllowRebaseMerge: pntrBool(false),
					Description:      pntrString("Test Repo: some-repo"),
					Visibility:       &privateVisibility,
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
		{
			name: "whitelist includes repos without CI configs",
			whitelistConfig: config.WhitelistConfig{
				Whitelist: map[string][]string{
					"org1": {"repo1", "repo-without-ci-config"},
					"org2": {"repo3", "another-repo-without-ci-config"},
				},
			},
			onlyOrg: "",
			expectedRepos: map[string]sets.Set[string]{
				"org1": sets.New("repo1", "repo-without-ci-config"),
				"org2": sets.New("repo3", "another-repo-without-ci-config"),
			},
		},
		{
			name: "whitelist with onlyOrg filter - whitelisted repos bypass filter",
			whitelistConfig: config.WhitelistConfig{
				Whitelist: map[string][]string{
					"org1": {"repo1", "repo-without-ci-config"},
					"org2": {"repo3", "another-repo-without-ci-config"},
				},
			},
			onlyOrg: "org1",
			expectedRepos: map[string]sets.Set[string]{
				"org1": sets.New("repo1", "repo-without-ci-config"),
				"org2": sets.New("repo3", "another-repo-without-ci-config"),
			},
		},
		{
			name:    "invalid configs are skipped without error",
			onlyOrg: "org1",
			expectedRepos: map[string]sets.Set[string]{
				"org1": sets.New("repo1"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orgRepos := getReposForPrivateOrg("testdata", tc.whitelistConfig, tc.onlyOrg)
			if diff := cmp.Diff(tc.expectedRepos, orgRepos); diff != "" {
				t.Fatal(diff)
			}

		})
	}
}
