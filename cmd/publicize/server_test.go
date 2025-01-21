package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/git/localgit"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/github/fakegithub"
)

func TestCheckPrerequisites(t *testing.T) {
	logrus.SetLevel(logrus.DebugLevel)

	testCases := []struct {
		id          string
		commentBody string

		isMember      bool
		isMerged      bool
		isPullRequest bool

		repositories     map[string]string
		expectedComments []github.IssueComment
		expectedError    error
	}{

		{
			id:            "issue is not a pull request",
			commentBody:   "/publicize",
			isMember:      true,
			repositories:  map[string]string{"org-priv/repo": "org/repo"},
			expectedError: errors.New("Publicize plugin can only be used in pull requests"),
		},
		{
			id:            "user is no org member",
			commentBody:   "/publicize",
			isMember:      false,
			isPullRequest: true,
			repositories:  map[string]string{"org-priv/repo": "org/repo"},
			expectedError: errors.New("only [org-priv](https://github.com/orgs/org-priv/people) org members may request publication of a private pull request"),
		},
		{
			id:            "pull request is not merged",
			commentBody:   "/publicize",
			isMember:      true,
			isMerged:      false,
			isPullRequest: true,
			repositories:  map[string]string{"org-priv/repo": "org/repo"},
			expectedError: errors.New("cannot publicize an unmerged pull request"),
		},
		{
			id:            "repository has no upstream repository configured",
			commentBody:   "/publicize",
			isMember:      true,
			isMerged:      true,
			isPullRequest: true,
			repositories:  map[string]string{"org-priv/anotherRepo": "org/anotherRepo"},
			expectedError: errors.New("cannot publicize because there is no upstream repository configured for org-priv/repo"),
		},
		{
			id:            "a hapy publicize",
			commentBody:   "/publicize",
			isMember:      true,
			isMerged:      true,
			isPullRequest: true,
			repositories:  map[string]string{"org-priv/repo": "org/repo"},
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			issueState := "open"
			if tc.isMerged {
				issueState = "closed"
			}

			prNumber := 1111
			fc := &fakegithub.FakeClient{
				IssueComments: make(map[int][]github.IssueComment),
				OrgMembers:    map[string][]string{"org-priv": {"k8s-ci-robot"}},
				PullRequests: map[int]*github.PullRequest{
					prNumber: {
						ID:     1,
						Number: prNumber,
						User:   github.User{Login: "pr-user"},
						Title:  tc.id,
						Body:   tc.id,
						Merged: tc.isMerged,
						Base:   github.PullRequestBranch{Ref: "master"},
					},
				},
			}

			localGit, gcf, err := localgit.NewV2()
			defer func() {
				if err := localGit.Clean(); err != nil {
					t.Errorf("couldn't clean localgit temp folders: %v", err)
				}

				if err := gcf.Clean(); err != nil {
					t.Errorf("coulnd't clean git client's temp folders: %v", err)
				}
			}()

			if err != nil {
				t.Fatal(err)
			}

			if err := localGit.MakeFakeRepo("org", "repo"); err != nil {
				t.Fatal(err)
			}

			if err := localGit.MakeFakeRepo("org-priv", "repo"); err != nil {
				t.Fatal(err)
			}

			ice := github.IssueCommentEvent{
				Action: github.IssueCommentActionCreated,
				Comment: github.IssueComment{
					Body: tc.commentBody,
				},
				Issue: github.Issue{
					User:      github.User{Login: "k8s-ci-robot"},
					Number:    prNumber,
					State:     issueState,
					Assignees: []github.User{{Login: "dptp-assignee"}},
				},

				Repo: github.Repo{
					Owner: github.User{Login: "org-priv"},
					Name:  "repo",
				},
			}

			if tc.isPullRequest {
				ice.Issue.PullRequest = &struct{}{}
			}

			if tc.isMember {
				ice.Comment.User.Login = "k8s-ci-robot"
			}

			serv := &server{
				gitName:  "test",
				gitEmail: "test@test.com",
				ghc:      fc,
				gc:       gcf,
				config: func() *Config {
					c := &Config{}
					c.Repositories = tc.repositories
					return c
				},
				dry: true,
			}

			actualErr := serv.checkPrerequisites(logrus.WithField("id", tc.id), fc.PullRequests[1111], ice)

			if !reflect.DeepEqual(actualErr, tc.expectedError) {
				t.Fatalf("%s", cmp.Diff(actualErr.Error(), tc.expectedError.Error()))
			}
		})
	}
}

