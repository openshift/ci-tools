package main

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/git/localgit"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/github/fakegithub"
)

func TestCheckPrerequisites(t *testing.T) {
	logrus.SetLevel(logrus.DebugLevel)

	testCases := []struct {
		id          string
		commentBody string

		isMember      bool
		isMerged      bool
		isPullRequest bool

		repositories     map[string]string
		expectedComments []github.IssueComment
		expectedError    error
	}{

		{
			id:            "issue is not a pull request",
			commentBody:   "/publicize",
			isMember:      true,
			repositories:  map[string]string{"org-priv/repo": "org/repo"},
			expectedError: errors.New("Publicize plugin can only be used in pull requests"),
		},
		{
			id:            "user is no org member",
			commentBody:   "/publicize",
			isMember:      false,
			isPullRequest: true,
			repositories:  map[string]string{"org-priv/repo": "org/repo"},
			expectedError: errors.New("only [org-priv](https://github.com/orgs/org-priv/people) org members may request publication of a private pull request"),
		},
		{
			id:            "pull request is not merged",
			commentBody:   "/publicize",
			isMember:      true,
			isMerged:      false,
			isPullRequest: true,
			repositories:  map[string]string{"org-priv/repo": "org/repo"},
			expectedError: errors.New("cannot publicize an unmerged pull request"),
		},
		{
			id:            "repository has no upstream repository configured",
			commentBody:   "/publicize",
			isMember:      true,
			isMerged:      true,
			isPullRequest: true,
			repositories:  map[string]string{"org-priv/anotherRepo": "org/anotherRepo"},
			expectedError: errors.New("cannot publicize because there is no upstream repository configured for org-priv/repo"),
		},
		{
			id:            "a hapy publicize",
			commentBody:   "/publicize",
			isMember:      true,
			isMerged:      true,
			isPullRequest: true,
			repositories:  map[string]string{"org-priv/repo": "org/repo"},
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			issueState := "open"
			if tc.isMerged {
				issueState = "closed"
			}

			prNumber := 1111
			fc := &fakegithub.FakeClient{
				IssueComments: make(map[int][]github.IssueComment),
				OrgMembers:    map[string][]string{"org-priv": {"k8s-ci-robot"}},
				PullRequests: map[int]*github.PullRequest{
					prNumber: {
						ID:     1,
						Number: prNumber,
						User:   github.User{Login: "pr-user"},
						Title:  tc.id,
						Body:   tc.id,
						Merged: tc.isMerged,
						Base:   github.PullRequestBranch{Ref: "master"},
					},
				},
			}

			localGit, gcf, err := localgit.NewV2()
			defer func() {
				if err := localGit.Clean(); err != nil {
					t.Errorf("couldn't clean localgit temp folders: %v", err)
				}

				if err := gcf.Clean(); err != nil {
					t.Errorf("coulnd't clean git client's temp folders: %v", err)
				}
			}()

			if err != nil {
				t.Fatal(err)
			}

			if err := localGit.MakeFakeRepo("org", "repo"); err != nil {
				t.Fatal(err)
			}

			if err := localGit.MakeFakeRepo("org-priv", "repo"); err != nil {
				t.Fatal(err)
			}

			ice := github.IssueCommentEvent{
				Action: github.IssueCommentActionCreated,
				Comment: github.IssueComment{
					Body: tc.commentBody,
				},
				Issue: github.Issue{
					User:      github.User{Login: "k8s-ci-robot"},
					Number:    prNumber,
					State:     issueState,
					Assignees: []github.User{{Login: "dptp-assignee"}},
				},

				Repo: github.Repo{
					Owner: github.User{Login: "org-priv"},
					Name:  "repo",
				},
			}

			if tc.isPullRequest {
				ice.Issue.PullRequest = &struct{}{}
			}

			if tc.isMember {
				ice.Comment.User.Login = "k8s-ci-robot"
			}

			serv := &server{
				gitName:  "test",
				gitEmail: "test@test.com",
				ghc:      fc,
				gc:       gcf,
				config: func() *Config {
					c := &Config{}
					c.Repositories = tc.repositories
					return c
				},
				secretAgent: &secret.Agent{},
				dry:         true,
			}

			actualErr := serv.checkPrerequisites(logrus.WithField("id", tc.id), fc.PullRequests[1111], ice)

			if !reflect.DeepEqual(actualErr, tc.expectedError) {
				t.Fatalf(cmp.Diff(actualErr.Error(), tc.expectedError.Error()))
			}
		})
	}
}
