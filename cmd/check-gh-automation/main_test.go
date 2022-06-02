package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/testhelper"
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
	if repo == "fake" {
		return false, errors.New("intentional error")
	}

	return false, nil
}

func TestCheckRepos(t *testing.T) {
	client := fakeCollaboratorClient{repos: map[string][]string{
		"org-1/repo-a": {"a-bot", "b-bot", "c-bot"},
		"org-2/repo-z": {"c-bot", "some-user"},
	}}

	testCases := []struct {
		name        string
		repos       []string
		bots        []string
		ignoreRepos sets.String
		expected    []string
		expectedErr error
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
		{
			name:        "ignored repo",
			repos:       []string{"org-2/repo-z"},
			bots:        []string{"a-bot", "b-bot"},
			ignoreRepos: sets.NewString("org-2/repo-z"),
		},
		{
			name:        "collaborator check returns error",
			repos:       []string{"org-1/fake"},
			bots:        []string{"a-bot"},
			expectedErr: errors.New("unable to determine if: a-bot is a collaborator on org-1/fake: intentional error"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			failing, err := checkRepos(tc.repos, tc.bots, tc.ignoreRepos, client, logrus.NewEntry(logrus.New()))
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error doesn't match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expected, failing); diff != "" {
				t.Fatalf("returned failing repos did not match expected, diff: %s", diff)
			}
		})
	}
}
