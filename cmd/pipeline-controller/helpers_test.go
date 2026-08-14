package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/kube"
)

type fakeGitHubClient struct {
	comments []string
}

func (f *fakeGitHubClient) CreateComment(org, repo string, number int, comment string) error {
	f.comments = append(f.comments, comment)
	return nil
}

func prowJobForPR(name string, sha string) *prowapi.ProwJob {
	const prNumber = 42
	return &prowapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", name, sha[:7]),
			Namespace: "ci",
			Labels: map[string]string{
				kube.OrgLabel:          "openshift",
				kube.RepoLabel:         "installer",
				kube.PullLabel:         fmt.Sprintf("%d", prNumber),
				kube.ProwJobTypeLabel:  string(prowapi.PresubmitJob),
				kube.ProwJobAnnotation: name,
			},
		},
		Spec: prowapi.ProwJobSpec{
			Type: prowapi.PresubmitJob,
			Job:  name,
			Refs: &prowapi.Refs{
				Org:  "openshift",
				Repo: "installer",
				Pulls: []prowapi.Pull{
					{
						Number: prNumber,
						SHA:    sha,
					},
				},
			},
		},
	}
}

func baseProwJob(org, repo string, prNumber int, sha string) *prowapi.ProwJob {
	return &prowapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "trigger-job",
			Namespace: "ci",
		},
		Spec: prowapi.ProwJobSpec{
			Type: prowapi.PresubmitJob,
			Refs: &prowapi.Refs{
				Org:  org,
				Repo: repo,
				Pulls: []prowapi.Pull{
					{
						Number: prNumber,
						SHA:    sha,
					},
				},
			},
		},
	}
}

func newFakeClient(objs ...runtime.Object) ctrlruntimeclient.Client {
	scheme := runtime.NewScheme()
	if err := prowapi.AddToScheme(scheme); err != nil {
		panic(fmt.Sprintf("failed to add prowjob scheme: %v", err))
	}
	return fakectrlruntimeclient.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()
}

