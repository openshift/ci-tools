package main

import (
	"strings"
	"testing"

	v1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
)

type fakeGhClientWithChanges struct {
	changes []github.PullRequestChange
	comment string
}

func (f *fakeGhClientWithChanges) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	return &github.PullRequest{State: github.PullRequestStateOpen}, nil
}

func (f *fakeGhClientWithChanges) CreateComment(owner, repo string, number int, comment string) error {
	f.comment = comment
	return nil
}

func (f *fakeGhClientWithChanges) GetPullRequestChanges(org, repo string, number int) ([]github.PullRequestChange, error) {
	return f.changes, nil
}

func (f *fakeGhClientWithChanges) CreateStatus(org, repo, ref string, s github.Status) error {
	return nil
}

func TestAcquireConditionalContexts(t *testing.T) {
	basePJ := &v1.ProwJob{
		Spec: v1.ProwJobSpec{
			Refs: &v1.Refs{
				Org:     "org",
				Repo:    "repo",
				BaseRef: "master",
				Pulls: []v1.Pull{
					{Number: 123, SHA: "abc"},
				},
			},
		},
	}

	tests := []struct {
		name                          string
		pipelineConditionallyRequired []config.Presubmit
		changes                       []github.PullRequestChange
		expectedTestCommands          []string
	}{
		{
			name: "pipeline_run_if_changed matches files",
			pipelineConditionallyRequired: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "org-repo-master-test",
						Annotations: map[string]string{
							"pipeline_run_if_changed": ".*\\.go",
						},
					},
					Reporter: config.Reporter{
						Context: "test",
					},
					RerunCommand: "/test test",
				},
			},
			changes: []github.PullRequestChange{
				{Filename: "main.go"},
				{Filename: "README.md"},
			},
			expectedTestCommands: []string{"/test test"},
		},
		{
			name: "pipeline_run_if_changed does not match files",
			pipelineConditionallyRequired: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "org-repo-master-test",
						Annotations: map[string]string{
							"pipeline_run_if_changed": ".*\\.go",
						},
					},
					Reporter: config.Reporter{
						Context: "test",
					},
					RerunCommand: "/test test",
					Optional:     false,
				},
			},
			changes: []github.PullRequestChange{
				{Filename: "README.md"},
				{Filename: "docs/guide.md"},
			},
			expectedTestCommands: []string{},
		},
		{
			name: "pipeline_skip_if_only_changed skips when only matching files changed",
			pipelineConditionallyRequired: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "org-repo-master-test",
						Annotations: map[string]string{
							"pipeline_skip_if_only_changed": "^docs/.*|.*\\.md",
						},
					},
					Reporter: config.Reporter{
						Context: "test",
					},
					RerunCommand: "/test test",
					Optional:     false,
				},
			},
			changes: []github.PullRequestChange{
				{Filename: "docs/guide.md"},
				{Filename: "README.md"},
			},
			expectedTestCommands: []string{},
		},
		{
			name: "pipeline_skip_if_only_changed runs when other files are changed",
			pipelineConditionallyRequired: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "org-repo-master-test",
						Annotations: map[string]string{
							"pipeline_skip_if_only_changed": "^docs/.*|.*\\.md",
						},
					},
					Reporter: config.Reporter{
						Context: "test",
					},
					RerunCommand: "/test test",
				},
			},
			changes: []github.PullRequestChange{
				{Filename: "docs/guide.md"},
				{Filename: "main.go"},
			},
			expectedTestCommands: []string{"/test test"},
		},
		{
			name: "pipeline_run_if_changed takes precedence over pipeline_skip_if_only_changed",
			pipelineConditionallyRequired: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "org-repo-master-test",
						Annotations: map[string]string{
							"pipeline_run_if_changed":       ".*\\.go",
							"pipeline_skip_if_only_changed": "^test/.*",
						},
					},
					Reporter: config.Reporter{
						Context: "test",
					},
					RerunCommand: "/test test",
				},
			},
			changes: []github.PullRequestChange{
				{Filename: "test/test.go"},
			},
			expectedTestCommands: []string{"/test test"},
		},
		{
			name: "multiple jobs with different annotations",
			pipelineConditionallyRequired: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "org-repo-master-test1",
						Annotations: map[string]string{
							"pipeline_run_if_changed": ".*\\.go",
						},
					},
					Reporter: config.Reporter{
						Context: "test1",
					},
					RerunCommand: "/test test1",
				},
				{
					JobBase: config.JobBase{
						Name: "org-repo-master-test2",
						Annotations: map[string]string{
							"pipeline_skip_if_only_changed": "^docs/.*",
						},
					},
					Reporter: config.Reporter{
						Context: "test2",
					},
					RerunCommand: "/test test2",
				},
			},
			changes: []github.PullRequestChange{
				{Filename: "main.go"},
				{Filename: "docs/guide.md"},
			},
			expectedTestCommands: []string{"/test test1", "/test test2"},
		},
		{
			name: "job name does not contain repo-baseRef",
			pipelineConditionallyRequired: []config.Presubmit{
				{
					JobBase: config.JobBase{
						Name: "different-job",
						Annotations: map[string]string{
							"pipeline_run_if_changed": ".*",
						},
					},
					Reporter: config.Reporter{
						Context: "different",
					},
					RerunCommand: "/test different",
				},
			},
			changes: []github.PullRequestChange{
				{Filename: "main.go"},
			},
			expectedTestCommands: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fakeGhClientWithChanges{changes: tc.changes}
			testCmds, err := acquireConditionalContexts(basePJ, tc.pipelineConditionallyRequired, ghc, func() {})

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Check test commands
			for _, expected := range tc.expectedTestCommands {
				if !strings.Contains(testCmds, expected) {
					t.Errorf("expected test commands to contain %q, got %q", expected, testCmds)
				}
			}

			// Check that we don't have unexpected commands
			if len(tc.expectedTestCommands) == 0 && testCmds != "" {
				t.Errorf("expected no test commands, got %q", testCmds)
			}
		})
	}
}

