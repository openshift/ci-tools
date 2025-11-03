package main

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/labels"
)

// testLoggerMain creates a discarded logger for tests
func testLoggerMain() *logrus.Entry {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return logrus.NewEntry(logger)
}

// Test constants for expected comment messages
const (
	testPipelineRequiredResponse = "Scheduling required tests:"
)

// fakeGhClient is a fake GitHub client for testing
type fakeGhClientWithComment struct {
	comment string
	error   error
	pr      *github.PullRequest
}

func (f *fakeGhClientWithComment) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	if f.error != nil {
		return nil, f.error
	}
	if f.pr != nil {
		return f.pr, nil
	}
	return &github.PullRequest{State: github.PullRequestStateOpen}, nil
}
func (f *fakeGhClientWithComment) CreateComment(owner, repo string, number int, comment string) error {
	f.comment = comment
	return nil
}
func (f *fakeGhClientWithComment) GetPullRequestChanges(org, repo string, number int) ([]github.PullRequestChange, error) {
	return []github.PullRequestChange{}, nil
}

func (f *fakeGhClientWithComment) CreateStatus(org, repo, ref string, s github.Status) error {
	return nil
}

func (f *fakeGhClientWithComment) ListStatuses(org, repo, ref string) ([]github.Status, error) {
	return []github.Status{}, nil
}

