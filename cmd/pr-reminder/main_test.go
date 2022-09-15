package main

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/slack-go/slack"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/github"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func Test_user_requestedToReview(t *testing.T) {
	testCases := []struct {
		name     string
		user     user
		pr       github.PullRequest
		expected bool
	}{
		{
			name: "user requested",
			user: user{GithubId: "some-id"},
			pr: github.PullRequest{
				RequestedReviewers: []github.User{
					{
						Login: "some-id",
					},
				},
			},
			expected: true,
		},
		{
			name: "user assigned",
			user: user{GithubId: "some-id"},
			pr: github.PullRequest{
				Assignees: []github.User{
					{
						Login: "some-id",
					},
				},
			},
			expected: true,
		},
		{
			name: "team requested",
			user: user{GithubId: "some-id", TeamNames: sets.NewString("some-team")},
			pr: github.PullRequest{
				RequestedTeams: []github.Team{
					{
						Slug: "some-team",
					},
				},
			},
			expected: true,
		},
		{
			name: "team requested while user is author",
			user: user{GithubId: "some-id", TeamNames: sets.NewString("some-team")},
			pr: github.PullRequest{
				User: github.User{
					Login: "some-id",
				},
				RequestedTeams: []github.Team{
					{
						Slug: "some-team",
					},
				},
			},
			expected: false,
		},
		{
			name: "not requested",
			user: user{GithubId: "some-id"},
			pr: github.PullRequest{
				RequestedReviewers: []github.User{
					{
						Login: "a-different-id",
					},
				},
				RequestedTeams: []github.Team{
					{
						Slug: "some-other-team",
					},
				},
			},
			expected: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			requested := tc.user.requestedToReview(tc.pr)
			if requested != tc.expected {
				t.Fatalf("requestedToReview returned %v, expected %v", requested, tc.expected)
			}
		})
	}
}

func Test_prRequest_recency(t *testing.T) {
	testCases := []struct {
		name      string
		prRequest prRequest
		expected  string
	}{
		{
			name: "recent PR",
			prRequest: prRequest{
				Created: time.Now().Add(-1 * time.Hour),
			},
			expected: recent,
		},
		{
			name: "5 day old PR",
			prRequest: prRequest{
				Created: time.Now().Add(-24 * 5 * time.Hour),
			},
			expected: normal,
		},
		{
			name: "old PR",
			prRequest: prRequest{
				Created: time.Now().Add(-24 * 30 * time.Hour),
			},
			expected: old,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recency := tc.prRequest.recency()
			if diff := cmp.Diff(tc.expected, recency); diff != "" {
				t.Fatalf("recency resulted didn't match expected, diff: %s", diff)
			}
		})
	}
}

type fakeGithubClient struct {
	prs map[string][]github.PullRequest
}

func (c fakeGithubClient) GetPullRequests(org, repo string) ([]github.PullRequest, error) {
	orgRepo := fmt.Sprintf("%s/%s", org, repo)
	return c.prs[orgRepo], nil
}

