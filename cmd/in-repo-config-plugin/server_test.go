package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/runtime"
	pjapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/github"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
)

type fakeGithubClient struct {
	comments   []comment
	prs        map[string]*github.PullRequest
	dirs       map[string][]github.DirectoryContent
	files      map[string][]byte
	commentErr error
}

type comment struct {
	org, repo string
	number    int
	body      string
}

func (c *fakeGithubClient) CreateComment(owner, repo string, number int, body string) error {
	c.comments = append(c.comments, comment{org: owner, repo: repo, number: number, body: body})
	return c.commentErr
}

func (c *fakeGithubClient) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	key := fmt.Sprintf("%s/%s#%d", org, repo, number)
	pr, ok := c.prs[key]
	if !ok {
		return nil, fmt.Errorf("PR not found: %s", key)
	}
	return pr, nil
}

func (c *fakeGithubClient) GetDirectory(org, repo, dirpath, commit string) ([]github.DirectoryContent, error) {
	key := fmt.Sprintf("%s/%s/%s@%s", org, repo, dirpath, commit)
	entries, ok := c.dirs[key]
	if !ok {
		return nil, &github.FileNotFound{}
	}
	return entries, nil
}

func (c *fakeGithubClient) GetFile(org, repo, path, commit string) ([]byte, error) {
	key := fmt.Sprintf("%s/%s/%s@%s", org, repo, path, commit)
	content, ok := c.files[key]
	if !ok {
		return nil, nil
	}
	return content, nil
}

func makePR(org, repo string, number int, branch, sha string) *github.PullRequest {
	return &github.PullRequest{
		Number: number,
		Base: github.PullRequestBranch{
			Ref:  branch,
			Repo: github.Repo{Owner: github.User{Login: org}, Name: repo},
		},
		Head: github.PullRequestBranch{
			SHA: sha,
		},
	}
}

func makePullRequestEvent(org, repo string, number int, action github.PullRequestEventAction, branch, sha string) github.PullRequestEvent {
	return github.PullRequestEvent{
		Action: action,
		Number: number,
		Repo:   github.Repo{Owner: github.User{Login: org}, Name: repo},
		PullRequest: github.PullRequest{
			Number: number,
			Base: github.PullRequestBranch{
				Ref:  branch,
				Repo: github.Repo{Owner: github.User{Login: org}, Name: repo},
			},
			Head: github.PullRequestBranch{
				SHA: sha,
			},
		},
	}
}

const ciOpConfig = `
build_root:
  image_stream_tag:
    name: release
    namespace: openshift
    tag: golang-1.21
tests:
- as: e2e
  steps:
    test:
    - as: test
      commands: make e2e
      from: src
      resources:
        requests:
          cpu: 100m
`

