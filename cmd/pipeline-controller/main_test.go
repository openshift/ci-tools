package main

import (
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/labels"
)

type fakeGhClientWithComment struct {
	comment string
}

func (f *fakeGhClientWithComment) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	return &github.PullRequest{State: github.PullRequestStateOpen}, nil
}

func (f *fakeGhClientWithComment) CreateComment(owner, repo string, number int, comment string) error {
	f.comment = comment
	return nil
}

func (f *fakeGhClientWithComment) GetPullRequestChanges(org string, repo string, number int) ([]github.PullRequestChange, error) {
	return nil, nil
}

func (f *fakeGhClientWithComment) CreateStatus(org, repo, ref string, s github.Status) error {
	return nil
}
func TestHandleLabelAddition_RealFunctions(t *testing.T) {
	org := "openshift"
	repo := "assisted-installer"
	baseRef := "master"
	prNumber := 1

	basicEvent := github.PullRequestEvent{
		Action: github.PullRequestActionLabeled,
		Label:  github.Label{Name: labels.LGTM},
		Repo: github.Repo{
			Owner: github.User{Login: org},
			Name:  repo,
		},
		PullRequest: github.PullRequest{
			Number: prNumber,
			Base:   github.PullRequestBranch{Ref: baseRef},
		},
	}

	tests := []struct {
		name              string
		event             github.PullRequestEvent
		configData        map[string]presubmitTests
		expectCommentCall bool
		expectedComment   string
	}{
		{
			name: "action not labeled: do nothing",
			event: github.PullRequestEvent{
				Action: "opened",
			},
			expectCommentCall: false,
		},
		{
			name: "label not LGTM: do nothing",
			event: github.PullRequestEvent{
				Action: github.PullRequestActionLabeled,
				Label:  github.Label{Name: "not-lgtm"},
			},
			expectCommentCall: false,
		},
		{
			name:  "presubmits exist, sendComment succeeds",
			event: basicEvent,
			configData: map[string]presubmitTests{
				org + "/" + repo: {
					protected: []string{"dummy-test"},
				},
			},
			expectCommentCall: true,
			expectedComment:   "/test remaining-required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fakeGhClientWithComment{}

			cw := &clientWrapper{
				lgtmWatcher: &watcher{config: enabledConfig{Orgs: []struct {
					Org   string   `yaml:"org"`
					Repos []string `yaml:"repos"`
				}{
					{
						Org:   org,
						Repos: []string{repo},
					},
				}}},
				configDataProvider: &ConfigDataProvider{updatedPresubmits: tc.configData},
				ghc:                ghc,
			}

			entry := logrus.NewEntry(logrus.New())
			cw.handleLabelAddition(entry, tc.event)

			if tc.expectCommentCall {
				if ghc.comment == "" {
					t.Errorf("expected CreateComment to be called, but no comment was recorded")
				} else {
					if !strings.Contains(ghc.comment, tc.expectedComment) {
						t.Errorf("expected comment to contain %q, got %q", tc.expectedComment, ghc.comment)
					}
				}
			} else {
				if ghc.comment != "" {
					t.Errorf("expected CreateComment not to be called, but got comment %q", ghc.comment)
				}
			}
		})
	}
}