func TestFindPRsForUsers(t *testing.T) {
	now := time.Now()
	client := fakeGithubClient{prs: map[string][]github.PullRequest{
		"org/repo-1": {
			{
				Number:    1,
				HTMLURL:   "github.com/org/repo-1/1",
				Title:     "Some PR",
				User:      github.User{Login: "a-user"},
				CreatedAt: now,
				UpdatedAt: now,
				RequestedReviewers: []github.User{
					{
						Login: "id-1",
					},
				},
				RequestedTeams: []github.Team{
					{
						Slug: "some-team",
					},
				},
			},
			{
				Number:    2,
				HTMLURL:   "github.com/org/repo-1/2",
				Title:     "Some Other PR",
				User:      github.User{Login: "some-user"},
				CreatedAt: now,
				UpdatedAt: now,
				RequestedReviewers: []github.User{
					{
						Login: "id-2",
					},
				},
				RequestedTeams: []github.Team{
					{
						Slug: "some-other-team",
					},
				},
			},
		},
		"org/repo-2": {
			{
				Number:    66,
				HTMLURL:   "github.com/org/repo-2/66",
				Title:     "Some PR in this repo",
				User:      github.User{Login: "a-user"},
				CreatedAt: now,
				UpdatedAt: now,
				RequestedReviewers: []github.User{
					{
						Login: "a-different-id",
					},
				},
				RequestedTeams: []github.Team{
					{
						Slug: "some-team",
					},
				},
			},
		},
	}}

	testCases := []struct {
		name     string
		users    map[string]user
		expected map[string]user
	}{
		{
			name: "PRs exist",
			users: map[string]user{
				"someuser": {
					KerberosId: "someuser",
					GithubId:   "id-1",
					TeamNames:  sets.NewString("some-team"),
					Repos:      sets.NewString("org/repo-1", "org/repo-2"),
				},
				"user-b": {
					KerberosId: "user-b",
					GithubId:   "id-2",
					TeamNames:  sets.NewString("some-team"),
					Repos:      sets.NewString("org/repo-1", "org/repo-2"),
				},
			},
			expected: map[string]user{
				"someuser": {
					KerberosId: "someuser",
					GithubId:   "id-1",
					TeamNames:  sets.NewString("some-team"),
					Repos:      sets.NewString("org/repo-1", "org/repo-2"),
					PrRequests: []prRequest{
						{
							Repo:        "org/repo-1",
							Number:      1,
							Url:         "github.com/org/repo-1/1",
							Title:       "Some PR",
							Author:      "a-user",
							Created:     now,
							LastUpdated: now,
						},
						{
							Repo:        "org/repo-2",
							Number:      66,
							Url:         "github.com/org/repo-2/66",
							Title:       "Some PR in this repo",
							Author:      "a-user",
							Created:     now,
							LastUpdated: now,
						},
					},
				},
				"user-b": {
					KerberosId: "user-b",
					GithubId:   "id-2",
					TeamNames:  sets.NewString("some-team"),
					Repos:      sets.NewString("org/repo-1", "org/repo-2"),
					PrRequests: []prRequest{
						{
							Repo:        "org/repo-1",
							Number:      1,
							Url:         "github.com/org/repo-1/1",
							Title:       "Some PR",
							Author:      "a-user",
							Created:     now,
							LastUpdated: now,
						},
						{
							Repo:        "org/repo-1",
							Number:      2,
							Url:         "github.com/org/repo-1/2",
							Title:       "Some Other PR",
							Author:      "some-user",
							Created:     now,
							LastUpdated: now,
						},
						{
							Repo:        "org/repo-2",
							Number:      66,
							Url:         "github.com/org/repo-2/66",
							Title:       "Some PR in this repo",
							Author:      "a-user",
							Created:     now,
							LastUpdated: now,
						},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			prs := findPrsForUsers(tc.users, client)
			if diff := cmp.Diff(tc.expected, prs); diff != "" {
				t.Fatalf("findPRsForUsers resulted didn't match expected, diff: %s", diff)
			}
		})
	}
}

type fakeSlackClient struct {
	userIdsByEmail map[string]string
}

func (c fakeSlackClient) GetUserByEmail(email string) (*slack.User, error) {
	userId, exists := c.userIdsByEmail[email]
	if exists {
		return &slack.User{ID: userId}, nil
	}

	return nil, fmt.Errorf("no userId found for email: %s", email)
}

func (c fakeSlackClient) PostMessage(channelID string, options ...slack.MsgOption) (string, string, error) {
	//No-op
	return "", "", nil
}

func Test_config_CreateUsers(t *testing.T) {
	client := fakeSlackClient{userIdsByEmail: map[string]string{"user1@redhat.com": "U1000000", "user2@redhat.com": "U222222", "user3@redhat.com": "U333333"}}
	testCases := []struct {
		name        string
		config      config
		gtk         githubToKerberos
		expected    map[string]user
		expectedErr error
	}{
		{
			name: "valid inputs",
			config: config{
				Teams: []team{
					{
						TeamMembers: []string{"user1", "user2"},
						TeamNames:   []string{"some-team", "other-team"},
						Repos:       []string{"org/repo"},
					},
					{
						TeamMembers: []string{"user3"},
						TeamNames:   []string{"some-team"},
						Repos:       []string{"other-org/repo"},
					},
				},
			},
			gtk: githubToKerberos{"user-1": "user1", "user-2": "user2", "user-3": "user3"},
			expected: map[string]user{
				"user1": {
					KerberosId: "user1",
					GithubId:   "user-1",
					SlackId:    "U1000000",
					TeamNames:  sets.NewString("some-team", "other-team"),
					Repos:      sets.NewString("org/repo"),
				},
				"user2": {
					KerberosId: "user2",
					GithubId:   "user-2",
					SlackId:    "U222222",
					TeamNames:  sets.NewString("some-team", "other-team"),
					Repos:      sets.NewString("org/repo"),
				},
				"user3": {
					KerberosId: "user3",
					GithubId:   "user-3",
					SlackId:    "U333333",
					TeamNames:  sets.NewString("some-team"),
					Repos:      sets.NewString("other-org/repo"),
				},
			},
		},
		{
			name: "user on multiple teams",
			config: config{
				Teams: []team{
					{
						TeamMembers: []string{"user1"},
						TeamNames:   []string{"some-team", "other-team"},
						Repos:       []string{"org/repo"},
					},
					{
						TeamMembers: []string{"user1"},
						TeamNames:   []string{"some-team", "additional-team"},
						Repos:       []string{"other-org/repo"},
					},
				},
			},
			gtk: githubToKerberos{"user-1": "user1"},
			expected: map[string]user{
				"user1": {
					KerberosId: "user1",
					GithubId:   "user-1",
					SlackId:    "U1000000",
					TeamNames:  sets.NewString("some-team", "other-team", "additional-team"),
					Repos:      sets.NewString("org/repo", "other-org/repo"),
				},
			},
		},
		{
			name: "no slack user found for a configured user",
			config: config{
				Teams: []team{{
					TeamMembers: []string{"user1", "user4"},
					TeamNames:   []string{"some-team"},
				}},
			},
			gtk: githubToKerberos{"user-1": "user1", "user-2": "user2"},
			expected: map[string]user{
				"user1": {
					KerberosId: "user1",
					GithubId:   "user-1",
					SlackId:    "U1000000",
					TeamNames:  sets.NewString("some-team"),
				},
			},
			expectedErr: errors.New("[could not get slack id for: user4: no userId found for email: user4@redhat.com, no githubId found for: user4]"),
		},
		{
			name: "no github user found for a configured user",
			config: config{
				Teams: []team{{
					TeamMembers: []string{"user1", "user2"},
					TeamNames:   []string{"some-team"},
				}},
			},
			gtk: githubToKerberos{"user-1": "user1"},
			expected: map[string]user{
				"user1": {
					KerberosId: "user1",
					GithubId:   "user-1",
					SlackId:    "U1000000",
					TeamNames:  sets.NewString("some-team"),
				},
			},
			expectedErr: errors.New("no githubId found for: user2"),
		},
		{
			name: "missing github id and missing slack id for different users",
			config: config{
				Teams: []team{{
					TeamMembers: []string{"user1", "user2", "user4"},
					TeamNames:   []string{"some-team"},
				}},
			},
			gtk: githubToKerberos{"user-1": "user1", "user-4": "user4"},
			expected: map[string]user{
				"user1": {
					KerberosId: "user1",
					GithubId:   "user-1",
					SlackId:    "U1000000",
					TeamNames:  sets.NewString("some-team"),
				},
			},
			expectedErr: errors.New("[could not get slack id for: user4: no userId found for email: user4@redhat.com, no githubId found for: user2]"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			users, err := tc.config.createUsers(tc.gtk, client)
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("returned error doesn't match expected, diff: %s", diff)
			}

			if diff := cmp.Diff(tc.expected, users); diff != "" {
				t.Fatalf("returned users don't match expected, diff: %s", diff)
			}
		})
	}
}

func Test_config_validate(t *testing.T) {
	client := fakeSlackClient{userIdsByEmail: map[string]string{"user1@redhat.com": "U1000000", "user2@redhat.com": "U222222", "no-gh@redhat.com": "U4444", "user5@redhat.com": "U55555555"}}
	gtk := githubToKerberos{"user-1": "user1", "user-2": "user2", "noslack": "no-slack", "user-5": "user5"}

	testCases := []struct {
		name     string
		config   config
		expected error
	}{
		{
			name: "valid",
			config: config{
				Teams: []team{
					{
						TeamMembers: []string{"user1", "user2"},
						TeamNames:   []string{"some-team"},
						Repos:       []string{"org/repo", "org/repo2", "org2/repo"},
					},
					{
						TeamMembers: []string{"user5"},
						TeamNames:   []string{"some-other-team"},
						Repos:       []string{"org2/repo"},
					},
				},
			},
		},
		{
			name: "no teamMembers",
			config: config{
				Teams: []team{
					{
						TeamNames: []string{"some-team"},
						Repos:     []string{"org/repo", "org/repo2", "org2/repo"},
					},
				},
			},
			expected: errors.New("teams[0] doesn't contain any teamMembers"),
		},
		{
			name: "invalid repo",
			config: config{
				Teams: []team{
					{
						TeamMembers: []string{"user1", "user2"},
						TeamNames:   []string{"some-team"},
						Repos:       []string{"repo", "org/repo2", "org2/repo"},
					},
				},
			},
			expected: errors.New("teams[0] has improperly formatted org/repo: repo"),
		},
		{
			name: "invalid teamMembers",
			config: config{
				Teams: []team{
					{
						TeamMembers: []string{"no-slack", "no-gh"},
						TeamNames:   []string{"some-team"},
						Repos:       []string{"org/repo"},
					},
				},
			},
			expected: errors.New("[could not get slack id for: no-slack: no userId found for email: no-slack@redhat.com, no githubId found for: no-gh]"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.validate(gtk, client)
			if diff := cmp.Diff(tc.expected, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("returned error doesn't match expected, diff: %s", diff)
			}
		})
	}
}

func Test_filterLabels(t *testing.T) {
	holdLabel := github.Label{Name: "do-not-merge/hold"}
	acceptedLabel := github.Label{Name: "accepted"}
	unwantedLabel := github.Label{Name: "not interesting label"}

	interestingLabels := sets.String{}
	interestingLabels.Insert(holdLabel.Name, acceptedLabel.Name)

	testCases := []struct {
		name     string
		prLabels []github.Label
		expected []string
	}{
		{
			name:     "pr with no labels",
			prLabels: []github.Label{},
			expected: nil,
		},
		{
			name:     "pr with one label we are interested in",
			prLabels: []github.Label{holdLabel},
			expected: []string{holdLabel.Name},
		},
		{
			name:     "returned labels are in correct order",
			prLabels: []github.Label{holdLabel, acceptedLabel},
			expected: []string{acceptedLabel.Name, holdLabel.Name},
		},
		{
			name:     "pr with only uninteresting labels",
			prLabels: []github.Label{unwantedLabel},
			expected: nil,
		},
		{
			name:     "pr has one label we are not interested in",
			prLabels: []github.Label{holdLabel, unwantedLabel},
			expected: []string{holdLabel.Name},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := filterLabels(tc.prLabels, interestingLabels)
			if diff := cmp.Diff(actual, tc.expected); diff != "" {
				t.Fatalf("returned labels do not match expected labels, diff:%s", diff)
			}
		})
	}
}