func TestHandlePullRequest(t *testing.T) {
	org, repo := "testorg", "testrepo"
	sha := "abc1234567890"

	testCases := []struct {
		name               string
		action             github.PullRequestEventAction
		hasConfigs         bool
		hasExistingJobs    bool
		expectComment      string
		expectEphemeralDir bool
		expectNoComment    bool
	}{
		{
			name:               "PR with new test creates ephemeral jobs and triggers them",
			action:             github.PullRequestActionOpened,
			hasConfigs:         true,
			expectComment:      "Triggered automatically",
			expectEphemeralDir: true,
		},
		{
			name:            "PR without .ci-operator/ configs is ignored",
			action:          github.PullRequestActionOpened,
			expectNoComment: true,
		},
		{
			name:            "PR with existing test only does not write ephemeral",
			action:          github.PullRequestActionOpened,
			hasConfigs:      true,
			hasExistingJobs: true,
			expectNoComment: true,
		},
		{
			name:   "PR closed cleans up ephemeral dir",
			action: github.PullRequestActionClosed,
		},
		{
			name:               "PR synchronize replaces ephemeral jobs",
			action:             github.PullRequestActionSynchronize,
			hasConfigs:         true,
			expectComment:      "Triggered automatically",
			expectEphemeralDir: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			prowCfgFile := filepath.Join(tmpDir, "prow-config.yaml")
			os.WriteFile(prowCfgFile, []byte("pod_namespace: test-pods\n"), 0644)

			ghc := &fakeGithubClient{
				prs: map[string]*github.PullRequest{
					fmt.Sprintf("%s/%s#%d", org, repo, 1): makePR(org, repo, 1, "main", sha),
				},
				dirs:  map[string][]github.DirectoryContent{},
				files: map[string][]byte{},
			}

			if tc.hasConfigs {
				dirKey := fmt.Sprintf("%s/%s/%s@%s", org, repo, ciOperatorDir, sha)
				ghc.dirs[dirKey] = []github.DirectoryContent{
					{Type: "file", Name: "ci-operator.yaml", Path: ".ci-operator/ci-operator.yaml"},
				}
				fileKey := fmt.Sprintf("%s/%s/%s@%s", org, repo, ".ci-operator/ci-operator.yaml", sha)
				ghc.files[fileKey] = []byte(ciOpConfig)
			}

			if tc.hasExistingJobs {
				writeTestJobs(t, tmpDir, org, repo)
			}

			if tc.action == github.PullRequestActionClosed {
				ephDir := filepath.Join(tmpDir, "ephemeral", org, repo, "PR-1")
				os.MkdirAll(ephDir, os.ModePerm)
				os.WriteFile(filepath.Join(ephDir, "test.yaml"), []byte("test"), 0644)
			}

			pjc := fakectrlruntimeclient.NewClientBuilder().WithScheme(pjScheme()).Build()

			s := &server{
				ghc:            ghc,
				pjclient:       pjc,
				prowConfigPath: prowCfgFile,
				namespace:      "test-ns",
				jobConfigDir:   tmpDir,
			}

			pre := makePullRequestEvent(org, repo, 1, tc.action, "main", sha)
			s.handlePullRequest(logrus.NewEntry(logrus.StandardLogger()), pre)

			ephemeralDir := filepath.Join(tmpDir, "ephemeral", org, repo, "PR-1")

			if tc.expectEphemeralDir {
				if !dirExists(ephemeralDir) {
					t.Error("expected ephemeral directory to be created")
				}
				files, _ := os.ReadDir(ephemeralDir)
				if len(files) == 0 {
					t.Error("expected job config files in ephemeral directory")
				}
			}

			if tc.action == github.PullRequestActionClosed {
				if dirExists(ephemeralDir) {
					t.Error("expected ephemeral directory to be removed on close")
				}
			}

			if tc.expectNoComment {
				if len(ghc.comments) != 0 {
					t.Errorf("expected no comments, got %d: %v", len(ghc.comments), ghc.comments)
				}
				return
			}

			if tc.expectComment != "" {
				if len(ghc.comments) == 0 {
					t.Fatal("expected a comment to be created")
				}
				lastComment := ghc.comments[len(ghc.comments)-1].body
				if !strings.Contains(lastComment, tc.expectComment) {
					t.Errorf("expected comment to contain %q, got: %s", tc.expectComment, lastComment)
				}
			}
		})
	}
}

