package main

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	v1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/kube"
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

func (f *fakeGhClientWithChanges) AddLabel(org, repo string, number int, label string) error {
	return nil
}

func (f *fakeGhClientWithChanges) GetIssueLabels(org, repo string, number int) ([]github.Label, error) {
	return []github.Label{}, nil
}

type fakeProwJobLister struct {
	prowJobs []v1.ProwJob
}

func (f *fakeProwJobLister) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.GetOption) error {
	return nil
}

func (f *fakeProwJobLister) List(ctx context.Context, list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) error {
	pjList := list.(*v1.ProwJobList)
	pjList.Items = f.prowJobs
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
		existingProwJobs              []v1.ProwJob
		expectedTestCommands          []string
		expectedManualControlMessage  string
		expectedError                 string
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
			existingProwJobs:             []v1.ProwJob{},
			expectedTestCommands:         []string{"/test test"},
			expectedManualControlMessage: "",
			expectedError:                "",
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
			existingProwJobs:             []v1.ProwJob{},
			expectedTestCommands:         []string{},
			expectedManualControlMessage: "",
			expectedError:                "",
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
			existingProwJobs:             []v1.ProwJob{},
			expectedTestCommands:         []string{},
			expectedManualControlMessage: "",
			expectedError:                "",
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
			existingProwJobs:             []v1.ProwJob{},
			expectedTestCommands:         []string{"/test test"},
			expectedManualControlMessage: "",
			expectedError:                "",
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
			existingProwJobs:             []v1.ProwJob{},
			expectedTestCommands:         []string{"/test test"},
			expectedManualControlMessage: "",
			expectedError:                "",
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
			existingProwJobs:             []v1.ProwJob{},
			expectedTestCommands:         []string{"/test test1", "/test test2"},
			expectedManualControlMessage: "",
			expectedError:                "",
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
			existingProwJobs:             []v1.ProwJob{},
			expectedTestCommands:         []string{},
			expectedManualControlMessage: "",
			expectedError:                "",
		},
		{
			name: "test already running - should return manual control message",
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
			},
			existingProwJobs: []v1.ProwJob{
				{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							kube.OrgLabel:         "org",
							kube.RepoLabel:        "repo",
							kube.PullLabel:        "123",
							kube.BaseRefLabel:     "master",
							kube.ProwJobTypeLabel: string(v1.PresubmitJob),
						},
					},
					Spec: v1.ProwJobSpec{
						Job: "org-repo-master-test",
						Refs: &v1.Refs{
							Org:     "org",
							Repo:    "repo",
							BaseRef: "master",
							Pulls: []v1.Pull{
								{Number: 123, SHA: "abc"},
							},
						},
					},
				},
			},
			expectedTestCommands:         []string{},
			expectedManualControlMessage: "Tests from second stage were triggered manually. Pipeline can be controlled only manually, until HEAD changes. Use command to trigger second stage.",
			expectedError:                "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fakeGhClientWithChanges{changes: tc.changes}
			var pjLister ctrlruntimeclient.Reader
			if len(tc.existingProwJobs) > 0 {
				pjLister = &fakeProwJobLister{prowJobs: tc.existingProwJobs}
			}
			testCmds, manualControlMessage, err := acquireConditionalContexts(context.Background(), basePJ, tc.pipelineConditionallyRequired, ghc, func() {}, pjLister, false)

			// Check expected error
			if tc.expectedError != "" {
				if err == nil {
					t.Errorf("expected error %q, got nil", tc.expectedError)
				} else if err.Error() != tc.expectedError {
					t.Errorf("expected error %q, got %q", tc.expectedError, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Check manual control message
			if tc.expectedManualControlMessage != "" {
				if manualControlMessage != tc.expectedManualControlMessage {
					t.Errorf("expected manual control message %q, got %q", tc.expectedManualControlMessage, manualControlMessage)
				}
				return
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
				protected: []config.Presubmit{
					{
						JobBase: config.JobBase{
							Name: "org-repo-master-protected",
						},
						Reporter: config.Reporter{
							Context: "protected",
						},
						RerunCommand: "/test protected",
					},
				},
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
				"Scheduling required tests:",
				"/test protected",
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

			err := sendCommentWithMode(tc.presubmits, basePJ, ghc, func() {}, nil, false)
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
