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

type fakeTrustedChecker struct {
	trusted bool
}

func (c *fakeTrustedChecker) trustedUser(_, _, _ string, _ int) (bool, error) {
	return c.trusted, nil
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

func makeIssueCommentEvent(org, repo string, number int, user, body string) github.IssueCommentEvent {
	return github.IssueCommentEvent{
		Action: github.IssueCommentActionCreated,
		Repo:   github.Repo{Owner: github.User{Login: org}, Name: repo},
		Issue: github.Issue{
			Number:      number,
			PullRequest: &struct{}{},
		},
		Comment: github.IssueComment{
			Body: body,
			User: github.User{Login: user},
		},
	}
}

func TestHandleOnboard(t *testing.T) {
	testCases := []struct {
		name            string
		efsExists       bool
		releaseExists   bool
		trusted         bool
		expectComment   string
		expectJobsOnEFS bool
	}{
		{
			name:            "successful onboard creates bootstrap jobs",
			trusted:         true,
			expectComment:   "successfully onboarded",
			expectJobsOnEFS: true,
		},
		{
			name:          "existing release repo config blocks onboard",
			trusted:       true,
			releaseExists: true,
			expectComment: "already exist in the centralized openshift/release",
		},
		{
			name:          "existing EFS jobs blocks onboard",
			trusted:       true,
			efsExists:     true,
			expectComment: "already exist on EFS",
		},
		{
			name:          "untrusted user is rejected",
			trusted:       false,
			expectComment: "not trusted",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			releaseDir := t.TempDir()

			org, repo := "testorg", "testrepo"

			if tc.efsExists {
				os.MkdirAll(filepath.Join(tmpDir, org, repo), os.ModePerm)
			}
			if tc.releaseExists {
				os.MkdirAll(filepath.Join(releaseDir, "ci-operator/config", org, repo), os.ModePerm)
			}

			ghc := &fakeGithubClient{
				prs: map[string]*github.PullRequest{
					fmt.Sprintf("%s/%s#%d", org, repo, 1): makePR(org, repo, 1, "main", "abc1234567"),
				},
			}

			s := &server{
				ghc:              ghc,
				trustedChecker:   &fakeTrustedChecker{trusted: tc.trusted},
				jobConfigDir:     tmpDir,
				releaseRepoDir:   releaseDir,
				prowgenImage:     "quay.io/test/prowgen:latest",
				checkconfigImage: "quay.io/test/checkconfig:latest",
			}

			ic := makeIssueCommentEvent(org, repo, 1, "testuser", "/onboard")
			s.handleIssueComment(logrus.NewEntry(logrus.StandardLogger()), ic)

			if len(ghc.comments) == 0 {
				t.Fatal("expected a comment to be created")
			}

			lastComment := ghc.comments[len(ghc.comments)-1].body
			if !strings.Contains(lastComment, tc.expectComment) {
				t.Errorf("expected comment to contain %q, got: %s", tc.expectComment, lastComment)
			}

			efsPath := filepath.Join(tmpDir, org, repo)
			if tc.expectJobsOnEFS {
				if !dirExists(efsPath) {
					t.Error("expected bootstrap jobs to be written to EFS")
				}
				files, _ := os.ReadDir(efsPath)
				if len(files) == 0 {
					t.Error("expected job config files in EFS directory")
				}
			}
		})
	}
}

func TestHandleNewTest(t *testing.T) {
	org, repo := "testorg", "testrepo"
	sha := "abc1234567890"

	ciOpConfig := `
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

	testCases := []struct {
		name          string
		trusted       bool
		body          string
		hasConfigs    bool
		expectComment string
	}{
		{
			name:          "creates ProwJobs",
			trusted:       true,
			body:          "/new-test e2e",
			hasConfigs:    true,
			expectComment: "ProwJobs created",
		},
		{
			name:          "no configs found",
			trusted:       true,
			body:          "/new-test e2e",
			hasConfigs:    false,
			expectComment: "no `.ci-operator/` configs found",
		},
		{
			name:          "untrusted user rejected",
			trusted:       false,
			body:          "/new-test e2e",
			expectComment: "not trusted",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()

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

			pjc := fakectrlruntimeclient.NewClientBuilder().WithScheme(pjScheme()).Build()

			s := &server{
				ghc:            ghc,
				trustedChecker: &fakeTrustedChecker{trusted: tc.trusted},
				pjclient:       pjc,
				namespace:      "test-ns",
				jobConfigDir:   tmpDir,
			}

			ic := makeIssueCommentEvent(org, repo, 1, "testuser", tc.body)
			s.handleIssueComment(logrus.NewEntry(logrus.StandardLogger()), ic)

			if len(ghc.comments) == 0 {
				t.Fatal("expected a comment to be created")
			}

			lastComment := ghc.comments[len(ghc.comments)-1].body
			if !strings.Contains(lastComment, tc.expectComment) {
				t.Errorf("expected comment to contain %q, got: %s", tc.expectComment, lastComment)
			}
		})
	}
}

func pjScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	pjapi.AddToScheme(s)
	return s
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

func TestHandleIssueCommentDispatch(t *testing.T) {
	testCases := []struct {
		name        string
		body        string
		action      github.IssueCommentEventAction
		isPR        bool
		expectComments int
	}{
		{
			name:        "dispatches /onboard",
			body:        "/onboard",
			action:      github.IssueCommentActionCreated,
			isPR:        true,
			expectComments: 1,
		},
		{
			name:        "dispatches /new-test",
			body:        "/new-test e2e",
			action:      github.IssueCommentActionCreated,
			isPR:        true,
			expectComments: 1,
		},
		{
			name:        "ignores non-created actions",
			body:        "/onboard",
			action:      github.IssueCommentActionDeleted,
			isPR:        true,
			expectComments: 0,
		},
		{
			name:        "ignores non-PR issues",
			body:        "/onboard",
			action:      github.IssueCommentActionCreated,
			isPR:        false,
			expectComments: 0,
		},
		{
			name:        "ignores unrelated comments",
			body:        "LGTM",
			action:      github.IssueCommentActionCreated,
			isPR:        true,
			expectComments: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			ghc := &fakeGithubClient{
				prs: map[string]*github.PullRequest{
					"org/repo#1": makePR("org", "repo", 1, "main", "abc1234567890"),
				},
				dirs:  map[string][]github.DirectoryContent{},
				files: map[string][]byte{},
			}

			pjc := fakectrlruntimeclient.NewClientBuilder().WithScheme(pjScheme()).Build()

			s := &server{
				ghc:              ghc,
				trustedChecker:   &fakeTrustedChecker{trusted: true},
				pjclient:         pjc,
				namespace:        "test-ns",
				jobConfigDir:     tmpDir,
				releaseRepoDir:   tmpDir,
				prowgenImage:     "img",
				checkconfigImage: "img",
			}

			ic := github.IssueCommentEvent{
				Action: tc.action,
				Repo:   github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				Issue: github.Issue{
					Number: 1,
				},
				Comment: github.IssueComment{
					Body: tc.body,
					User: github.User{Login: "testuser"},
				},
			}
			if tc.isPR {
				ic.Issue.PullRequest = &struct{}{}
			}

			s.handleIssueComment(logrus.NewEntry(logrus.StandardLogger()), ic)

			if len(ghc.comments) != tc.expectComments {
				t.Errorf("expected %d comments, got %d: %+v", tc.expectComments, len(ghc.comments), ghc.comments)
			}
		})
	}
}