func TestSendCommentWithMode(t *testing.T) {
	basePJ := &v1.ProwJob{
		Spec: v1.ProwJobSpec{
			Refs: &v1.Refs{
				Org:     "org",
				Repo:    "repo",
				BaseRef: "master",
				Pulls: []v1.Pull{
					{Number: 123, SHA: "abc"},
				},
			},
		},
	}

	tests := []struct {
		name                       string
		presubmits                 presubmitTests
		changes                    []github.PullRequestChange
		expectedCommentContains    []string
		expectedCommentNotContains []string
	}{
		{
			name: "manual mode with pipeline jobs",
			presubmits: presubmitTests{
				pipelineConditionallyRequired: []config.Presubmit{
					{
						JobBase: config.JobBase{
							Name: "org-repo-master-test",
							Annotations: map[string]string{
								"pipeline_run_if_changed": ".*\\.go",
							},
						},
						Reporter: config.Reporter{
							Context: "test",
						},
						RerunCommand: "/test test",
						Optional:     false,
					},
				},
			},
			changes: []github.PullRequestChange{
				{Filename: "README.md"},
			},
			expectedCommentContains: []string{
				testPipelineRequiredResponse,
			},
		},
		{
			name: "automatic mode with pipeline jobs",
			presubmits: presubmitTests{
				pipelineConditionallyRequired: []config.Presubmit{
					{
						JobBase: config.JobBase{
							Name: "org-repo-master-test",
							Annotations: map[string]string{
								"pipeline_skip_if_only_changed": "^docs/.*",
							},
						},
						Reporter: config.Reporter{
							Context: "test2",
						},
						RerunCommand: "/test test2",
					},
				},
			},
			changes: []github.PullRequestChange{
				{Filename: "main.go"},
			},
			expectedCommentContains: []string{
				"/test test2",
			},
			expectedCommentNotContains: []string{
				"Pipeline controller response",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fakeGhClientWithChanges{changes: tc.changes}

			err := sendCommentWithMode(tc.presubmits, basePJ, ghc, func() {})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			comment := ghc.comment

			for _, expected := range tc.expectedCommentContains {
				if !strings.Contains(comment, expected) {
					t.Errorf("expected comment to contain %q, got:\n%s", expected, comment)
				}
			}

			for _, notExpected := range tc.expectedCommentNotContains {
				if strings.Contains(comment, notExpected) {
					t.Errorf("expected comment NOT to contain %q, got:\n%s", notExpected, comment)
				}
			}
		})
	}
}