func TestMergeAndPushToRemote(t *testing.T) {
	publicOrg, publicRepo := "openshift", "test"
	privateOrg, privateRepo := "openshift-priv", "test"
	fixedTime := time.Now()
	fixedTimeUnix := fixedTime.Unix()
	makeRepo := func(localgit *localgit.LocalGit, org, repo string, init func() error) error {
		if err := localgit.MakeFakeRepo(org, repo); err != nil {
			return fmt.Errorf("couldn't create fake repo for %s/%s: %w", org, repo, err)
		}
		// The test relies on the repository created by MakeFakeRepo generating
		// the same history across calls, which can only happen if time remains
		// constant, as it is part of the Git commit hash.  We amend the initial
		// file and commit with fixed dates to guarantee identical commits.
		path := filepath.Join(localgit.Dir, org, repo)
		initial := filepath.Join(path, "initial")
		if err := os.Chtimes(initial, fixedTime, fixedTime); err != nil {
			return err
		}
		cmd := exec.Command("git", "add", initial)
		cmd.Dir = path
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to amend fake repository file at %q: %w, output:\n%s", path, err, out)
		}
		cmd = exec.Command("git", "commit", "--quiet", "--amend", "--reset-author", "--no-edit")
		cmd.Dir = path
		cmd.Env = append(
			os.Environ(),
			fmt.Sprintf("GIT_AUTHOR_DATE=%d", fixedTimeUnix),
			fmt.Sprintf("GIT_COMMITTER_DATE=%d", fixedTimeUnix))
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to amend fake repository at %q: %w, output:\n%s", path, err, out)
		}
		if init != nil {
			if err := init(); err != nil {
				return err
			}
		}
		return nil
	}
	localgit, gc, err := localgit.NewV2()
	if err != nil {
		t.Fatalf("couldn't create localgit: %v", err)
	}
	localgit.InitialBranch = "master"

	defer func() {
		if err := gc.Clean(); err != nil {
			t.Fatalf("couldn't clean git cache: %v", err)
		}
	}()

	testCases := []struct {
		id             string
		branch         string
		remoteResolver func() (string, error)
		privateGitRepo func() error
		publicGitRepo  func() error
		errExpectedMsg string
	}{
		{
			id:     "wrong branch, error expected",
			branch: "whatever",
			remoteResolver: func() (string, error) {
				return path.Join(localgit.Dir, privateOrg, privateRepo), nil
			},
			errExpectedMsg: "couldn't checkout to branch whatever: error checking out \"whatever\": exit status 1 error: pathspec 'whatever' did not match any file(s) known to git",
		},
		{
			id:     "wrong remote resolver, error expected",
			branch: "refs/heads/master",
			remoteResolver: func() (string, error) {
				return path.Join(localgit.Dir, "wrongOrg", "wrongRepo"), nil
			},
			errExpectedMsg: fmt.Sprintf(`couldn't fetch from the downstream repository: error fetching refs/heads/master from %s/wrongOrg/wrongRepo: exit status 128 fatal: '%s/wrongOrg/wrongRepo' does not appear to be a git repository
fatal: Could not read from remote repository.

Please make sure you have the correct access rights
and the repository exists.
`, localgit.Dir, localgit.Dir),
		},
		{
			id:     "nothing to merge, no error expected",
			branch: "refs/heads/master",
			remoteResolver: func() (string, error) {
				return path.Join(localgit.Dir, privateOrg, privateRepo), nil
			},
		},
		{
			id:     "one commit to publicize, no error expected",
			branch: "refs/heads/master",
			remoteResolver: func() (string, error) {
				return path.Join(localgit.Dir, privateOrg, privateRepo), nil
			},
			privateGitRepo: func() error {
				filesToCommit := map[string][]byte{"test-file": []byte("TEST")}
				if err := localgit.AddCommit(privateOrg, privateRepo, filesToCommit); err != nil {
					return fmt.Errorf("couldn't add commit: %w", err)
				}
				return nil
			},
		},
		{
			id:     "multiple commits to publicize, no error expected",
			branch: "refs/heads/master",
			remoteResolver: func() (string, error) {
				return path.Join(localgit.Dir, privateOrg, privateRepo), nil
			},
			privateGitRepo: func() error {
				filesToCommit := map[string][]byte{
					"test-file":  []byte("TEST"),
					"test-file2": []byte("TEST"),
					"test-file3": []byte("TEST"),
				}
				if err := localgit.AddCommit(privateOrg, privateRepo, filesToCommit); err != nil {
					return fmt.Errorf("couldn't add commit: %w", err)
				}
				return nil
			},
		},
		{
			id:     "different histories without conflict, no error expected",
			branch: "refs/heads/master",
			remoteResolver: func() (string, error) {
				return path.Join(localgit.Dir, privateOrg, privateRepo), nil
			},
			privateGitRepo: func() error {
				filesToCommit := map[string][]byte{
					"test-file":  []byte("TEST"),
					"test-file2": []byte("TEST"),
					"test-file3": []byte("TEST"),
				}
				if err := localgit.AddCommit(privateOrg, privateRepo, filesToCommit); err != nil {
					return fmt.Errorf("couldn't add commit: %w", err)
				}
				return nil
			},
			publicGitRepo: func() error {
				filesToCommit := map[string][]byte{
					"test-file4": []byte("TEST"),
					"test-file5": []byte("TEST"),
					"test-file6": []byte("TEST"),
				}
				if err := localgit.AddCommit(publicOrg, publicRepo, filesToCommit); err != nil {
					return fmt.Errorf("couldn't add commit: %w", err)
				}
				return nil
			},
		},
		{
			id:     "one commit to publicize with conflict, error expected",
			branch: "refs/heads/master",
			remoteResolver: func() (string, error) {
				return path.Join(localgit.Dir, privateOrg, privateRepo), nil
			},
			privateGitRepo: func() error {
				filesToCommit := map[string][]byte{"test-file": []byte("CONFLICT")}
				if err := localgit.AddCommit(privateOrg, privateRepo, filesToCommit); err != nil {
					return fmt.Errorf("couldn't add commit: %w", err)
				}
				return nil
			},
			publicGitRepo: func() error {
				filesToCommit := map[string][]byte{"test-file": []byte("TEST")}
				if err := localgit.AddCommit(publicOrg, publicRepo, filesToCommit); err != nil {
					return fmt.Errorf("couldn't add commit: %w", err)
				}
				return nil
			},
			errExpectedMsg: "couldn't merge openshift/test, due to merge conflict. You will need to create a new PR in openshift-priv/test which merges/resolves from openshift/test. Once this PR merges, you can then use /publicize there to merge all changes into the the public repository.",
		},
		{
			id:     "multiple commits with one conflict, error expected",
			branch: "refs/heads/master",
			remoteResolver: func() (string, error) {
				return path.Join(localgit.Dir, privateOrg, privateRepo), nil
			},
			privateGitRepo: func() error {
				filesToCommit := map[string][]byte{
					"test-file":  []byte("CONFLICT"),
					"test-file2": []byte("TEST"),
					"test-file3": []byte("TEST"),
				}
				if err := localgit.AddCommit(privateOrg, privateRepo, filesToCommit); err != nil {
					return fmt.Errorf("couldn't add commit: %w", err)
				}
				return nil
			},
			publicGitRepo: func() error {
				filesToCommit := map[string][]byte{
					"test-file":  []byte("TEST"),
					"test-file5": []byte("TEST"),
					"test-file6": []byte("TEST"),
				}
				if err := localgit.AddCommit(publicOrg, publicRepo, filesToCommit); err != nil {
					return fmt.Errorf("couldn't add commit: %w", err)
				}
				return nil
			},
			errExpectedMsg: "couldn't merge openshift/test, due to merge conflict. You will need to create a new PR in openshift-priv/test which merges/resolves from openshift/test. Once this PR merges, you can then use /publicize there to merge all changes into the the public repository.",
		},
	}

	s := &server{
		gc:       gc,
		gitName:  "Foo Bar",
		gitEmail: "foobar@redhat.com",
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			if err := makeRepo(localgit, privateOrg, privateRepo, tc.privateGitRepo); err != nil {
				t.Fatalf("couldn't create private fake repo: %v", err)
			}
			if err := makeRepo(localgit, publicOrg, publicRepo, tc.publicGitRepo); err != nil {
				t.Fatalf("couldn't create public fake repo: %v", err)
			}
			headCommitRef, err := s.mergeAndPushToRemote(privateOrg, privateRepo, publicOrg, publicRepo, tc.remoteResolver, tc.branch, false)
			if err != nil && tc.errExpectedMsg == "" {
				t.Fatalf("error not expected: %v", err)
			}

			if err != nil && !strings.HasPrefix(err.Error(), tc.errExpectedMsg) {
				t.Fatal(cmp.Diff(err.Error(), tc.errExpectedMsg))
			}

			if err == nil && len(headCommitRef) != 40 {
				t.Fatalf("expected a head commit ref to be 40 chars long: %s", headCommitRef)
			}

			if err := localgit.Clean(); err != nil {
				t.Fatalf("couldn't clean temporary folders: %v", err)
			}
		})
	}
}
