package main

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"

	"k8s.io/test-infra/prow/config/org"
	"k8s.io/test-infra/prow/github/fakegithub"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

func TestGenerateRepositories(t *testing.T) {
	pntrBool := func(b bool) *bool { return &b }
	pntrString := func(s string) *string { return &s }

	orgRepos := map[string]sets.String{
		"openshift": sets.NewString([]string{"repo1", "repo2"}...),
		"testshift": sets.NewString([]string{"repo3", "repo4"}...),
	}

	expectedRepos := map[string]org.Repo{
		"repo1": {
			HasProjects:      pntrBool(false),
			AllowSquashMerge: pntrBool(false),
			AllowMergeCommit: pntrBool(false),
			AllowRebaseMerge: pntrBool(false),
			Description:      pntrString("Test Repo: repo1"),
		},
		"repo2": {
			HasProjects:      pntrBool(false),
			AllowSquashMerge: pntrBool(false),
			AllowMergeCommit: pntrBool(false),
			AllowRebaseMerge: pntrBool(false),
			Description:      pntrString("Test Repo: repo2"),
		},
		"repo3": {
			HasProjects:      pntrBool(false),
			AllowSquashMerge: pntrBool(false),
			AllowMergeCommit: pntrBool(false),
			AllowRebaseMerge: pntrBool(false),
			Description:      pntrString("Test Repo: repo3"),
		},
		"repo4": {
			HasProjects:      pntrBool(false),
			AllowSquashMerge: pntrBool(false),
			AllowMergeCommit: pntrBool(false),
			AllowRebaseMerge: pntrBool(false),
			Description:      pntrString("Test Repo: repo4"),
		},
	}

	repos := generateRepositories(&fakegithub.FakeClient{}, orgRepos, logrus.WithField("destination-org", "testOrg"))
	if !reflect.DeepEqual(repos, expectedRepos) {
		t.Fatal(cmp.Diff(repos, expectedRepos))
	}
}

func TestValidateWhitelist(t *testing.T) {
	testCases := []struct {
		id          string
		whitelist   []string
		errExpected bool
	}{
		{
			id:        "repos to include, no error expected",
			whitelist: []string{"testshift/origin"},
		},
		{
			id:          "no valid repo format, error expected",
			whitelist:   []string{"no-valid-repo"},
			errExpected: true,
		},
		{
			id:        "multiple repos to include, no error expected",
			whitelist: []string{"openshift/repo1", "openshift/repo2", "testshift/repo1", "testshift/repo2"},
		},
		{
			id:          "multiple repos to include, with mixed valid and non valid repos , error expected",
			whitelist:   []string{"openshift/repo1", "no-valid-repo", "testshift/repo1", "no-valid-repo"},
			errExpected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			err := validateWhitelist(tc.whitelist)
			if err != nil && !tc.errExpected {
				t.Fatalf("No error expected: %v", err)
			}

			if err == nil && tc.errExpected {
				t.Fatal("Error expected, got nil")
			}
		})
	}
}