// Helper function to create repo config for tests
func createRepoConfig(name string, trigger string) RepoItem {
	item := RepoItem{
		Name: name,
	}
	item.Mode.Trigger = trigger
	return item
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
					protected: func() []config.Presubmit {
						p := config.Presubmit{
							JobBase:      config.JobBase{Name: "dummy-test"},
							RerunCommand: "/test dummy-test",
						}
						p.Context = "dummy-test"
						return []config.Presubmit{p}
					}(),
				},
			},
			expectCommentCall: true,
			expectedComment:   "dummy-test",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fakeGhClientWithComment{}

			cw := &clientWrapper{
				lgtmWatcher: &watcher{config: enabledConfig{Orgs: []struct {
					Org   string     `yaml:"org"`
					Repos []RepoItem `yaml:"repos"`
				}{
					{
						Org: org,
						Repos: []RepoItem{
							createRepoConfig(repo, "auto"),
						},
					},
				}}},
				configDataProvider: &ConfigDataProvider{
					updatedPresubmits: tc.configData,
					previousRepoList:  []string{},
					logger:            testLoggerMain(),
				},
				ghc: ghc,
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

func TestHandleIssueComment(t *testing.T) {
	org := "openshift"
	repo := "test-repo"
	prNumber := 123

	basicEvent := github.IssueCommentEvent{
		Action: github.IssueCommentActionCreated,
		Comment: github.IssueComment{
			Body: "/pipeline required",
		},
		Repo: github.Repo{
			Owner: github.User{Login: org},
			Name:  repo,
		},
		Issue: github.Issue{
			Number:      prNumber,
			PullRequest: &struct{}{}, // Non-nil means it's a PR
		},
	}

	tests := []struct {
		name              string
		event             github.IssueCommentEvent
		configData        map[string]presubmitTests
		watcherConfig     enabledConfig
		ghPR              *github.PullRequest
		ghError           error
		expectCommentCall bool
		expectedComment   string
	}{
		{
			name: "not a PR: do nothing",
			event: github.IssueCommentEvent{
				Issue: github.Issue{
					PullRequest: nil,
				},
			},
			expectCommentCall: false,
		},
		{
			name: "comment doesn't contain /pipeline required: do nothing",
			event: github.IssueCommentEvent{
				Comment: github.IssueComment{
					Body: "This is just a regular comment",
				},
				Issue: github.Issue{
					PullRequest: &struct{}{},
				},
			},
			expectCommentCall: false,
		},
		{
			name: "whitespace variation: double space",
			event: github.IssueCommentEvent{
				Action: github.IssueCommentActionCreated,
				Comment: github.IssueComment{
					Body: "/pipeline  required",
				},
				Repo: github.Repo{
					Owner: github.User{Login: org},
					Name:  repo,
				},
				Issue: github.Issue{
					Number:      prNumber,
					PullRequest: &struct{}{},
				},
			},
			configData: map[string]presubmitTests{
				org + "/" + repo: {
					protected: []config.Presubmit{{JobBase: config.JobBase{Name: "protected-test"}}},
				},
			},
			ghPR: &github.PullRequest{
				Base: github.PullRequestBranch{Ref: "master"},
				Head: github.PullRequestBranch{SHA: "abc123"},
			},
			expectCommentCall: true,
			expectedComment:   testPipelineRequiredResponse,
		},
		{
			name: "whitespace variation: tab",
			event: github.IssueCommentEvent{
				Action: github.IssueCommentActionCreated,
				Comment: github.IssueComment{
					Body: "/pipeline\trequired",
				},
				Repo: github.Repo{
					Owner: github.User{Login: org},
					Name:  repo,
				},
				Issue: github.Issue{
					Number:      prNumber,
					PullRequest: &struct{}{},
				},
			},
			configData: map[string]presubmitTests{
				org + "/" + repo: {
					protected: []config.Presubmit{{JobBase: config.JobBase{Name: "protected-test"}}},
				},
			},
			ghPR: &github.PullRequest{
				Base: github.PullRequestBranch{Ref: "master"},
				Head: github.PullRequestBranch{SHA: "abc123"},
			},
			expectCommentCall: true,
			expectedComment:   testPipelineRequiredResponse,
		},
		{
			name: "case insensitive: uppercase",
			event: github.IssueCommentEvent{
				Action: github.IssueCommentActionCreated,
				Comment: github.IssueComment{
					Body: "/PIPELINE REQUIRED",
				},
				Repo: github.Repo{
					Owner: github.User{Login: org},
					Name:  repo,
				},
				Issue: github.Issue{
					Number:      prNumber,
					PullRequest: &struct{}{},
				},
			},
			configData: map[string]presubmitTests{
				org + "/" + repo: {
					protected: []config.Presubmit{{JobBase: config.JobBase{Name: "protected-test"}}},
				},
			},
			ghPR: &github.PullRequest{
				Base: github.PullRequestBranch{Ref: "master"},
				Head: github.PullRequestBranch{SHA: "abc123"},
			},
			expectCommentCall: true,
			expectedComment:   testPipelineRequiredResponse,
		},
		{
			name:  "no pipeline-controlled jobs: do nothing",
			event: basicEvent,
			configData: map[string]presubmitTests{
				org + "/" + repo: {},
			},
			expectCommentCall: false,
		},
		{
			name:  "has pipeline jobs, responds with test and override commands",
			event: basicEvent,
			configData: map[string]presubmitTests{
				org + "/" + repo: {
					protected: []config.Presubmit{{JobBase: config.JobBase{Name: "protected-test"}}},
					pipelineConditionallyRequired: []config.Presubmit{
						{
							JobBase: config.JobBase{
								Name: repo + "-master-test",
								Annotations: map[string]string{
									"pipeline_run_if_changed": ".*\\.go",
								},
							},
							RerunCommand: "/test test",
							Reporter: config.Reporter{
								Context: "test",
							},
						},
					},
				},
			},
			ghPR: &github.PullRequest{
				Base: github.PullRequestBranch{Ref: "master"},
				Head: github.PullRequestBranch{SHA: "abc123"},
			},
			expectCommentCall: true,
			expectedComment:   testPipelineRequiredResponse,
		},
		{
			name:  "error getting PR: do nothing",
			event: basicEvent,
			configData: map[string]presubmitTests{
				org + "/" + repo: {
					protected: []config.Presubmit{{JobBase: config.JobBase{Name: "protected-test"}}},
				},
			},
			ghError:           errors.New("failed to get PR"),
			expectCommentCall: false,
		},
		{
			name:  "repo not configured: responds with not configured message",
			event: basicEvent,
			configData: map[string]presubmitTests{
				org + "/" + repo: {
					protected: []config.Presubmit{{JobBase: config.JobBase{Name: "protected-test"}}},
				},
			},
			watcherConfig:     enabledConfig{}, // Empty config means repo not configured
			expectCommentCall: true,
			expectedComment:   RepoNotConfiguredMessage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fakeGhClientWithComment{}
			if tc.ghError != nil {
				ghc = &fakeGhClientWithComment{error: tc.ghError}
			} else if tc.ghPR != nil {
				ghc = &fakeGhClientWithComment{pr: tc.ghPR}
			}

			cw := &clientWrapper{
				configDataProvider: &ConfigDataProvider{
					updatedPresubmits: tc.configData,
					previousRepoList:  []string{},
					logger:            testLoggerMain(),
				},
				ghc:         ghc,
				watcher:     &watcher{config: tc.watcherConfig},
				lgtmWatcher: &watcher{config: enabledConfig{}},
			}

			// If no watcherConfig is provided, use a default config with the repo configured
			if len(tc.watcherConfig.Orgs) == 0 && tc.expectedComment != RepoNotConfiguredMessage {
				cw.watcher.config = enabledConfig{Orgs: []struct {
					Org   string     `yaml:"org"`
					Repos []RepoItem `yaml:"repos"`
				}{
					{
						Org: org,
						Repos: []RepoItem{
							createRepoConfig(repo, "auto"),
						},
					},
				}}
			}

			entry := logrus.NewEntry(logrus.New())
			cw.handleIssueComment(entry, tc.event)

			if tc.expectCommentCall {
				if ghc.comment == "" {
					t.Errorf("expected CreateComment to be called, but no comment was recorded")
				} else {
					if !strings.Contains(ghc.comment, tc.expectedComment) {
						t.Errorf("expected comment to contain %q, got %q", tc.expectedComment, ghc.comment)
					}
					// Check for protected job listing format if not the "not configured" message
					if tc.expectedComment != RepoNotConfiguredMessage &&
						!strings.Contains(ghc.comment, "Scheduling required tests:") {
						t.Errorf("expected comment to contain 'Scheduling required tests:', got %q", ghc.comment)
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

func TestHandlePullRequestCreation(t *testing.T) {
	org := "openshift"
	repo := "test-repo"
	prNumber := 123

	basicEvent := github.PullRequestEvent{
		Action: github.PullRequestActionOpened,
		Repo: github.Repo{
			Owner: github.User{Login: org},
			Name:  repo,
		},
		PullRequest: github.PullRequest{
			Number: prNumber,
		},
	}

	tests := []struct {
		name              string
		event             github.PullRequestEvent
		watcherConfig     enabledConfig
		configData        map[string]presubmitTests
		presubmits        []config.Presubmit
		configGetter      config.Getter
		expectCommentCall bool
		expectedComment   string
	}{
		{
			name: "action not opened: do nothing",
			event: github.PullRequestEvent{
				Action: "closed",
			},
			expectCommentCall: false,
		},
		{
			name:  "automatic pipeline repo with jobs: shows pipeline info",
			event: basicEvent,
			watcherConfig: enabledConfig{Orgs: []struct {
				Org   string     `yaml:"org"`
				Repos []RepoItem `yaml:"repos"`
			}{
				{
					Org: org,
					Repos: []RepoItem{
						createRepoConfig(repo, "auto"),
					},
				},
			}},
			configData: map[string]presubmitTests{
				org + "/" + repo: {
					protected: []config.Presubmit{{JobBase: config.JobBase{Name: "protected-test"}}},
				},
			},
			expectCommentCall: true,
			expectedComment:   pullRequestInfoComment,
		},
		{
			name:  "automatic pipeline repo without jobs: no comment",
			event: basicEvent,
			watcherConfig: enabledConfig{Orgs: []struct {
				Org   string     `yaml:"org"`
				Repos []RepoItem `yaml:"repos"`
			}{
				{
					Org: org,
					Repos: []RepoItem{
						createRepoConfig(repo, "auto"),
					},
				},
			}},
			configData: map[string]presubmitTests{
				org + "/" + repo: {},
			},
			expectCommentCall: false,
		},
		{
			name:          "non-configured repo: no comment regardless of jobs",
			event:         basicEvent,
			watcherConfig: enabledConfig{}, // Empty config means not configured
			configGetter: func() *config.Config {
				return &config.Config{
					JobConfig: config.JobConfig{PresubmitsStatic: map[string][]config.Presubmit{
						org + "/" + repo: {
							{
								JobBase:   config.JobBase{Name: "manual-test"},
								AlwaysRun: false,
							},
						},
					}},
				}
			},
			expectCommentCall: false,
		},
		{
			name:  "manual pipeline repo with jobs: no comment for manual mode",
			event: basicEvent,
			watcherConfig: enabledConfig{Orgs: []struct {
				Org   string     `yaml:"org"`
				Repos []RepoItem `yaml:"repos"`
			}{
				{
					Org: org,
					Repos: []RepoItem{
						createRepoConfig(repo, "manual"),
					},
				},
			}},
			configData: map[string]presubmitTests{
				org + "/" + repo: {
					protected: []config.Presubmit{{JobBase: config.JobBase{Name: "protected-test"}}},
				},
			},
			expectCommentCall: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fakeGhClientWithComment{}

			configGetter := tc.configGetter
			if configGetter == nil {
				configGetter = func() *config.Config {
					return &config.Config{}
				}
			}

			cw := &clientWrapper{
				watcher:     &watcher{config: tc.watcherConfig},
				lgtmWatcher: &watcher{config: enabledConfig{}},
				configDataProvider: &ConfigDataProvider{
					updatedPresubmits: tc.configData,
					configGetter:      configGetter,
					previousRepoList:  []string{},
					logger:            testLoggerMain(),
				},
				ghc: ghc,
			}

			entry := logrus.NewEntry(logrus.New())
			cw.handlePullRequestCreation(entry, tc.event)

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