func TestHandlePush(t *testing.T) {
	org, repo := "testorg", "testrepo"
	sha := "abc1234567890"

	testCases := []struct {
		name                 string
		pushEvent            github.PushEvent
		hasConfigs           bool
		hasExistingJobs      bool
		expectPermanentJobs  bool
		expectBootstrapJobs  bool
	}{
		{
			name: "push touching .ci-operator/ writes permanent jobs and auto-onboards",
			pushEvent: github.PushEvent{
				Ref:   "refs/heads/main",
				After: sha,
				Repo:  github.Repo{Owner: github.User{Login: org}, Name: repo},
				Commits: []github.Commit{
					{Added: []string{".ci-operator/ci-operator.yaml"}},
				},
			},
			hasConfigs:          true,
			expectPermanentJobs: true,
			expectBootstrapJobs: true,
		},
		{
			name: "push to existing repo does not re-onboard",
			pushEvent: github.PushEvent{
				Ref:   "refs/heads/main",
				After: sha,
				Repo:  github.Repo{Owner: github.User{Login: org}, Name: repo},
				Commits: []github.Commit{
					{Modified: []string{".ci-operator/ci-operator.yaml"}},
				},
			},
			hasConfigs:          true,
			hasExistingJobs:     true,
			expectPermanentJobs: true,
			expectBootstrapJobs: false,
		},
		{
			name: "push not touching .ci-operator/ is ignored",
			pushEvent: github.PushEvent{
				Ref:   "refs/heads/main",
				After: sha,
				Repo:  github.Repo{Owner: github.User{Login: org}, Name: repo},
				Commits: []github.Commit{
					{Modified: []string{"main.go"}},
				},
			},
		},
		{
			name: "deleted ref is ignored",
			pushEvent: github.PushEvent{
				Ref:     "refs/heads/main",
				Deleted: true,
				After:   sha,
				Repo:    github.Repo{Owner: github.User{Login: org}, Name: repo},
				Commits: []github.Commit{
					{Added: []string{".ci-operator/ci-operator.yaml"}},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			ghc := &fakeGithubClient{
				dirs:  map[string][]github.DirectoryContent{},
				files: map[string][]byte{},
			}

			if tc.hasConfigs {
				dirKey := fmt.Sprintf("%s/%s/%s@%s", org, repo, ciOperatorDir, sha)
				ghc.dirs[dirKey] = []github.DirectoryContent{
					{Type: "file", Name: "ci-operator.yaml", Path: ".ci-operator/ci-operator.yaml"},
				}
				fileKey := fmt.Sprintf("%s/%s/%s@%s", org, repo, ".ci-operator/ci-operator.yaml", sha)
				ghc.files[fileKey] = []byte(ciOpConfig)
			}

			if tc.hasExistingJobs {
				writeTestJobs(t, tmpDir, org, repo)
			}

			s := &server{
				ghc:              ghc,
				jobConfigDir:     tmpDir,
				prowgenImage:     "quay.io/test/prowgen:latest",
				checkconfigImage: "quay.io/test/checkconfig:latest",
				}

			s.handlePush(logrus.NewEntry(logrus.StandardLogger()), tc.pushEvent)

			permanentDir := filepath.Join(tmpDir, org, repo)
			if tc.expectPermanentJobs {
				if !dirExists(permanentDir) {
					t.Error("expected permanent jobs directory to be created")
				}
				files, _ := os.ReadDir(permanentDir)
				if len(files) == 0 {
					t.Error("expected job config files in permanent directory")
				}

				if tc.expectBootstrapJobs {
					found := false
					for _, f := range files {
						if strings.Contains(f.Name(), "presubmits") {
							content, _ := os.ReadFile(filepath.Join(permanentDir, f.Name()))
							if strings.Contains(string(content), "ci-operator-config-check") {
								found = true
								break
							}
						}
					}
					if !found {
						t.Error("expected bootstrap config-checker presubmit in permanent directory")
					}
				}
			} else if !tc.hasExistingJobs {
				if dirExists(permanentDir) {
					t.Error("did not expect permanent jobs directory to be created")
				}
			}
		})
	}
}

func TestPushTouchesCIOperator(t *testing.T) {
	testCases := []struct {
		name   string
		event  github.PushEvent
		expect bool
	}{
		{
			name: "added file",
			event: github.PushEvent{
				Commits: []github.Commit{{Added: []string{".ci-operator/ci-operator.yaml"}}},
			},
			expect: true,
		},
		{
			name: "modified file",
			event: github.PushEvent{
				Commits: []github.Commit{{Modified: []string{".ci-operator/ci-operator.yaml"}}},
			},
			expect: true,
		},
		{
			name: "removed file",
			event: github.PushEvent{
				Commits: []github.Commit{{Removed: []string{".ci-operator/ci-operator.yaml"}}},
			},
			expect: true,
		},
		{
			name: "single-file config",
			event: github.PushEvent{
				Commits: []github.Commit{{Added: []string{".ci-operator.yaml"}}},
			},
			expect: true,
		},
		{
			name: "unrelated file",
			event: github.PushEvent{
				Commits: []github.Commit{{Modified: []string{"main.go"}}},
			},
			expect: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pushTouchesCIOperator(tc.event); got != tc.expect {
				t.Errorf("expected %v, got %v", tc.expect, got)
			}
		})
	}
}

func TestMetadataFromFilename(t *testing.T) {
	testCases := []struct {
		filename string
		expected *cioperatorapi.Metadata
	}{
		{
			filename: "ci-operator.yaml",
			expected: &cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "main"},
		},
		{
			filename: "ci-operator__aws.yaml",
			expected: &cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "main", Variant: "aws"},
		},
		{
			filename: "ci-operator__multi-arch.yml",
			expected: &cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "main", Variant: "multi-arch"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			result := metadataFromFilename(tc.filename, "org", "repo", "main")
			if result.Org != tc.expected.Org || result.Repo != tc.expected.Repo ||
				result.Branch != tc.expected.Branch || result.Variant != tc.expected.Variant {
				t.Errorf("expected %+v, got %+v", tc.expected, result)
			}
		})
	}
}

func pjScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	pjapi.AddToScheme(s)
	return s
}

// writeTestJobs creates a minimal job config in the permanent EFS directory
// to simulate existing jobs for a repo.
func writeTestJobs(t *testing.T, jobConfigDir, org, repo string) {
	t.Helper()
	dir := filepath.Join(jobConfigDir, org, repo)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(`presubmits:
  %s/%s:
  - name: pull-ci-%s-%s-main-e2e
    agent: kubernetes
    always_run: true
    spec:
      containers:
      - image: ci-operator:latest
        command: [ci-operator]
    branches:
    - ^main$
`, org, repo, org, repo)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%s-main-presubmits.yaml", org, repo)), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

