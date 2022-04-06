package main

import (
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/github/fakegithub"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

type fakeGithubClient struct {
	*fakegithub.FakeClient
}

func (f fakeGithubClient) FindIssues(query, sortVerb string, asc bool) ([]github.Issue, error) {
	var issues []github.Issue
	for _, issue := range f.FakeClient.Issues {
		issues = append(issues, *issue)
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	return issues, nil
}

func TestManageIssues(t *testing.T) {
	testCases := []struct {
		id             string
		branches       sets.String
		issues         map[int]*github.Issue
		repoInfo       *config.Info
		expectedIssues []github.Issue
	}{
		{
			id:       "all up to date case",
			branches: sets.NewString([]string{"release-4.9"}...),
			repoInfo: &config.Info{
				Metadata: cioperatorapi.Metadata{
					Org:    "testOrg",
					Repo:   "testRepo",
					Branch: "testBranch",
				},
			},
			issues: map[int]*github.Issue{
				1: {
					ID:     1,
					Title:  "Future Release Branches Frozen For Merging | branch:release-4.9",
					Body:   "The following branches are being fast-forwarded from the current development branch (testBranch) as placeholders for future releases. No merging is allowed into these release branches until they are unfrozen for production release.\n\n - `release-4.9`\n\nContact the [Test Platform](https://coreos.slack.com/messages/CBN38N3MW) or [Automated Release](https://coreos.slack.com/messages/CB95J6R4N) teams for more information.",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
				},
			},
			expectedIssues: []github.Issue{
				{
					ID:     1,
					Title:  "Future Release Branches Frozen For Merging | branch:release-6.66",
					Body:   "The following branches are being fast-forwarded from the current development branch (testBranch) as placeholders for future releases. No merging is allowed into these release branches until they are unfrozen for production release.\n\n - `release-4.9`\n\nContact the [Test Platform](https://coreos.slack.com/messages/CBN38N3MW) or [Automated Release](https://coreos.slack.com/messages/CB95J6R4N) teams for more information.",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
				},
			},
		},
		{
			id:       "create case",
			branches: sets.NewString([]string{"release-4.9"}...),
			repoInfo: &config.Info{
				Metadata: cioperatorapi.Metadata{
					Org:    "testOrg",
					Repo:   "testRepo",
					Branch: "testBranch",
				},
			},
			issues: map[int]*github.Issue{},
			expectedIssues: []github.Issue{
				{
					ID:     1,
					Title:  "Future Release Branches Frozen For Merging | branch:release-4.9",
					Body:   "The following branches are being fast-forwarded from the current development branch (testBranch) as placeholders for future releases. No merging is allowed into these release branches until they are unfrozen for production release.\n\n - `release-4.9`\n\nContact the [Test Platform](https://coreos.slack.com/messages/CBN38N3MW) or [Automated Release](https://coreos.slack.com/messages/CB95J6R4N) teams for more information.",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
				},
			},
		},
		{
			id:       "update case",
			branches: sets.NewString([]string{"release-4.9"}...),
			repoInfo: &config.Info{Metadata: cioperatorapi.Metadata{
				Org:    "testOrg",
				Repo:   "testRepo",
				Branch: "testBranch",
			},
			},
			issues: map[int]*github.Issue{
				1: {
					Number: 1,
					Title:  "Old Title",
					Body:   "Old Body",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
				},
			},
			expectedIssues: []github.Issue{
				{
					Number: 1,
					Title:  "Future Release Branches Frozen For Merging | branch:release-4.9",
					Body:   "The following branches are being fast-forwarded from the current development branch (testBranch) as placeholders for future releases. No merging is allowed into these release branches until they are unfrozen for production release.\n\n - `release-4.9`\n\nContact the [Test Platform](https://coreos.slack.com/messages/CBN38N3MW) or [Automated Release](https://coreos.slack.com/messages/CB95J6R4N) teams for more information.",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
				},
			},
		},
		{
			id:       "close multiple case",
			branches: sets.NewString([]string{"release-4.9"}...),
			repoInfo: &config.Info{Metadata: cioperatorapi.Metadata{
				Org:    "testOrg",
				Repo:   "testRepo",
				Branch: "testBranch",
			},
			},
			issues: map[int]*github.Issue{
				1: {
					ID:     1,
					Number: 1,
					Title:  "Old Title",
					Body:   "Old Body",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
				},
				2: {
					ID:     2,
					Number: 2,
					Title:  "Old Title",
					Body:   "Old Body",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
				},
				3: {
					ID:     3,
					Number: 3,
					Title:  "Old Title",
					Body:   "Old Body",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
				},
			},
			expectedIssues: []github.Issue{
				{
					ID:     1,
					Number: 1,
					Title:  "Future Release Branches Frozen For Merging | branch:release-4.9",
					Body:   "The following branches are being fast-forwarded from the current development branch (testBranch) as placeholders for future releases. No merging is allowed into these release branches until they are unfrozen for production release.\n\n - `release-4.9`\n\nContact the [Test Platform](https://coreos.slack.com/messages/CBN38N3MW) or [Automated Release](https://coreos.slack.com/messages/CB95J6R4N) teams for more information.",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
				},
				{
					ID:     2,
					Number: 2,
					Title:  "Old Title",
					Body:   "Old Body",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
					State:  "closed",
				},
				{
					ID:     3,
					Number: 3,
					Title:  "Old Title",
					Body:   "Old Body",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
					State:  "closed",
				},
			},
		},
		{
			id:       "close abandoned cases, branch list empty",
			branches: sets.NewString(),
			repoInfo: &config.Info{Metadata: cioperatorapi.Metadata{
				Org:    "testOrg",
				Repo:   "testRepo",
				Branch: "testBranch",
			},
			},
			issues: map[int]*github.Issue{
				1: {
					ID:     1,
					Number: 1,
					Title:  "Old Title",
					Body:   "Old Body",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
				},
				2: {
					ID:     2,
					Number: 2,
					Title:  "Old Title",
					Body:   "Old Body",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
				},
			},
			expectedIssues: []github.Issue{
				{
					ID:     1,
					Number: 1,
					Title:  "Old Title",
					Body:   "Old Body",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
					State:  "closed",
				},
				{
					ID:     2,
					Number: 2,
					Title:  "Old Title",
					Body:   "Old Body",
					Labels: []github.Label{{Name: "tide/merge-blocker"}},
					State:  "closed",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			fgh := fakeGithubClient{
				FakeClient: fakegithub.NewFakeClient(),
			}
			fgh.FakeClient.Issues = tc.issues

			if err := manageIssues(fgh, "", tc.repoInfo, tc.branches, logrus.WithField("id", tc.id)); err != nil {
				t.Fatal(err)
			}

			openedIssues, _ := fgh.ListOpenIssues(tc.repoInfo.Org, tc.repoInfo.Repo)
			sort.Slice(openedIssues, func(i, j int) bool { return openedIssues[i].ID < openedIssues[j].ID })

			if diff := cmp.Diff(openedIssues, tc.expectedIssues); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}