func TestSendCommentWithMode_ProtectedDedup(t *testing.T) {
	const (
		org      = "openshift"
		repo     = "installer"
		prNumber = 42
		sha      = "abc1234def5678"
		ns       = "ci"
	)

	protectedJobs := []prowconfig.Presubmit{
		{JobBase: prowconfig.JobBase{Name: "pull-ci-openshift-installer-e2e-aws"}},
		{JobBase: prowconfig.JobBase{Name: "pull-ci-openshift-installer-e2e-gcp"}},
	}
	conditionalJobs := []prowconfig.Presubmit{
		{JobBase: prowconfig.JobBase{Name: "pull-ci-openshift-installer-e2e-azure"}},
	}

	tests := []struct {
		name              string
		existingPJs       []runtime.Object
		isExplicitCommand bool
		wantComments      int
		wantTriggered     []string
		wantSkipped       []string
		wantNoComment     bool
	}{
		{
			name:              "no existing ProwJobs: triggers all protected and conditional tests",
			existingPJs:       nil,
			isExplicitCommand: false,
			wantComments:      1,
			wantTriggered:     []string{"pull-ci-openshift-installer-e2e-aws", "pull-ci-openshift-installer-e2e-gcp", "pull-ci-openshift-installer-e2e-azure"},
		},
		{
			name: "protected ProwJob exists at same SHA: skips it, triggers others",
			existingPJs: []runtime.Object{
				prowJobForPR("pull-ci-openshift-installer-e2e-aws", sha),
			},
			isExplicitCommand: false,
			wantComments:      1,
			wantTriggered:     []string{"pull-ci-openshift-installer-e2e-gcp", "pull-ci-openshift-installer-e2e-azure"},
			wantSkipped:       []string{"pull-ci-openshift-installer-e2e-aws"},
		},
		{
			name: "all ProwJobs exist at same SHA: no comment posted",
			existingPJs: []runtime.Object{
				prowJobForPR("pull-ci-openshift-installer-e2e-aws", sha),
				prowJobForPR("pull-ci-openshift-installer-e2e-gcp", sha),
				prowJobForPR("pull-ci-openshift-installer-e2e-azure", sha),
			},
			isExplicitCommand: false,
			wantNoComment:     true,
		},
		{
			name: "explicit command bypasses dedup for protected tests",
			existingPJs: []runtime.Object{
				prowJobForPR("pull-ci-openshift-installer-e2e-aws", sha),
				prowJobForPR("pull-ci-openshift-installer-e2e-gcp", sha),
			},
			isExplicitCommand: true,
			wantComments:      1,
			// Explicit command triggers all protected tests unconditionally,
			// but conditional tests still get deduped
			wantTriggered: []string{"pull-ci-openshift-installer-e2e-aws", "pull-ci-openshift-installer-e2e-gcp"},
		},
		{
			name: "ProwJob exists at different SHA: triggers it",
			existingPJs: []runtime.Object{
				prowJobForPR("pull-ci-openshift-installer-e2e-aws", "different_sha_123"),
			},
			isExplicitCommand: false,
			wantComments:      1,
			wantTriggered:     []string{"pull-ci-openshift-installer-e2e-aws", "pull-ci-openshift-installer-e2e-gcp", "pull-ci-openshift-installer-e2e-azure"},
		},
		{
			name: "conditional ProwJob exists at same SHA: skips conditional, triggers protected",
			existingPJs: []runtime.Object{
				prowJobForPR("pull-ci-openshift-installer-e2e-azure", sha),
			},
			isExplicitCommand: false,
			wantComments:      1,
			wantTriggered:     []string{"pull-ci-openshift-installer-e2e-aws", "pull-ci-openshift-installer-e2e-gcp"},
			wantSkipped:       []string{"pull-ci-openshift-installer-e2e-azure"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ghc := &fakeGitHubClient{}
			client := newFakeClient(tc.existingPJs...)
			pj := baseProwJob(org, repo, prNumber, sha)
			logger := logrus.NewEntry(logrus.New())

			msg, err := sendCommentWithMode(
				context.Background(),
				logger,
				ghc,
				client,
				pj,
				protectedJobs,
				conditionalJobs,
				tc.isExplicitCommand,
				ns,
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantNoComment {
				if len(ghc.comments) != 0 {
					t.Errorf("expected no comments, got %d: %v", len(ghc.comments), ghc.comments)
				}
				if !strings.Contains(msg, "All pipeline tests already exist") {
					t.Errorf("expected informational message about existing tests, got: %s", msg)
				}
				return
			}

			if len(ghc.comments) != tc.wantComments {
				t.Errorf("expected %d comment(s), got %d", tc.wantComments, len(ghc.comments))
			}

			if len(ghc.comments) > 0 {
				comment := ghc.comments[0]
				for _, job := range tc.wantTriggered {
					testCmd := fmt.Sprintf("/test %s", job)
					if !strings.Contains(comment, testCmd) {
						t.Errorf("expected comment to contain %q, got: %s", testCmd, comment)
					}
				}
				for _, job := range tc.wantSkipped {
					testCmd := fmt.Sprintf("/test %s", job)
					if strings.Contains(comment, testCmd) {
						t.Errorf("expected comment NOT to contain %q (should be deduped), got: %s", testCmd, comment)
					}
				}
			}

			_ = msg // msg is informational
		})
	}
}

func TestProwJobExistsForSHA(t *testing.T) {
	const (
		org      = "openshift"
		repo     = "installer"
		prNumber = 42
		sha      = "abc1234def5678"
		ns       = "ci"
	)

	tests := []struct {
		name        string
		existingPJs []runtime.Object
		jobName     string
		sha         string
		wantExists  bool
	}{
		{
			name:       "no ProwJobs: returns false",
			jobName:    "pull-ci-openshift-installer-e2e-aws",
			sha:        sha,
			wantExists: false,
		},
		{
			name: "matching ProwJob at same SHA: returns true",
			existingPJs: []runtime.Object{
				prowJobForPR("pull-ci-openshift-installer-e2e-aws", sha),
			},
			jobName:    "pull-ci-openshift-installer-e2e-aws",
			sha:        sha,
			wantExists: true,
		},
		{
			name: "matching ProwJob at different SHA: returns false",
			existingPJs: []runtime.Object{
				prowJobForPR("pull-ci-openshift-installer-e2e-aws", "different_sha_123"),
			},
			jobName:    "pull-ci-openshift-installer-e2e-aws",
			sha:        sha,
			wantExists: false,
		},
		{
			name: "different job name: returns false",
			existingPJs: []runtime.Object{
				prowJobForPR("pull-ci-openshift-installer-e2e-gcp", sha),
			},
			jobName:    "pull-ci-openshift-installer-e2e-aws",
			sha:        sha,
			wantExists: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newFakeClient(tc.existingPJs...)

			exists, err := prowJobExistsForSHA(
				context.Background(),
				client,
				tc.jobName,
				org, repo, prNumber,
				tc.sha,
				ns,
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if exists != tc.wantExists {
				t.Errorf("expected exists=%v, got %v", tc.wantExists, exists)
			}
		})
	}
}

func TestAcquireConditionalContexts(t *testing.T) {
	const (
		org      = "openshift"
		repo     = "installer"
		prNumber = 42
		sha      = "abc1234def5678"
		ns       = "ci"
	)

	presubmits := []prowconfig.Presubmit{
		{JobBase: prowconfig.JobBase{Name: "pull-ci-openshift-installer-e2e-azure"}},
		{JobBase: prowconfig.JobBase{Name: "pull-ci-openshift-installer-e2e-vsphere"}},
	}

	tests := []struct {
		name          string
		existingPJs   []runtime.Object
		wantCommands  []string
		wantSkippedIn string
	}{
		{
			name:         "no existing ProwJobs: all commands returned",
			wantCommands: []string{"/test pull-ci-openshift-installer-e2e-azure", "/test pull-ci-openshift-installer-e2e-vsphere"},
		},
		{
			name: "one ProwJob exists at same SHA: only the other is returned",
			existingPJs: []runtime.Object{
				prowJobForPR("pull-ci-openshift-installer-e2e-azure", sha),
			},
			wantCommands:  []string{"/test pull-ci-openshift-installer-e2e-vsphere"},
			wantSkippedIn: "pull-ci-openshift-installer-e2e-azure",
		},
		{
			name: "all ProwJobs exist: no commands returned",
			existingPJs: []runtime.Object{
				prowJobForPR("pull-ci-openshift-installer-e2e-azure", sha),
				prowJobForPR("pull-ci-openshift-installer-e2e-vsphere", sha),
			},
			wantCommands:  nil,
			wantSkippedIn: "pull-ci-openshift-installer-e2e-azure",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newFakeClient(tc.existingPJs...)
			logger := logrus.NewEntry(logrus.New())

			commands, msg := acquireConditionalContexts(
				context.Background(),
				logger,
				client,
				presubmits,
				org, repo, prNumber, sha, ns,
			)

			if len(commands) != len(tc.wantCommands) {
				t.Fatalf("expected %d commands, got %d: %v", len(tc.wantCommands), len(commands), commands)
			}
			for i, cmd := range tc.wantCommands {
				if commands[i] != cmd {
					t.Errorf("command[%d]: expected %q, got %q", i, cmd, commands[i])
				}
			}

			if tc.wantSkippedIn != "" && !strings.Contains(msg, tc.wantSkippedIn) {
				t.Errorf("expected skip message to mention %q, got: %q", tc.wantSkippedIn, msg)
			}
		})
	}
}

func TestSendCommentWithMode_NoRefs(t *testing.T) {
	ghc := &fakeGitHubClient{}
	client := newFakeClient()
	pj := &prowapi.ProwJob{
		ObjectMeta: metav1.ObjectMeta{Name: "no-refs-job"},
		Spec:       prowapi.ProwJobSpec{},
	}
	logger := logrus.NewEntry(logrus.New())

	_, err := sendCommentWithMode(
		context.Background(),
		logger,
		ghc,
		client,
		pj,
		nil, nil,
		false,
		"ci",
	)
	if err == nil {
		t.Fatal("expected error for ProwJob with no refs")
	}
	if !strings.Contains(err.Error(), "no pull request refs") {
		t.Errorf("expected error about missing refs, got: %v", err)
	}
}
