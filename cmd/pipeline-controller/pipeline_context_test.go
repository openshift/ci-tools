package main

import (
	"fmt"
	"io"
	"testing"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
)

// testLoggerPipeline creates a discarded logger for tests
func testLoggerPipeline() *logrus.Entry {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return logrus.NewEntry(logger)
}

func TestMatchesPattern(t *testing.T) {
	tests := []struct {
		name      string
		pattern   string
		filenames []string
		expected  bool
		expectErr bool
	}{
		{
			name:      "empty pattern returns false",
			pattern:   "",
			filenames: []string{"file.go"},
			expected:  false,
			expectErr: false,
		},
		{
			name:      "simple go file pattern matches",
			pattern:   ".*\\.go$",
			filenames: []string{"main.go", "test.txt"},
			expected:  true,
			expectErr: false,
		},
		{
			name:      "go file pattern doesn't match txt files",
			pattern:   ".*\\.go$",
			filenames: []string{"readme.txt", "docs.md"},
			expected:  false,
			expectErr: false,
		},
		{
			name:      "docs pattern matches docs directory",
			pattern:   "^docs/.*",
			filenames: []string{"docs/readme.md", "src/main.go"},
			expected:  true,
			expectErr: false,
		},
		{
			name:      "invalid regex pattern returns error",
			pattern:   "[",
			filenames: []string{"file.go"},
			expected:  false,
			expectErr: true,
		},
		{
			name:      "empty filenames list returns false",
			pattern:   ".*\\.go$",
			filenames: []string{},
			expected:  false,
			expectErr: false,
		},
		{
			name:      "multiple patterns - first matches",
			pattern:   "(cmd|pkg)/.*\\.go$",
			filenames: []string{"cmd/main.go", "docs/readme.md"},
			expected:  true,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := matchesPattern(tt.pattern, tt.filenames)

			if tt.expectErr && err == nil {
				t.Errorf("expected error but got none")
				return
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if result != tt.expected {
				t.Errorf("matchesPattern() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

type fakeGhClientForContext struct {
	createdStatuses   []github.Status
	changedFiles      []github.PullRequestChange
	pullReqChangesErr error
}

func (f *fakeGhClientForContext) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	return &github.PullRequest{State: github.PullRequestStateOpen}, nil
}

func (f *fakeGhClientForContext) CreateComment(org, repo string, number int, comment string) error {
	return nil
}

func (f *fakeGhClientForContext) GetPullRequestChanges(org string, repo string, number int) ([]github.PullRequestChange, error) {
	if f.pullReqChangesErr != nil {
		return nil, f.pullReqChangesErr
	}
	return f.changedFiles, nil
}

func (f *fakeGhClientForContext) CreateStatus(org, repo, ref string, s github.Status) error {
	f.createdStatuses = append(f.createdStatuses, s)
	return nil
}

func (f *fakeGhClientForContext) ListStatuses(org, repo, ref string) ([]github.Status, error) {
	return []github.Status{}, nil
}

func TestHandlePipelineContextCreation(t *testing.T) {
	tests := []struct {
		name             string
		action           github.PullRequestEventAction
		presubmits       presubmitTests
		changedFiles     []github.PullRequestChange
		expectedContexts int
	}{
		{
			name:   "PR opened with matching pipeline_run_if_changed",
			action: github.PullRequestActionOpened,
			presubmits: presubmitTests{
				pipelineConditionallyRequired: func() []config.Presubmit {
					p := config.Presubmit{
						JobBase: config.JobBase{
							Name: "pull-ci-test-org-test-repo-main-test-go",
							Annotations: map[string]string{
								"pipeline_run_if_changed": ".*\\.go$",
							},
						},
					}
					p.Context = "ci/prow/test-go"
					return []config.Presubmit{p}
				}(),
			},
			changedFiles: []github.PullRequestChange{
				{Filename: "main.go"},
				{Filename: "readme.md"},
			},
			expectedContexts: 1,
		},
		{
			name:   "PR synchronized with non-matching pipeline_run_if_changed",
			action: github.PullRequestActionSynchronize,
			presubmits: presubmitTests{
				pipelineConditionallyRequired: func() []config.Presubmit {
					p := config.Presubmit{
						JobBase: config.JobBase{
							Name: "pull-ci-test-org-test-repo-main-test-go",
							Annotations: map[string]string{
								"pipeline_run_if_changed": ".*\\.go$",
							},
						},
					}
					p.Context = "ci/prow/test-go"
					return []config.Presubmit{p}
				}(),
			},
			changedFiles: []github.PullRequestChange{
				{Filename: "readme.md"},
				{Filename: "docs.txt"},
			},
			expectedContexts: 0,
		},
		{
			name:   "PR reopened with pipeline_skip_only_if_changed - should run",
			action: github.PullRequestActionReopened,
			presubmits: presubmitTests{
				pipelineSkipOnlyRequired: func() []config.Presubmit {
					p := config.Presubmit{
						JobBase: config.JobBase{
							Name: "pull-ci-test-org-test-repo-main-test-important",
							Annotations: map[string]string{
								"pipeline_skip_if_only_changed": ".*\\.md$",
							},
						},
					}
					p.Context = "ci/prow/test-important"
					return []config.Presubmit{p}
				}(),
			},
			changedFiles: []github.PullRequestChange{
				{Filename: "main.go"},
				{Filename: "readme.md"},
			},
			expectedContexts: 1, // Should run because non-md files changed
		},
		{
			name:   "PR opened with pipeline_skip_only_if_changed - should skip",
			action: github.PullRequestActionOpened,
			presubmits: presubmitTests{
				pipelineSkipOnlyRequired: func() []config.Presubmit {
					p := config.Presubmit{
						JobBase: config.JobBase{
							Name: "pull-ci-test-org-test-repo-main-test-important",
							Annotations: map[string]string{
								"pipeline_skip_if_only_changed": ".*\\.md$",
							},
						},
					}
					p.Context = "ci/prow/test-important"
					return []config.Presubmit{p}
				}(),
			},
			changedFiles: []github.PullRequestChange{
				{Filename: "readme.md"},
				{Filename: "docs.md"},
			},
			expectedContexts: 0, // Should skip because only md files changed
		},
		{
			name:   "wrong action - should do nothing",
			action: github.PullRequestActionClosed,
			presubmits: presubmitTests{
				pipelineConditionallyRequired: []config.Presubmit{
					{
						JobBase: config.JobBase{
							Name: "pull-ci-test-org-test-repo-main-test-go",
							Annotations: map[string]string{
								"pipeline_run_if_changed": ".*",
							},
						},
					},
				},
			},
			changedFiles: []github.PullRequestChange{
				{Filename: "main.go"},
			},
			expectedContexts: 0,
		},
		{
			name:   "PR opened with protected jobs - always create contexts",
			action: github.PullRequestActionOpened,
			presubmits: presubmitTests{
				protected: func() []config.Presubmit {
					p1 := config.Presubmit{JobBase: config.JobBase{Name: "pull-ci-test-org-test-repo-main-protected-1"}}
					p1.Context = "ci/prow/protected-1"
					p2 := config.Presubmit{JobBase: config.JobBase{Name: "pull-ci-test-org-test-repo-main-protected-2"}}
					p2.Context = "ci/prow/protected-2"
					return []config.Presubmit{p1, p2}
				}(),
			},
			changedFiles: []github.PullRequestChange{
				{Filename: "main.go"},
			},
			expectedContexts: 2,
		},
		{
			name:   "PR opened with mixed job types",
			action: github.PullRequestActionOpened,
			presubmits: presubmitTests{
				protected: func() []config.Presubmit {
					p := config.Presubmit{JobBase: config.JobBase{Name: "pull-ci-test-org-test-repo-main-protected"}}
					p.Context = "ci/prow/protected"
					return []config.Presubmit{p}
				}(),
				pipelineConditionallyRequired: func() []config.Presubmit {
					p := config.Presubmit{
						JobBase: config.JobBase{
							Name: "pull-ci-test-org-test-repo-main-pipeline",
							Annotations: map[string]string{
								"pipeline_run_if_changed": ".*\\.go$",
							},
						},
					}
					p.Context = "ci/prow/pipeline"
					return []config.Presubmit{p}
				}(),
				pipelineSkipOnlyRequired: func() []config.Presubmit {
					p := config.Presubmit{
						JobBase: config.JobBase{
							Name: "pull-ci-test-org-test-repo-main-skip-docs",
							Annotations: map[string]string{
								"pipeline_skip_if_only_changed": ".*\\.md$",
							},
						},
					}
					p.Context = "ci/prow/skip-docs"
					return []config.Presubmit{p}
				}(),
			},
			changedFiles: []github.PullRequestChange{
				{Filename: "main.go"},
				{Filename: "readme.md"},
			},
			expectedContexts: 3, // protected + pipeline (matches go) + skip (mixed files)
		},
		{
			name:   "PR synchronized with only protected jobs",
			action: github.PullRequestActionSynchronize,
			presubmits: presubmitTests{
				protected: func() []config.Presubmit {
					p := config.Presubmit{JobBase: config.JobBase{Name: "pull-ci-test-org-test-repo-main-always-required"}}
					p.Context = "ci/prow/always-required"
					return []config.Presubmit{p}
				}(),
			},
			changedFiles: []github.PullRequestChange{
				{Filename: "config.yaml"},
			},
			expectedContexts: 1,
		},
		{
			name:   "PR opened with job from different branch - should not create context",
			action: github.PullRequestActionOpened,
			presubmits: presubmitTests{
				protected: func() []config.Presubmit {
					// This job is for release-4.16 branch, but PR is targeting main
					p := config.Presubmit{JobBase: config.JobBase{Name: "pull-ci-test-org-test-repo-release-4.16-e2e-aws"}}
					p.Context = "ci/prow/e2e-aws"
					return []config.Presubmit{p}
				}(),
			},
			changedFiles: []github.PullRequestChange{
				{Filename: "main.go"},
			},
			expectedContexts: 0, // Job doesn't match branch
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := &fakeGhClientForContext{
				changedFiles: tt.changedFiles,
			}

			configProvider := &ConfigDataProvider{
				updatedPresubmits: map[string]presubmitTests{
					"test-org/test-repo": tt.presubmits,
				},
				previousRepoList: []string{},
				logger:           testLoggerPipeline(),
			}

			watcherVar := &watcher{
				config: enabledConfig{
					Orgs: []struct {
						Org   string     `yaml:"org"`
						Repos []RepoItem `yaml:"repos"`
					}{
						{
							Org:   "test-org",
							Repos: []RepoItem{{Name: "test-repo"}},
						},
					},
				},
			}

			lgtmWatcher := &watcher{config: enabledConfig{}}
			cw := &clientWrapper{
				ghc:                fakeClient,
				configDataProvider: configProvider,
				watcher:            watcherVar,
				lgtmWatcher:        lgtmWatcher,
			}

			event := github.PullRequestEvent{
				Action: tt.action,
				Repo: github.Repo{
					Owner: github.User{Login: "test-org"},
					Name:  "test-repo",
				},
				PullRequest: github.PullRequest{
					Number: 123,
					Head:   github.PullRequestBranch{SHA: "abc123"},
					Base:   github.PullRequestBranch{Ref: "main"},
				},
			}

			logger := logrus.NewEntry(logrus.New())
			cw.handlePipelineContextCreation(logger, event)

			if len(fakeClient.createdStatuses) != tt.expectedContexts {
				t.Errorf("expected %d contexts created, got %d", tt.expectedContexts, len(fakeClient.createdStatuses))
			}

			// Verify that created contexts have correct state
			for _, status := range fakeClient.createdStatuses {
				if status.State != "pending" {
					t.Errorf("expected status state 'pending', got '%s'", status.State)
				}
				if status.Description != PipelinePendingMessage {
					t.Errorf("unexpected status description: %s", status.Description)
				}
			}
		})
	}
}

func TestAllFilesMatchPattern(t *testing.T) {
	tests := []struct {
		name      string
		pattern   string
		filenames []string
		expected  bool
		expectErr bool
	}{
		{
			name:      "empty pattern returns false",
			pattern:   "",
			filenames: []string{"file.go"},
			expected:  false,
			expectErr: false,
		},
		{
			name:      "all files match pattern",
			pattern:   ".*\\.md$",
			filenames: []string{"readme.md", "docs.md"},
			expected:  true,
			expectErr: false,
		},
		{
			name:      "not all files match pattern",
			pattern:   ".*\\.md$",
			filenames: []string{"readme.md", "main.go"},
			expected:  false,
			expectErr: false,
		},
		{
			name:      "empty filenames list returns false",
			pattern:   ".*\\.md$",
			filenames: []string{},
			expected:  false,
			expectErr: false,
		},
		{
			name:      "invalid regex pattern returns error",
			pattern:   "[",
			filenames: []string{"file.md"},
			expected:  false,
			expectErr: true,
		},
		{
			name:      "single file matches",
			pattern:   "^docs/.*\\.md$",
			filenames: []string{"docs/readme.md"},
			expected:  true,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := allFilesMatchPattern(tt.pattern, tt.filenames)

			if tt.expectErr && err == nil {
				t.Errorf("expected error but got none")
				return
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if result != tt.expected {
				t.Errorf("allFilesMatchPattern() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestHandlePipelineContextCreationEdgeCases(t *testing.T) {
	tests := []struct {
		name                string
		action              github.PullRequestEventAction
		presubmits          presubmitTests
		changedFiles        []github.PullRequestChange
		pullReqChangesErr   error
		expectedContexts    int
		expectedNames       []string
		shouldSkipExecution bool
	}{
		{
			name:   "no presubmits at all - should do nothing",
			action: github.PullRequestActionOpened,
			presubmits: presubmitTests{
				protected:                     []config.Presubmit{},
				pipelineConditionallyRequired: []config.Presubmit{},
				pipelineSkipOnlyRequired:      []config.Presubmit{},
			},
			changedFiles:        []github.PullRequestChange{{Filename: "main.go"}},
			expectedContexts:    0,
			shouldSkipExecution: true,
		},
		{
			name:   "empty changed files list",
			action: github.PullRequestActionOpened,
			presubmits: presubmitTests{
				pipelineConditionallyRequired: func() []config.Presubmit {
					p := config.Presubmit{
						JobBase: config.JobBase{
							Name: "pull-ci-test-org-test-repo-main-test-go",
							Annotations: map[string]string{
								"pipeline_run_if_changed": ".*\\.go$",
							},
						},
					}
					p.Context = "ci/prow/test-go"
					return []config.Presubmit{p}
				}(),
			},
			changedFiles:     []github.PullRequestChange{},
			expectedContexts: 0,
		},
		{
			name:   "GitHub API error getting changed files",
			action: github.PullRequestActionOpened,
			presubmits: presubmitTests{
				protected: func() []config.Presubmit {
					p := config.Presubmit{JobBase: config.JobBase{Name: "pull-ci-test-org-test-repo-main-protected"}}
					p.Context = "ci/prow/protected"
					return []config.Presubmit{p}
				}(),
			},
			changedFiles:        []github.PullRequestChange{},
			pullReqChangesErr:   fmt.Errorf("GitHub API error"),
			expectedContexts:    0,
			shouldSkipExecution: true,
		},
		{
			name:   "invalid regex pattern in pipeline_run_if_changed",
			action: github.PullRequestActionOpened,
			presubmits: presubmitTests{
				pipelineConditionallyRequired: func() []config.Presubmit {
					p := config.Presubmit{
						JobBase: config.JobBase{
							Name: "pull-ci-test-org-test-repo-main-invalid-pattern",
							Annotations: map[string]string{
								"pipeline_run_if_changed": "[invalid-regex",
							},
						},
					}
					p.Context = "ci/prow/invalid-pattern"
					return []config.Presubmit{p}
				}(),
			},
			changedFiles:     []github.PullRequestChange{{Filename: "main.go"}},
			expectedContexts: 0, // Should not create context due to invalid pattern
		},
		{
			name:   "invalid regex pattern in pipeline_skip_only_if_changed",
			action: github.PullRequestActionOpened,
			presubmits: presubmitTests{
				pipelineSkipOnlyRequired: func() []config.Presubmit {
					p := config.Presubmit{
						JobBase: config.JobBase{
							Name: "pull-ci-test-org-test-repo-main-invalid-skip-pattern",
							Annotations: map[string]string{
								"pipeline_skip_if_only_changed": "[invalid-regex",
							},
						},
					}
					p.Context = "ci/prow/invalid-skip-pattern"
					return []config.Presubmit{p}
				}(),
			},
			changedFiles:     []github.PullRequestChange{{Filename: "main.go"}},
			expectedContexts: 0, // Should not create context due to invalid pattern
		},
		{
			name:   "complex regex patterns",
			action: github.PullRequestActionOpened,
			presubmits: presubmitTests{
				pipelineConditionallyRequired: func() []config.Presubmit {
					p := config.Presubmit{
						JobBase: config.JobBase{
							Name: "pull-ci-test-org-test-repo-main-complex-go-pattern",
							Annotations: map[string]string{
								"pipeline_run_if_changed": "^(cmd|pkg)/.*\\.go$|^go\\.(mod|sum)$",
							},
						},
					}
					p.Context = "ci/prow/complex-go-pattern"
					return []config.Presubmit{p}
				}(),
			},
			changedFiles: []github.PullRequestChange{
				{Filename: "cmd/main.go"},
				{Filename: "docs/readme.md"},
			},
			expectedContexts: 1,
			expectedNames:    []string{"ci/prow/complex-go-pattern"},
		},
		{
			name:   "protected jobs with specific names verification",
			action: github.PullRequestActionOpened,
			presubmits: presubmitTests{
				protected: func() []config.Presubmit {
					p1 := config.Presubmit{JobBase: config.JobBase{Name: "pull-ci-test-org-test-repo-main-test-1"}}
					p1.Context = "ci/prow/test-1"
					p2 := config.Presubmit{JobBase: config.JobBase{Name: "pull-ci-test-org-test-repo-main-test-2"}}
					p2.Context = "ci/prow/test-2"
					return []config.Presubmit{p1, p2}
				}(),
			},
			changedFiles: []github.PullRequestChange{
				{Filename: "config.yaml"},
			},
			expectedContexts: 2,
			expectedNames:    []string{"ci/prow/test-1", "ci/prow/test-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := &fakeGhClientForContext{
				changedFiles:      tt.changedFiles,
				pullReqChangesErr: tt.pullReqChangesErr,
			}

			configProvider := &ConfigDataProvider{
				updatedPresubmits: map[string]presubmitTests{
					"test-org/test-repo": tt.presubmits,
				},
				previousRepoList: []string{},
				logger:           testLoggerPipeline(),
			}

			watcherVar := &watcher{
				config: enabledConfig{
					Orgs: []struct {
						Org   string     `yaml:"org"`
						Repos []RepoItem `yaml:"repos"`
					}{
						{
							Org:   "test-org",
							Repos: []RepoItem{{Name: "test-repo"}},
						},
					},
				},
			}

			lgtmWatcher := &watcher{config: enabledConfig{}}
			cw := &clientWrapper{
				ghc:                fakeClient,
				configDataProvider: configProvider,
				watcher:            watcherVar,
				lgtmWatcher:        lgtmWatcher,
			}

			event := github.PullRequestEvent{
				Action: tt.action,
				Repo: github.Repo{
					Owner: github.User{Login: "test-org"},
					Name:  "test-repo",
				},
				PullRequest: github.PullRequest{
					Number: 123,
					Head:   github.PullRequestBranch{SHA: "abc123"},
					Base:   github.PullRequestBranch{Ref: "main"},
				},
			}

			logger := logrus.NewEntry(logrus.New())
			cw.handlePipelineContextCreation(logger, event)

			if tt.shouldSkipExecution {
				// For error cases or early returns, just verify no contexts were created
				if len(fakeClient.createdStatuses) != 0 {
					t.Errorf("expected 0 contexts created due to early return, got %d", len(fakeClient.createdStatuses))
				}
				return
			}

			if len(fakeClient.createdStatuses) != tt.expectedContexts {
				t.Errorf("expected %d contexts created, got %d", tt.expectedContexts, len(fakeClient.createdStatuses))
				for i, status := range fakeClient.createdStatuses {
					t.Logf("Created context %d: %s", i, status.Context)
				}
			}

			// Verify that created contexts have correct state and names
			createdNames := make([]string, len(fakeClient.createdStatuses))
			for i, status := range fakeClient.createdStatuses {
				if status.State != "pending" {
					t.Errorf("expected status state 'pending', got '%s'", status.State)
				}
				if status.Description != PipelinePendingMessage {
					t.Errorf("unexpected status description: %s", status.Description)
				}
				createdNames[i] = status.Context
			}

			// Verify expected context names were created (if specified)
			if len(tt.expectedNames) > 0 {
				for _, expectedName := range tt.expectedNames {
					found := false
					for _, createdName := range createdNames {
						if createdName == expectedName {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected context '%s' was not found. Created contexts: %v", expectedName, createdNames)
					}
				}
			}
		})
	}
}
