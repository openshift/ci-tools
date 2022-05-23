package main

import (
	"fmt"
	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	"testing"
)

type fakeCollaboratorClient struct {
	repos map[string][]string
}

func (c fakeCollaboratorClient) IsCollaborator(org, repo, user string) (bool, error) {
	orgRepo := fmt.Sprintf("%s/%s", org, repo)
	collaborators := c.repos[orgRepo]
	for _, collaborator := range collaborators {
		if collaborator == user {
			return true, nil
		}
	}

	return false, nil
}

func TestCheckRepos(t *testing.T) {
	client := fakeCollaboratorClient{repos: map[string][]string{
		"org-1/repo-a": {"a-bot", "b-bot", "c-bot"},
		"org-2/repo-z": {"c-bot", "some-user"},
	}}

	testCases := []struct {
		name     string
		repos    []string
		bots     []string
		expected []string
	}{
		{
			name:  "repo has bots as collaborators",
			repos: []string{"org-1/repo-a"},
			bots:  []string{"a-bot", "b-bot"},
		},
		{
			name:     "repo doesn't have bots as collaborators",
			repos:    []string{"org-2/repo-z"},
			bots:     []string{"a-bot", "b-bot"},
			expected: []string{"org-2/repo-z"},
		},
		{
			name:     "multiple repos, some passing",
			repos:    []string{"org-1/repo-a", "org-2/repo-z"},
			bots:     []string{"a-bot", "b-bot"},
			expected: []string{"org-2/repo-z"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			failing, err := checkRepos(tc.repos, tc.bots, client, logrus.NewEntry(logrus.New()))
			if err != nil {
				t.Fatalf("unexpected error returned: %v", err)
			}
			if diff := cmp.Diff(tc.expected, failing); diff != "" {
				t.Fatalf("returned failing repos did not match expected, diff: %s", diff)
			}
		})
	}
}
