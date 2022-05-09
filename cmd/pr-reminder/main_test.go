package main

import (
	"errors"
	"fmt"
	"github.com/google/go-cmp/cmp"
	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/slack-go/slack"
	"k8s.io/test-infra/prow/github"
	"testing"
	"time"
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
			name: "team requested",
			user: user{GithubId: "some-id", TeamName: "some-team"},
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
			user: user{GithubId: "some-id", TeamName: "some-team"},
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
		repos    []string
		expected map[string]user
	}{
		{
			name: "PRs exist",
			users: map[string]user{
				"someuser": {
					KerberosId: "someuser",
					GithubId:   "id-1",
					TeamName:   "some-team",
				},
				"user-b": {
					KerberosId: "user-b",
					GithubId:   "id-2",
					TeamName:   "some-team",
				},
			},
			repos: []string{"org/repo-1", "org/repo-2"},
			expected: map[string]user{
				"someuser": {
					KerberosId: "someuser",
					GithubId:   "id-1",
					TeamName:   "some-team",
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
					TeamName:   "some-team",
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
			prs := findPrsForUsers(tc.users, tc.repos, client)
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

func TestCreateUsers(t *testing.T) {
	client := fakeSlackClient{userIdsByEmail: map[string]string{"user1@redhat.com": "U1000000", "user2@redhat.com": "U222222"}}
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
				TeamMembers: []string{"user1", "user2"},
				TeamName:    "some-team",
			},
			gtk: githubToKerberos{"user-1": "user1", "user-2": "user2"},
			expected: map[string]user{
				"user1": {
					KerberosId: "user1",
					GithubId:   "user-1",
					SlackId:    "U1000000",
					TeamName:   "some-team",
				},
				"user2": {
					KerberosId: "user2",
					GithubId:   "user-2",
					SlackId:    "U222222",
					TeamName:   "some-team",
				},
			},
		},
		{
			name: "no slack user found",
			config: config{
				TeamMembers: []string{"user1", "user3"},
				TeamName:    "some-team",
			},
			gtk:         githubToKerberos{"user-1": "user1", "user-2": "user2"},
			expectedErr: errors.New("could not get slack user for user3: no userId found for email: user3@redhat.com"),
		},
		{
			name: "no github user found",
			config: config{
				TeamMembers: []string{"user1", "user2"},
				TeamName:    "some-team",
			},
			gtk:         githubToKerberos{"user-1": "user1"},
			expectedErr: errors.New("no githubId found for user(s): [user2]"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			users, err := createUsers(tc.config, tc.gtk, client)
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("returned error doesn't match expected, diff: %s", diff)
			}

			if diff := cmp.Diff(tc.expected, users); diff != "" {
				t.Fatalf("returned users don't match expected, diff: %s", diff)
			}
		})
	}
}