func TestGetWhitelistByOrg(t *testing.T) {
	testCases := []struct {
		id        string
		whitelist []string
		expected  map[string]sets.String
	}{
		{
			id:        "one repos in whitelist",
			whitelist: []string{"testshift/origin"},
			expected:  map[string]sets.String{"testshift": sets.NewString("origin")},
		},
		{
			id:        "multiple repos in whitelist",
			whitelist: []string{"openshift/repo1", "openshift/repo2", "testshift/repo1", "testshift/repo2"},
			expected: map[string]sets.String{
				"openshift": sets.NewString("repo1", "repo2"),
				"testshift": sets.NewString("repo1", "repo2"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			whitelistByOrg := getWhitelistByOrg(tc.whitelist)

			if !reflect.DeepEqual(whitelistByOrg, tc.expected) {
				t.Fatalf("Didn't convert the slice to a map of sets as expected: %v", cmp.Diff(whitelistByOrg, tc.expected))
			}
		})
	}
}

func TestMakeCallback(t *testing.T) {
	type releaseBuildConfigInfo struct {
		releaseBuildConfig *api.ReleaseBuildConfiguration
		configInfo         *config.Info
	}

	testCases := []struct {
		id                      string
		releaseBuildConfigInfos []releaseBuildConfigInfo
		includeRepos            map[string]sets.String
		expectedOrgRepos        map[string]sets.String
	}{
		{
			id: "one official repo, no include repos added",
			releaseBuildConfigInfos: []releaseBuildConfigInfo{
				{
					releaseBuildConfig: &api.ReleaseBuildConfiguration{PromotionConfiguration: &api.PromotionConfiguration{Namespace: "ocp"}},
					configInfo:         &config.Info{Org: "openshift", Repo: "repo1"},
				},
				{
					releaseBuildConfig: &api.ReleaseBuildConfiguration{PromotionConfiguration: &api.PromotionConfiguration{Namespace: "non-ocp"}},
					configInfo:         &config.Info{Org: "openshift", Repo: "repo2"},
				},
			},
			expectedOrgRepos: map[string]sets.String{"openshift": sets.NewString("repo1")},
		},

		{
			id: "official repos, multiple orgs, no include repos added",
			releaseBuildConfigInfos: []releaseBuildConfigInfo{
				{
					releaseBuildConfig: &api.ReleaseBuildConfiguration{PromotionConfiguration: &api.PromotionConfiguration{Namespace: "ocp"}},
					configInfo:         &config.Info{Org: "openshift", Repo: "repo1"},
				},
				{
					releaseBuildConfig: &api.ReleaseBuildConfiguration{PromotionConfiguration: &api.PromotionConfiguration{Namespace: "non-ocp"}},
					configInfo:         &config.Info{Org: "openshift", Repo: "repo2"},
				},
				{
					releaseBuildConfig: &api.ReleaseBuildConfiguration{PromotionConfiguration: &api.PromotionConfiguration{Namespace: "ocp"}},
					configInfo:         &config.Info{Org: "testshift", Repo: "repo1"},
				},
				{
					releaseBuildConfig: &api.ReleaseBuildConfiguration{PromotionConfiguration: &api.PromotionConfiguration{Namespace: "non-ocp"}},
					configInfo:         &config.Info{Org: "testshift", Repo: "repo2"},
				},
			},
			expectedOrgRepos: map[string]sets.String{
				"openshift": sets.NewString("repo1"),
				"testshift": sets.NewString("repo1"),
			},
		},
		{
			id: "one official repo, include repos added",
			releaseBuildConfigInfos: []releaseBuildConfigInfo{
				{
					releaseBuildConfig: &api.ReleaseBuildConfiguration{PromotionConfiguration: &api.PromotionConfiguration{Namespace: "ocp"}},
					configInfo:         &config.Info{Org: "openshift", Repo: "repo1"},
				},
				{
					releaseBuildConfig: &api.ReleaseBuildConfiguration{PromotionConfiguration: &api.PromotionConfiguration{Namespace: "non-ocp"}},
					configInfo:         &config.Info{Org: "openshift", Repo: "repo2"},
				},
			},
			includeRepos:     map[string]sets.String{"openshift": sets.NewString("repo2")},
			expectedOrgRepos: map[string]sets.String{"openshift": sets.NewString("repo1", "repo2")},
		},
		{
			id: "official repos, multiple orgs, include repos added",
			releaseBuildConfigInfos: []releaseBuildConfigInfo{
				{
					releaseBuildConfig: &api.ReleaseBuildConfiguration{PromotionConfiguration: &api.PromotionConfiguration{Namespace: "ocp"}},
					configInfo:         &config.Info{Org: "openshift", Repo: "repo1"},
				},
				{
					releaseBuildConfig: &api.ReleaseBuildConfiguration{PromotionConfiguration: &api.PromotionConfiguration{Namespace: "non-ocp"}},
					configInfo:         &config.Info{Org: "openshift", Repo: "repo2"},
				},
				{
					releaseBuildConfig: &api.ReleaseBuildConfiguration{PromotionConfiguration: &api.PromotionConfiguration{Namespace: "ocp"}},
					configInfo:         &config.Info{Org: "testshift", Repo: "repo1"},
				},
				{
					releaseBuildConfig: &api.ReleaseBuildConfiguration{PromotionConfiguration: &api.PromotionConfiguration{Namespace: "non-ocp"}},
					configInfo:         &config.Info{Org: "testshift", Repo: "repo2"},
				},
				{
					releaseBuildConfig: &api.ReleaseBuildConfiguration{PromotionConfiguration: &api.PromotionConfiguration{Namespace: "non-ocp"}},
					configInfo:         &config.Info{Org: "testshift", Repo: "repo3"},
				},
			},
			includeRepos: map[string]sets.String{
				"openshift": sets.NewString("repo2"),
				"testshift": sets.NewString("repo3"),
			},
			expectedOrgRepos: map[string]sets.String{
				"openshift": sets.NewString("repo1", "repo2"),
				"testshift": sets.NewString("repo1", "repo3"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			orgReposPicked := make(map[string]sets.String)

			callback := makeCallback(tc.includeRepos, orgReposPicked)

			for _, rbci := range tc.releaseBuildConfigInfos {
				callback(rbci.releaseBuildConfig, rbci.configInfo)
			}

			if !reflect.DeepEqual(orgReposPicked, tc.expectedOrgRepos) {
				t.Fatal(cmp.Diff(orgReposPicked, tc.expectedOrgRepos))
			}
		})
	}

}
