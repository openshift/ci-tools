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

type fakeAutomationClient struct {
	collaboratorsByRepo   map[string][]string
	membersByOrg          map[string][]string
	reposWithAppInstalled sets.Set[string]
}

func (c fakeAutomationClient) IsMember(org, user string) (bool, error) {
	for _, member := range c.membersByOrg[org] {
		if user == member {
			return true, nil
		}
	}
	if org == "fake" {
		return false, errors.New("intentional error")
	}

	return false, nil
}

func (c fakeAutomationClient) IsCollaborator(org, repo, user string) (bool, error) {
	orgRepo := fmt.Sprintf("%s/%s", org, repo)
	collaborators := c.collaboratorsByRepo[orgRepo]
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

func (c fakeAutomationClient) IsAppInstalled(org, repo string) (bool, error) {
	if repo == "error" {
		return false, errors.New("intentional error")
	}

	orgRepo := fmt.Sprintf("%s/%s", org, repo)
	return c.reposWithAppInstalled.Has(orgRepo), nil
}

func TestCheckRepos(t *testing.T) {
	client := fakeAutomationClient{
		collaboratorsByRepo: map[string][]string{
			"org-1/repo-a": {"a-bot", "b-bot", "c-bot"},
			"org-2/repo-z": {"c-bot", "some-user"},
		},
		membersByOrg: map[string][]string{
			"org-1": {"a-user", "d-bot", "e-bot", "openshift-cherrypick-robot"},
			"org-2": {"some-user", "z-bot"},
			"org-3": {"a-user", "openshift-cherrypick-robot"},
		},
		reposWithAppInstalled: sets.New[string]("org-1/repo-a", "org-2/repo-z"),
	}

	testCases := []struct {
		name        string
		repos       []string
		bots        []string
		ignore      sets.Set[string]
		expected    []string
		expectedErr error
	}{
		{
			name:     "org has bots as members",
			repos:    []string{"org-1/repo-a"},
			bots:     []string{"d-bot", "e-bot"},
			expected: []string{},
		},
		{
			name:     "org has one bot as member, and one as collaborator",
			repos:    []string{"org-1/repo-a"},
			bots:     []string{"a-bot", "e-bot"},
			expected: []string{},
		},
		{
			name:     "repo has bots as collaborators",
			repos:    []string{"org-1/repo-a"},
			bots:     []string{"a-bot", "b-bot"},
			expected: []string{},
		},
		{
			name:     "org doesn't have bots as members, and repo doesn't have bots as collaborators",
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
			name:     "app installed, no bots",
			repos:    []string{"org-1/repo-a"},
			expected: []string{},
		},
		{
			name:     "app not installed",
			repos:    []string{"org-3/repo-y"},
			bots:     []string{"a-bot", "b-bot"},
			expected: []string{"org-3/repo-y"},
		},
		{
			name:     "ignored repo",
			repos:    []string{"org-2/repo-z"},
			bots:     []string{"a-bot", "b-bot"},
			ignore:   sets.New[string]("org-2/repo-z"),
			expected: []string{},
		},
		{
			name:     "ignored org",
			repos:    []string{"org-2/repo-z"},
			bots:     []string{"a-bot", "b-bot"},
			ignore:   sets.New[string]("org-2"),
			expected: []string{},
		},
		{
			name:        "org member check returns error",
			repos:       []string{"fake/repo"},
			bots:        []string{"a-bot"},
			expectedErr: errors.New("unable to determine if: a-bot is a member of fake: intentional error"),
		},
		{
			name:        "collaborator check returns error",
			repos:       []string{"org-1/fake"},
			bots:        []string{"a-bot"},
			expectedErr: errors.New("unable to determine if: a-bot is a collaborator on org-1/fake: intentional error"),
		},
		{
			name:        "app install check returns error",
			repos:       []string{"org-1/error"},
			bots:        []string{"a-bot"},
			expectedErr: errors.New("unable to determine if openshift-ci app is installed on org-1/error: intentional error"),
		},
		{
			name:     "openshift-cherrypick-robot is an org member",
			repos:    []string{"org-1/repo-a"},
			bots:     []string{"d-bot", "e-bot"},
			expected: []string{},
		},
		{
			name:     "openshift-cherrypick-robot is not an org member",
			repos:    []string{"org-2/repo-z"},
			bots:     []string{"z-bot"},
			expected: []string{"org-2/repo-z"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			failing, err := checkRepos(tc.repos, tc.bots, tc.ignore, client, logrus.NewEntry(logrus.New()))
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error doesn't match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expected, failing); diff != "" {
				t.Fatalf("returned failing repos did not match expected, diff: %s", diff)
			}
		})
	}
}
