package main

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/plugins"
	utilpointer "k8s.io/utils/pointer"
)

var orgRepos = orgReposWithOfficialImages{
	"openshift": sets.NewString("testRepo1", "testRepo2"),
	"testshift": sets.NewString("testRepo3", "testRepo4"),
}

func pBool(b bool) *bool {
	return &b
}

func TestInjectPrivateBranchProtection(t *testing.T) {
	testCases := []struct {
		id               string
		branchProtection prowconfig.BranchProtection
		expected         prowconfig.BranchProtection
	}{
		{
			id: "no changes expected",
			branchProtection: prowconfig.BranchProtection{
				Orgs: map[string]prowconfig.Org{
					"openshift": {Repos: map[string]prowconfig.Repo{
						"anotherRepo1": {Branches: map[string]prowconfig.Branch{
							"branch1": {Policy: prowconfig.Policy{Protect: pBool(false)}}}}}},
				},
			},
			expected: prowconfig.BranchProtection{
				Orgs: map[string]prowconfig.Org{
					"openshift": {Repos: map[string]prowconfig.Repo{
						"anotherRepo1": {Branches: map[string]prowconfig.Branch{
							"branch1": {Policy: prowconfig.Policy{Protect: pBool(false)}}}}}},
				},
			},
		},
		{
			id: "changes expected",
			branchProtection: prowconfig.BranchProtection{
				Orgs: map[string]prowconfig.Org{
					"openshift": {Repos: map[string]prowconfig.Repo{
						"testRepo1": {Branches: map[string]prowconfig.Branch{
							"branch1": {Policy: prowconfig.Policy{Protect: pBool(false)}}}}}},
				},
			},
			expected: prowconfig.BranchProtection{
				Orgs: map[string]prowconfig.Org{
					"openshift": {Repos: map[string]prowconfig.Repo{
						"testRepo1": {Branches: map[string]prowconfig.Branch{
							"branch1": {Policy: prowconfig.Policy{Protect: pBool(false)}}}}}},
					"openshift-priv": {Repos: map[string]prowconfig.Repo{
						"testRepo1": {Branches: map[string]prowconfig.Branch{
							"branch1": {Policy: prowconfig.Policy{Protect: pBool(false)}}}}},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			injectPrivateBranchProtection(tc.branchProtection, orgRepos)
			if !reflect.DeepEqual(tc.branchProtection, tc.expected) {
				t.Fatal(cmp.Diff(tc.branchProtection, tc.expected))
			}
		})
	}
}

func TestInjectPrivateTideOrgContextPolicy(t *testing.T) {
	testCases := []struct {
		id                   string
		contextPolicyOptions prowconfig.TideContextPolicyOptions
		expected             prowconfig.TideContextPolicyOptions
	}{
		{
			id: "no changes expected",
			contextPolicyOptions: prowconfig.TideContextPolicyOptions{
				Orgs: map[string]prowconfig.TideOrgContextPolicy{
					"openshift": {Repos: map[string]prowconfig.TideRepoContextPolicy{
						"anotherRepo1": {TideContextPolicy: prowconfig.TideContextPolicy{
							SkipUnknownContexts: pBool(true)}}}},
				},
			},
			expected: prowconfig.TideContextPolicyOptions{
				Orgs: map[string]prowconfig.TideOrgContextPolicy{
					"openshift": {Repos: map[string]prowconfig.TideRepoContextPolicy{
						"anotherRepo1": {TideContextPolicy: prowconfig.TideContextPolicy{
							SkipUnknownContexts: pBool(true)}}}},
				},
			},
		},
		{
			id: "changes expected",
			contextPolicyOptions: prowconfig.TideContextPolicyOptions{
				Orgs: map[string]prowconfig.TideOrgContextPolicy{
					"openshift": {Repos: map[string]prowconfig.TideRepoContextPolicy{
						"testRepo1": {TideContextPolicy: prowconfig.TideContextPolicy{
							SkipUnknownContexts: pBool(true)}}}},
				},
			},
			expected: prowconfig.TideContextPolicyOptions{
				Orgs: map[string]prowconfig.TideOrgContextPolicy{
					"openshift": {Repos: map[string]prowconfig.TideRepoContextPolicy{
						"testRepo1": {TideContextPolicy: prowconfig.TideContextPolicy{
							SkipUnknownContexts: pBool(true)}}}},
					"openshift-priv": {Repos: map[string]prowconfig.TideRepoContextPolicy{
						"testRepo1": {TideContextPolicy: prowconfig.TideContextPolicy{
							SkipUnknownContexts: pBool(true)}}}},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			injectPrivateTideOrgContextPolicy(tc.contextPolicyOptions, orgRepos)
			if !reflect.DeepEqual(tc.contextPolicyOptions, tc.expected) {
				t.Fatal(cmp.Diff(tc.contextPolicyOptions, tc.expected))
			}
		})
	}
}

func TestInjectPrivateReposTideQueries(t *testing.T) {
	testCases := []struct {
		id          string
		tideQueries []prowconfig.TideQuery
		expected    []prowconfig.TideQuery
	}{
		{
			id: "no changes expected",
			tideQueries: []prowconfig.TideQuery{
				{
					IncludedBranches: []string{"release-4.0", "release-4.1"},
					Labels:           []string{"lgtm", "approved"},
					MissingLabels:    []string{"needs-rebase", "do-not-merge/work-in-progress"},
					Repos:            []string{"openshift/anotherRepo1"},
				},
				{
					ExcludedBranches: []string{"release-4.2", "release-4.3"},
					Labels:           []string{"lgtm", "approved"},
					MissingLabels:    []string{"needs-rebase", "do-not-merge/work-in-progress"},
					Repos:            []string{"openshift/anotherRepo2"},
				},
			},
			expected: []prowconfig.TideQuery{
				{
					IncludedBranches: []string{"release-4.0", "release-4.1"},
					Labels:           []string{"lgtm", "approved"},
					MissingLabels:    []string{"needs-rebase", "do-not-merge/work-in-progress"},
					Repos:            []string{"openshift/anotherRepo1"},
				},
				{
					ExcludedBranches: []string{"release-4.2", "release-4.3"},
					Labels:           []string{"lgtm", "approved"},
					MissingLabels:    []string{"needs-rebase", "do-not-merge/work-in-progress"},
					Repos:            []string{"openshift/anotherRepo2"},
				},
			},
		},
		{
			id: "changes expected",
			tideQueries: []prowconfig.TideQuery{
				{
					IncludedBranches: []string{"release-4.0", "release-4.1"},
					Labels:           []string{"lgtm", "approved"},
					MissingLabels:    []string{"needs-rebase", "do-not-merge/work-in-progress"},
					Repos:            []string{"openshift/testRepo1", "testshift/testRepo3"},
				},
				{
					ExcludedBranches: []string{"release-4.2", "release-4.3"},
					Labels:           []string{"lgtm", "approved"},
					MissingLabels:    []string{"needs-rebase", "do-not-merge/work-in-progress"},
					Repos:            []string{"openshift/anotherRepo2"},
				},
			},
			expected: []prowconfig.TideQuery{
				{
					IncludedBranches: []string{"release-4.0", "release-4.1"},
					Labels:           []string{"lgtm", "approved"},
					MissingLabels:    []string{"needs-rebase", "do-not-merge/work-in-progress"},
					Repos: []string{
						"openshift-priv/testRepo1", "openshift-priv/testRepo3",
						"openshift/testRepo1", "testshift/testRepo3",
					},
				},
				{
					ExcludedBranches: []string{"release-4.2", "release-4.3"},
					Labels:           []string{"lgtm", "approved"},
					MissingLabels:    []string{"needs-rebase", "do-not-merge/work-in-progress"},
					Repos:            []string{"openshift/anotherRepo2"},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			setPrivateReposTideQueries(tc.tideQueries, orgRepos)
			if !reflect.DeepEqual(tc.tideQueries, tc.expected) {
				t.Fatal(cmp.Diff(tc.tideQueries, tc.expected))
			}
		})
	}
}

func TestInjectPrivateMergeType(t *testing.T) {
	testCases := []struct {
		id             string
		tideMergeTypes map[string]github.PullRequestMergeType
		expected       map[string]github.PullRequestMergeType
	}{
		{
			id: "no changes expected",
			tideMergeTypes: map[string]github.PullRequestMergeType{
				"anotherOrg/Repo": github.MergeMerge,
				"openshift/Repo2": github.MergeRebase,
				"testshift/Repo3": github.MergeSquash,
			},
			expected: map[string]github.PullRequestMergeType{
				"anotherOrg/Repo": github.MergeMerge,
				"openshift/Repo2": github.MergeRebase,
				"testshift/Repo3": github.MergeSquash,
			},
		},
		{
			id: "changes expected",
			tideMergeTypes: map[string]github.PullRequestMergeType{
				"anotherOrg/Repo":       github.MergeMerge,
				"openshift/testRepo1":   github.MergeSquash,
				"openshift/anotherRepo": github.MergeSquash,
				"testshift/anotherRepo": github.MergeMerge,
				"testshift/testRepo3":   github.MergeMerge,
			},
			expected: map[string]github.PullRequestMergeType{
				"anotherOrg/Repo":          github.MergeMerge,
				"openshift/testRepo1":      github.MergeSquash,
				"openshift/anotherRepo":    github.MergeSquash,
				"testshift/anotherRepo":    github.MergeMerge,
				"testshift/testRepo3":      github.MergeMerge,
				"openshift-priv/testRepo1": github.MergeSquash,
				"openshift-priv/testRepo3": github.MergeMerge,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			injectPrivateMergeType(tc.tideMergeTypes, orgRepos)
			if !reflect.DeepEqual(tc.tideMergeTypes, tc.expected) {
				t.Fatalf(cmp.Diff(tc.tideMergeTypes, tc.expected))
			}
		})
	}
}

func TestInjectPrivatePRStatusBaseURLs(t *testing.T) {
	testCases := []struct {
		id               string
		prStatusBaseURLs map[string]string
		expected         map[string]string
	}{
		{
			id: "no changes expected",
			prStatusBaseURLs: map[string]string{
				"openshift":              "https://test.com",
				"testshift":              "https://test.com",
				"openshift/anotherRepo1": "https://test.com",
			},
			expected: map[string]string{
				"openshift":              "https://test.com",
				"testshift":              "https://test.com",
				"openshift/anotherRepo1": "https://test.com",
			},
		},
		{
			id: "changes expected",
			prStatusBaseURLs: map[string]string{
				"openshift":              "https://test.com",
				"openshift/anotherRepo1": "https://test.com",
				"openshift/testRepo1":    "https://test.com",
				"testshift/testRepo3":    "https://test.com",
			},
			expected: map[string]string{
				"openshift":                "https://test.com",
				"openshift-priv/testRepo1": "https://test.com",
				"openshift-priv/testRepo3": "https://test.com",
				"openshift/anotherRepo1":   "https://test.com",
				"openshift/testRepo1":      "https://test.com",
				"testshift/testRepo3":      "https://test.com",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			injectPrivatePRStatusBaseURLs(tc.prStatusBaseURLs, orgRepos)
			if !reflect.DeepEqual(tc.prStatusBaseURLs, tc.expected) {
				t.Fatal(cmp.Diff(tc.prStatusBaseURLs, tc.expected))
			}
		})
	}
}

func TestInjectPrivatePlankDefaultDecorationConfigs(t *testing.T) {
	testCases := []struct {
		id                       string
		defaultDecorationConfigs map[string]*prowapi.DecorationConfig
		expected                 map[string]*prowapi.DecorationConfig
	}{
		{
			id: "no changes expected",
			defaultDecorationConfigs: map[string]*prowapi.DecorationConfig{
				"openshift":              {GCSCredentialsSecret: utilpointer.StringPtr("gcs_secret"), SkipCloning: pBool(true)},
				"openshift/anotherRepo1": {GCSCredentialsSecret: utilpointer.StringPtr("gcs_secret2"), SkipCloning: pBool(false)},
			},
			expected: map[string]*prowapi.DecorationConfig{
				"openshift":              {GCSCredentialsSecret: utilpointer.StringPtr("gcs_secret"), SkipCloning: pBool(true)},
				"openshift/anotherRepo1": {GCSCredentialsSecret: utilpointer.StringPtr("gcs_secret2"), SkipCloning: pBool(false)},
			},
		},
		{
			id: "changes expected",
			defaultDecorationConfigs: map[string]*prowapi.DecorationConfig{
				"openshift":           {GCSCredentialsSecret: utilpointer.StringPtr("gcs_secret"), SkipCloning: pBool(true)},
				"openshift/testRepo1": {GCSCredentialsSecret: utilpointer.StringPtr("gcs_secret2"), SkipCloning: pBool(false)},
			},
			expected: map[string]*prowapi.DecorationConfig{
				"openshift":                {GCSCredentialsSecret: utilpointer.StringPtr("gcs_secret"), SkipCloning: pBool(true)},
				"openshift/testRepo1":      {GCSCredentialsSecret: utilpointer.StringPtr("gcs_secret2"), SkipCloning: pBool(false)},
				"openshift-priv/testRepo1": {GCSCredentialsSecret: utilpointer.StringPtr("gcs_secret2"), SkipCloning: pBool(false)},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			injectPrivatePlankDefaultDecorationConfigs(tc.defaultDecorationConfigs, orgRepos)
			if !reflect.DeepEqual(tc.defaultDecorationConfigs, tc.expected) {
				t.Fatal(cmp.Diff(tc.defaultDecorationConfigs, tc.expected))
			}
		})
	}
}

func TestInjectPrivateJobURLPrefixConfig(t *testing.T) {
	testCases := []struct {
		id                 string
		jobURLPrefixConfig map[string]string
		expected           map[string]string
	}{
		{
			id: "no changes expected",
			jobURLPrefixConfig: map[string]string{
				"openshift":              "https://test.com",
				"openshift/anotherRepo1": "https://test.com",
			},
			expected: map[string]string{
				"openshift":              "https://test.com",
				"openshift/anotherRepo1": "https://test.com",
			},
		},
		{
			id: "changes expected",
			jobURLPrefixConfig: map[string]string{
				"openshift":           "https://test.com",
				"openshift/testRepo1": "https://test.com",
				"testshift/testRepo3": "https://test.com",
			},
			expected: map[string]string{
				"openshift":                "https://test.com",
				"openshift/testRepo1":      "https://test.com",
				"testshift/testRepo3":      "https://test.com",
				"openshift-priv/testRepo1": "https://test.com",
				"openshift-priv/testRepo3": "https://test.com",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			injectPrivateJobURLPrefixConfig(tc.jobURLPrefixConfig, orgRepos)
			if !reflect.DeepEqual(tc.jobURLPrefixConfig, tc.expected) {
				t.Fatal(cmp.Diff(tc.jobURLPrefixConfig, tc.expected))
			}
		})
	}
}

func TestInjectPrivateApprovePlugin(t *testing.T) {
	testCases := []struct {
		id       string
		approves []plugins.Approve
		expected []plugins.Approve
	}{
		{
			id: "no changes expected",
			approves: []plugins.Approve{
				{
					IgnoreReviewState: pBool(false),
					LgtmActsAsApprove: true,
					Repos:             []string{"openshift/anotherRepo1", "testshift/anotherRepo2"},
				},
				{
					IgnoreReviewState: pBool(false),
					LgtmActsAsApprove: true,
					Repos:             []string{"openshift/anotherRepo3", "testshift/anotherRepo4"},
				},
			},
			expected: []plugins.Approve{
				{
					IgnoreReviewState: pBool(false),
					LgtmActsAsApprove: true,
					Repos:             []string{"openshift/anotherRepo1", "testshift/anotherRepo2"},
				},
				{
					IgnoreReviewState: pBool(false),
					LgtmActsAsApprove: true,
					Repos:             []string{"openshift/anotherRepo3", "testshift/anotherRepo4"},
				},
			},
		},
		{
			id: "changes expected",
			approves: []plugins.Approve{
				{
					IgnoreReviewState: pBool(false),
					LgtmActsAsApprove: true,
					Repos:             []string{"openshift/testRepo1", "testshift/anotherRepo2"},
				},
				{
					IgnoreReviewState: pBool(false),
					LgtmActsAsApprove: true,
					Repos:             []string{"openshift/anotherRepo3", "testshift/testRepo3"},
				},
			},
			expected: []plugins.Approve{
				{
					IgnoreReviewState: pBool(false),
					LgtmActsAsApprove: true,
					Repos:             []string{"openshift-priv/testRepo1", "openshift/testRepo1", "testshift/anotherRepo2"},
				},
				{
					IgnoreReviewState: pBool(false),
					LgtmActsAsApprove: true,
					Repos:             []string{"openshift-priv/testRepo3", "openshift/anotherRepo3", "testshift/testRepo3"},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			injectPrivateApprovePlugin(tc.approves, orgRepos)
			if !reflect.DeepEqual(tc.approves, tc.expected) {
				t.Fatal(cmp.Diff(tc.approves, tc.expected))
			}
		})
	}
}

func TestInjectPrivateLGTMPlugin(t *testing.T) {
	testCases := []struct {
		id       string
		lgtms    []plugins.Lgtm
		expected []plugins.Lgtm
	}{
		{
			id: "no changes expected",
			lgtms: []plugins.Lgtm{
				{
					ReviewActsAsLgtm: true,
					Repos:            []string{"openshift/anotherRepo1", "testshift/anotherRepo2"},
				},
				{
					ReviewActsAsLgtm: true,
					Repos:            []string{"openshift/anotherRepo3", "testshift/anotherRepo4"},
				},
			},
			expected: []plugins.Lgtm{
				{
					ReviewActsAsLgtm: true,
					Repos:            []string{"openshift/anotherRepo1", "testshift/anotherRepo2"},
				},
				{
					ReviewActsAsLgtm: true,
					Repos:            []string{"openshift/anotherRepo3", "testshift/anotherRepo4"},
				},
			},
		},
		{
			id: "changes expected",
			lgtms: []plugins.Lgtm{
				{
					ReviewActsAsLgtm: true,
					Repos:            []string{"openshift/testRepo1", "testshift/anotherRepo2"},
				},
				{
					ReviewActsAsLgtm: true,
					Repos:            []string{"openshift/anotherRepo3", "testshift/testRepo3"},
				},
			},
			expected: []plugins.Lgtm{
				{
					ReviewActsAsLgtm: true,
					Repos:            []string{"openshift-priv/testRepo1", "openshift/testRepo1", "testshift/anotherRepo2"},
				},
				{
					ReviewActsAsLgtm: true,
					Repos:            []string{"openshift-priv/testRepo3", "openshift/anotherRepo3", "testshift/testRepo3"},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			injectPrivateLGTMPlugin(tc.lgtms, orgRepos)
			if !reflect.DeepEqual(tc.lgtms, tc.expected) {
				t.Fatal(cmp.Diff(tc.lgtms, tc.expected))
			}
		})
	}
}

func TestInjectPrivateBugzillaPlugin(t *testing.T) {
	testCases := []struct {
		id       string
		bugzilla plugins.Bugzilla
		expected plugins.Bugzilla
	}{
		{
			id: "no changes expected",
			bugzilla: plugins.Bugzilla{
				Orgs: map[string]plugins.BugzillaOrgOptions{
					"openshift": {Repos: map[string]plugins.BugzillaRepoOptions{
						"anotherRepo1": {Branches: map[string]plugins.BugzillaBranchOptions{"master": {ExcludeDefaults: pBool(true)}}}}},
					"testshift": {Repos: map[string]plugins.BugzillaRepoOptions{
						"anotherRepo2": {Branches: map[string]plugins.BugzillaBranchOptions{"master": {ExcludeDefaults: pBool(true)}}}}},
				},
			},
			expected: plugins.Bugzilla{
				Orgs: map[string]plugins.BugzillaOrgOptions{
					"openshift": {Repos: map[string]plugins.BugzillaRepoOptions{
						"anotherRepo1": {Branches: map[string]plugins.BugzillaBranchOptions{"master": {ExcludeDefaults: pBool(true)}}}}},
					"testshift": {Repos: map[string]plugins.BugzillaRepoOptions{
						"anotherRepo2": {Branches: map[string]plugins.BugzillaBranchOptions{"master": {ExcludeDefaults: pBool(true)}}}}},
				},
			},
		},

		{
			id: "changes expected",
			bugzilla: plugins.Bugzilla{
				Orgs: map[string]plugins.BugzillaOrgOptions{
					"openshift": {Repos: map[string]plugins.BugzillaRepoOptions{
						"testRepo1": {Branches: map[string]plugins.BugzillaBranchOptions{"master": {ExcludeDefaults: pBool(true)}}}}},
					"testshift": {Repos: map[string]plugins.BugzillaRepoOptions{
						"testRepo3": {Branches: map[string]plugins.BugzillaBranchOptions{"master": {ExcludeDefaults: pBool(true)}}}}},
				},
			},
			expected: plugins.Bugzilla{
				Orgs: map[string]plugins.BugzillaOrgOptions{
					"openshift": {Repos: map[string]plugins.BugzillaRepoOptions{
						"testRepo1": {Branches: map[string]plugins.BugzillaBranchOptions{"master": {ExcludeDefaults: pBool(true)}}}}},
					"testshift": {Repos: map[string]plugins.BugzillaRepoOptions{
						"testRepo3": {Branches: map[string]plugins.BugzillaBranchOptions{"master": {ExcludeDefaults: pBool(true)}}}}},
					"openshift-priv": {Repos: map[string]plugins.BugzillaRepoOptions{
						"testRepo1": {Branches: map[string]plugins.BugzillaBranchOptions{"master": {ExcludeDefaults: pBool(true)}}},
						"testRepo3": {Branches: map[string]plugins.BugzillaBranchOptions{"master": {ExcludeDefaults: pBool(true)}}}},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			injectPrivateBugzillaPlugin(tc.bugzilla, orgRepos)
			if !reflect.DeepEqual(tc.bugzilla, tc.expected) {
				t.Fatal(cmp.Diff(tc.bugzilla, tc.expected))
			}
		})
	}
}

func TestInjectPrivatePlugins(t *testing.T) {
	testCases := []struct {
		id       string
		plugins  map[string][]string
		expected map[string][]string
	}{
		{
			id:       "no changes expected",
			plugins:  map[string][]string{"openshift/anotherRepo1": {"approve", "lgtm", "cat", "dog"}},
			expected: map[string][]string{"openshift/anotherRepo1": {"approve", "lgtm", "cat", "dog"}},
		},
		{
			id: "changes expected",
			plugins: map[string][]string{
				"openshift/testRepo1": {"approve", "lgtm", "cat", "dog"},
			},
			expected: map[string][]string{
				"openshift-priv/testRepo1": {"approve", "cat", "dog", "lgtm"},
				"openshift/testRepo1":      {"approve", "lgtm", "cat", "dog"},
			},
		},
		{
			id: "changes expected, multiple org/repos",
			plugins: map[string][]string{
				"openshift":           {"lgtm", "cat", "dog", "hold"},
				"testshift":           {"lgtm", "milestone", "label", "hold"},
				"openshift/testRepo1": {"approve"},
				"testshift/testRepo3": {"approve", "trigger"},
			},
			expected: map[string][]string{
				"openshift":           {"lgtm", "cat", "dog", "hold"},
				"testshift":           {"lgtm", "milestone", "label", "hold"},
				"openshift/testRepo1": {"approve"},
				"testshift/testRepo3": {"approve", "trigger"},

				"openshift-priv":           {"hold", "lgtm"},
				"openshift-priv/testRepo1": {"approve", "cat", "dog"},
				"openshift-priv/testRepo2": {"cat", "dog"},
				"openshift-priv/testRepo3": {"approve", "label", "milestone", "trigger"},
				"openshift-priv/testRepo4": {"label", "milestone"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			injectPrivatePlugins(tc.plugins, orgRepos)
			if !reflect.DeepEqual(tc.plugins, tc.expected) {
				t.Fatal(cmp.Diff(tc.plugins, tc.expected))
			}
		})
	}
}

func TestGetCommonPlugins(t *testing.T) {
	plugins := map[string][]string{
		"openshift/repo1":   {"approve", "label", "hold", "cat", "dog"},
		"openshift/repo2":   {"approve", "label", "hold", "lgtm", "milestone"},
		"openshift/repo3":   {"approve", "label", "hold", "trigger"},
		"openshift/repo4":   {"approve", "label", "hold", "lgtm"},
		"openshift/repo5":   {"approve", "label", "hold", "lgtm"},
		"openshift/repo6":   {"approve", "label", "hold", "trigger"},
		"openshift/repo7":   {"approve", "label", "hold", "cat", "bugzilla"},
		"openshift/repo8":   {"approve", "label", "hold", "milestone"},
		"openshift/arepo9":  {"approve", "label", "hold", "bugzilla"},
		"openshift/arepo10": {"approve", "label", "hold", "milestone"},
		"openshift/arepo11": {"approve", "label", "hold", "lgtm", "milestone"},
		"openshift/arepo12": {"approve", "label", "hold", "lgtm", "milestone"},
	}
	expected := sets.String{"approve": sets.Empty{}, "hold": sets.Empty{}, "label": sets.Empty{}}

	commonValues := getCommonPlugins(plugins)
	if !reflect.DeepEqual(commonValues, expected) {
		t.Fatal(cmp.Diff(commonValues, expected))
	}
}
