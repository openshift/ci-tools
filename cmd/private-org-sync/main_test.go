package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/privateorg"
)

func TestOptionsValidate(t *testing.T) {
	good := options{
		Options:   config.Options{LogLevel: "info"},
		configDir: "path/to/dir",
		tokenPath: "path/to/token",
		targetOrg: "org",
		gitName:   "openshift-bot",
		gitEmail:  "opensthift-bot@redhat.com",
	}
	testcases := []struct {
		description string
		bad         *options

		org  string
		repo string

		expectedErrors int
	}{
		{
			description: "good options pass validation",
		},
		{
			description:    "missing --config-dir does not pass validation",
			bad:            &options{tokenPath: "path/to/token", targetOrg: "org"},
			expectedErrors: 3,
		},
		{
			description:    "missing --token-path does not pass validation",
			bad:            &options{configDir: "path/to/dir", targetOrg: "org", Options: config.Options{LogLevel: "info"}},
			expectedErrors: 3,
		},
		{
			description:    "missing --target-org does not pass validation",
			bad:            &options{configDir: "path/to/dir", tokenPath: "path/to/token", Options: config.Options{LogLevel: "info"}},
			expectedErrors: 3,
		},
		{
			description: "--only-org different from --target-org passes validation",
			org:         "different-org",
		},
		{
			description:    "--only-org same as --target-org does not pass validation",
			org:            "org",
			expectedErrors: 1,
		},
		{
			description:    "--only-org and --only-repo does not pass validation",
			org:            "different-org",
			repo:           "another-org/repo",
			expectedErrors: 1,
		},
		{
			description:    "bad --only-repo does not pass validation",
			repo:           "not-a-repo",
			expectedErrors: 1,
		},
		{
			description:    "--only-repo in --target-org does not pass validation",
			repo:           "org/repo",
			expectedErrors: 1,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.description, func(t *testing.T) {
			opts := good
			if tc.bad != nil {
				opts = *tc.bad
			}
			opts.org = tc.org
			opts.repo = tc.repo

			errs := opts.validate()
			if len(errs) != tc.expectedErrors {
				t.Errorf("%s: expected %d errors, got %d (%v)", tc.description, tc.expectedErrors, len(errs), errs)
			}
		})
	}
}

func TestOptionsMakeFilter(t *testing.T) {
	official := &api.ReleaseBuildConfiguration{
		PromotionConfiguration: &api.PromotionConfiguration{
			Targets: []api.PromotionTarget{{
				Namespace: "ocp",
			}},
		},
	}
	notOfficial := &api.ReleaseBuildConfiguration{
		PromotionConfiguration: &api.PromotionConfiguration{
			Targets: []api.PromotionTarget{{
				Namespace: "not-ocp",
			}},
		},
	}
	// Check that our assumptions about what is an official image still holds
	if !api.BuildsAnyOfficialImages(official, api.WithoutOKD) {
		t.Fatal("Test data assumed to be official images are not official images")
	}
	if api.BuildsAnyOfficialImages(notOfficial, api.WithoutOKD) {
		t.Fatal("Test data assumed to be non-official images are official images")
	}
	testcases := []struct {
		description   string
		optionOrg     string
		optionRepo    string
		repoOrg       string
		repoName      string
		callbackError error
		notOfficial   bool

		expectCall  bool
		expectError bool
	}{
		{
			description: "no org option passed, callbacks are not filtered",
			expectCall:  true,
		},
		{
			description: "org option passed, callback is made for repo in that org",
			optionOrg:   "org",
			repoOrg:     "org",
			expectCall:  true,
		},
		{
			description: "org option passed, callback is not made for repo not in that org",
			optionOrg:   "org",
			repoOrg:     "not-org",
			expectCall:  false,
		},
		{
			description: "repo option passed, callback is made for that repo",
			optionRepo:  "org/repo",
			repoOrg:     "org",
			repoName:    "repo",
			expectCall:  true,
		},
		{
			description: "repo option passed, callback is not made for other repo",
			optionRepo:  "org/repo",
			repoOrg:     "org",
			repoName:    "not-repo",
			expectCall:  false,
		},
		{
			description:   "callback is made and its error is propagated",
			callbackError: fmt.Errorf("FAIL"),
			expectCall:    true,
			expectError:   true,
		},
		{
			description: "no filter options but repo does not build official images, callback is not made",
			notOfficial: true,
			expectCall:  false,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.description, func(t *testing.T) {
			o := &options{
				org:  tc.optionOrg,
				repo: tc.optionRepo,
			}
			ciop := official
			if tc.notOfficial {
				ciop = notOfficial
			}
			info := &config.Info{
				Metadata: api.Metadata{
					Org:  tc.repoOrg,
					Repo: tc.repoName,
				},
			}
			var called bool
			callback := func(*api.ReleaseBuildConfiguration, *config.Info) error {
				called = true
				return tc.callbackError
			}
			err := o.makeFilter(callback)(ciop, info)
			if err == nil && tc.expectError {
				t.Errorf("%s: expected error, got none", tc.description)
			}
			if err != nil && !tc.expectError {
				t.Errorf("%s: got unexpected error: %v", tc.description, err)
			}
			if called != tc.expectCall {
				var expected, actual string
				if !tc.expectCall {
					expected = "not "
				}
				if !called {
					actual = " not"
				}
				t.Errorf("%s: expected callback to %sbe called, it was%s", tc.description, expected, actual)
			}
		})
	}
}

type mockGitCall struct {
	call     string
	output   string
	exitCode int
}

type mockGit struct {
	next     int
	expected []mockGitCall

	t *testing.T
}

func (m *mockGit) exec(_ *logrus.Entry, _ string, command ...string) (string, int, error) {
	cmd := strings.Join(command, " ")
	if m.next >= len(m.expected) {
		m.t.Fatalf("unexpected git call: %s", cmd)
		return "", 0, nil
	}
	if m.expected[m.next].call != cmd {
		m.t.Fatalf("unexpected git call:\n  expected: %s\n  called:   %s", m.expected[m.next].call, cmd)
		return "", 0, nil
	}

	out := m.expected[m.next].output
	exitCode := m.expected[m.next].exitCode
	m.next++

	return out, exitCode, nil
}

func (m mockGit) check() error {
	if m.next != len(m.expected) {
		return fmt.Errorf("unexpected number of git calls: expected %d, done %d", len(m.expected), m.next)
	}
	return nil
}

func TestMirror(t *testing.T) {
	second = time.Millisecond
	token := "TOKEN"
	org, repo, branch := "org", "repo", "branch"
	destOrg := "dest"
	testCases := []struct {
		description string

		src     location
		dst     location
		confirm bool

		srcHeads RemoteBranchHeads
		dstHeads RemoteBranchHeads

		expectedGitCalls []mockGitCall
		expectError      bool
	}{
		{
			description: "confirm, success -> no error",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			confirm:     true,
			srcHeads:    RemoteBranchHeads{branch: "source-sha"},
			dstHeads:    RemoteBranchHeads{branch: "dest-sha"},
			expectedGitCalls: []mockGitCall{
				{call: "fetch --tags org-repo branch --depth=2"},
				{call: "push --tags https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch"},
			},
		},
		{
			description: "no confirm, success -> push with dry run",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			srcHeads:    RemoteBranchHeads{branch: "source-sha"},
			dstHeads:    RemoteBranchHeads{branch: "dest-sha"},
			expectedGitCalls: []mockGitCall{
				{call: "fetch --tags org-repo branch --depth=2"},
				{call: "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch"},
			},
		},
		{
			description: "fails to fetch -> error",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			srcHeads:    RemoteBranchHeads{branch: "source-sha"},
			dstHeads:    RemoteBranchHeads{branch: "dest-sha"},
			expectedGitCalls: []mockGitCall{
				{call: "fetch --tags org-repo branch --depth=2", exitCode: 1},
			},
			expectError: true,
		},
		{
			description: "fetch fails with shallow file changed -> retries and succeeds",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			srcHeads:    RemoteBranchHeads{branch: "source-sha"},
			dstHeads:    RemoteBranchHeads{branch: "dest-sha"},
			expectedGitCalls: []mockGitCall{
				{call: "fetch --tags org-repo branch --depth=2", exitCode: 128, output: "fatal: shallow file has changed since we read it\n"},
				{call: "fetch --tags org-repo branch --depth=2"},
				{call: "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch"},
			},
		},
		{
			description: "fetch fails with shallow file changed repeatedly -> error after retries exhausted",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			srcHeads:    RemoteBranchHeads{branch: "source-sha"},
			dstHeads:    RemoteBranchHeads{branch: "dest-sha"},
			expectedGitCalls: []mockGitCall{
				{call: "fetch --tags org-repo branch --depth=2", exitCode: 128, output: "fatal: shallow file has changed since we read it\n"},
				{call: "fetch --tags org-repo branch --depth=2", exitCode: 128, output: "fatal: shallow file has changed since we read it\n"},
				{call: "fetch --tags org-repo branch --depth=2", exitCode: 128, output: "fatal: shallow file has changed since we read it\n"},
			},
			expectError: true,
		},
		{
			description: "no confirm, fails to push -> error",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			srcHeads:    RemoteBranchHeads{branch: "source-sha"},
			dstHeads:    RemoteBranchHeads{branch: "dest-sha"},
			expectedGitCalls: []mockGitCall{
				{call: "fetch --tags org-repo branch --depth=2"},
				{call: "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch", exitCode: 1},
			},
			expectError: true,
		},
		{
			description: "branches are in sync -> no fetch, no push",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			srcHeads:    RemoteBranchHeads{branch: "same-sha"},
			dstHeads:    RemoteBranchHeads{branch: "same-sha"},
		},
		{
			description: "destination is empty repo -> full fetch then success",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			srcHeads:    RemoteBranchHeads{branch: "source-sha"},
			dstHeads:    RemoteBranchHeads{},
			expectedGitCalls: []mockGitCall{
				{call: "fetch --tags org-repo branch"},
				{call: "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch"},
			},
		},
		{
			description: "destination needs 50 commits -> retries deepening fetches, then success",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			srcHeads:    RemoteBranchHeads{branch: "source-sha"},
			dstHeads:    RemoteBranchHeads{branch: "dest-sha"},
			expectedGitCalls: []mockGitCall{
				{call: "fetch --tags org-repo branch --depth=2"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=2"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=4"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=8"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=16"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=32"},
				{call: "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch"},
			},
		},
		{
			description: "destination needs to merge with source -> retries exceeded, then perform merge after fetching --unshallow",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			srcHeads:    RemoteBranchHeads{branch: "source-sha"},
			dstHeads:    RemoteBranchHeads{branch: "dest-sha"},
			expectedGitCalls: []mockGitCall{
				{call: "fetch --tags org-repo branch --depth=2"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=2"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=4"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=8"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=16"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=32"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --unshallow"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "fetch https://TOKEN@github.com/dest/repo branch"},
				{call: "checkout FETCH_HEAD"},
				{call: "-c user.name=openshift-bot -c user.email=openshift-bot@redhat.com merge org-repo/branch -m DPTP reconciliation from upstream"},
				{call: "push --tags --dry-run https://TOKEN@github.com/dest/repo HEAD:branch"},
			},
		},
		{
			description: "destination needs to merge with source -> retries exceeded, merge fails and performs merge --abort",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			srcHeads:    RemoteBranchHeads{branch: "source-sha"},
			dstHeads:    RemoteBranchHeads{branch: "dest-sha"},
			expectedGitCalls: []mockGitCall{
				{call: "fetch --tags org-repo branch --depth=2"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=2"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=4"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=8"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=16"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --deepen=32"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch org-repo branch --unshallow"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "fetch https://TOKEN@github.com/dest/repo branch"},
				{call: "checkout FETCH_HEAD"},
				{
					call:     "-c user.name=openshift-bot -c user.email=openshift-bot@redhat.com merge org-repo/branch -m DPTP reconciliation from upstream",
					exitCode: 1,
				},
				{call: "merge --abort"},
			},
		},
		{
			description: "conflicting histories after a force-push result in an error",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			srcHeads:    RemoteBranchHeads{branch: "source-sha"},
			dstHeads:    RemoteBranchHeads{},
			expectedGitCalls: []mockGitCall{
				{call: "fetch --tags org-repo branch"},
				{
					call: "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					output: `To https://TOKEN@github.com/dest/repo
 ! [rejected]        branch -> branch (non-fast-forward)
error: failed to push some refs to 'https://TOKEN@github.com/dest/repo'
hint: Updates were rejected because the tip of your current branch is behind
hint: its remote counterpart. Integrate the remote changes (e.g.
hint: 'git pull ...') before pushing again.
hint: See the 'Note about fast-forwards' in 'git push --help' for details.
`,
					exitCode: 1,
				},
			},
			expectError: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			git := mockGit{
				expected: tc.expectedGitCalls,
				t:        t,
			}
			m := gitSyncer{
				logger:   logrus.WithField("test", tc.description),
				prefix:   defaultPrefix,
				token:    token,
				confirm:  tc.confirm,
				root:     "git-dir",
				git:      git.exec,
				gitName:  "openshift-bot",
				gitEmail: "openshift-bot@redhat.com",
			}
			initialDepth := startDepth
			if len(tc.dstHeads) == 0 {
				initialDepth = fullFetch
			}
			err := m.mirror("repo-dir", tc.src, tc.dst, tc.srcHeads[tc.src.branch], tc.dstHeads[tc.dst.branch], initialDepth)
			if err == nil && tc.expectError {
				t.Error("expected error, got nil")
			}
			if err != nil && !tc.expectError {
				t.Errorf("unexpected error: %v", err)
			}
			if err = git.check(); err != nil {
				t.Errorf("bad git operation: %v", err)
			}
		})
	}
}

func TestInitRepo(t *testing.T) {
	token := "TOKEN"
	testCases := []struct {
		description      string
		org, repo        string
		expectedGitCalls []mockGitCall
		expectError      bool
	}{
		{
			description: "cold cache -> init and add remote",
			org:         "org",
			repo:        "repo",
			expectedGitCalls: []mockGitCall{
				{call: "init"},
				{call: "remote get-url org-repo", exitCode: 1},
				{call: "remote add org-repo https://TOKEN@github.com/org/repo"},
			},
		},
		{
			description: "warm cache -> init, remote already exists",
			org:         "org",
			repo:        "repo",
			expectedGitCalls: []mockGitCall{
				{call: "init"},
				{call: "remote get-url org-repo"},
			},
		},
		{
			description: "init fails -> error",
			org:         "org",
			repo:        "repo",
			expectedGitCalls: []mockGitCall{
				{call: "init", exitCode: 1},
			},
			expectError: true,
		},
		{
			description: "remote add fails -> error",
			org:         "org",
			repo:        "repo",
			expectedGitCalls: []mockGitCall{
				{call: "init"},
				{call: "remote get-url org-repo", exitCode: 1},
				{call: "remote add org-repo https://TOKEN@github.com/org/repo", exitCode: 1},
			},
			expectError: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			git := mockGit{
				expected: tc.expectedGitCalls,
				t:        t,
			}
			s := gitSyncer{
				logger: logrus.WithField("test", tc.description),
				prefix: defaultPrefix,
				token:  token,
				root:   "git-dir",
				git:    git.exec,
			}
			err := s.initRepo("repo-dir", tc.org, tc.repo)
			if err == nil && tc.expectError {
				t.Error("expected error, got nil")
			}
			if err != nil && !tc.expectError {
				t.Errorf("unexpected error: %v", err)
			}
			if err = git.check(); err != nil {
				t.Errorf("bad git operation: %v", err)
			}
		})
	}
}

func TestSyncRepo(t *testing.T) {
	second = time.Millisecond
	token := "TOKEN"
	org, repo := "org", "repo"
	targetOrg := "dest"
	branches := []location{
		{org: org, repo: repo, branch: "main"},
		{org: org, repo: repo, branch: "release-4.18"},
	}

	testCases := []struct {
		description          string
		branches             []location
		failOnNonexistentDst bool
		expectedGitCalls     []mockGitCall
		expectError          bool
	}{
		{
			description: "all branches in sync -> init, ls-remote, no fetch/push",
			branches:    branches,
			expectedGitCalls: []mockGitCall{
				{call: "init"},
				{call: "remote get-url org-repo", exitCode: 1},
				{call: "remote add org-repo https://TOKEN@github.com/org/repo"},
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "aaa refs/heads/main\naaa refs/heads/release-4.18\n"},
				{call: "ls-remote --heads org-repo", output: "aaa refs/heads/main\naaa refs/heads/release-4.18\n"},
			},
		},
		{
			description: "one branch needs sync -> fetches and pushes that branch only",
			branches:    branches,
			expectedGitCalls: []mockGitCall{
				{call: "init"},
				{call: "remote get-url org-repo", exitCode: 1},
				{call: "remote add org-repo https://TOKEN@github.com/org/repo"},
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "aaa refs/heads/main\nbbb refs/heads/release-4.18\n"},
				{call: "ls-remote --heads org-repo", output: "aaa refs/heads/main\naaa refs/heads/release-4.18\n"},
				// only release-4.18 needs sync (bbb != aaa)
				{call: "fetch --tags org-repo release-4.18 --depth=2"},
				{call: "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/release-4.18"},
			},
		},
		{
			description:          "dst ls-remote fails, failOnNonexistentDst=true -> error",
			branches:             branches,
			failOnNonexistentDst: true,
			expectedGitCalls: []mockGitCall{
				{call: "init"},
				{call: "remote get-url org-repo", exitCode: 1},
				{call: "remote add org-repo https://TOKEN@github.com/org/repo"},
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", exitCode: 1},
			},
			expectError: true,
		},
		{
			description:          "dst ls-remote fails, failOnNonexistentDst=false -> no error (skip)",
			branches:             branches,
			failOnNonexistentDst: false,
			expectedGitCalls: []mockGitCall{
				{call: "init"},
				{call: "remote get-url org-repo", exitCode: 1},
				{call: "remote add org-repo https://TOKEN@github.com/org/repo"},
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", exitCode: 1},
			},
		},
		{
			description: "src ls-remote fails -> error",
			branches:    branches,
			expectedGitCalls: []mockGitCall{
				{call: "init"},
				{call: "remote get-url org-repo", exitCode: 1},
				{call: "remote add org-repo https://TOKEN@github.com/org/repo"},
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "aaa refs/heads/main\n"},
				// src ls-remote fails with retries (withRetryOnNonzero does 5 retries)
				{call: "ls-remote --heads org-repo", exitCode: 1},
				{call: "ls-remote --heads org-repo", exitCode: 1},
				{call: "ls-remote --heads org-repo", exitCode: 1},
				{call: "ls-remote --heads org-repo", exitCode: 1},
				{call: "ls-remote --heads org-repo", exitCode: 1},
			},
			expectError: true,
		},
		{
			description: "init fails -> error",
			branches:    branches,
			expectedGitCalls: []mockGitCall{
				{call: "init", exitCode: 1},
			},
			expectError: true,
		},
		{
			description: "non-release source branch does not exist -> skip with warning, no error",
			branches:    []location{{org: org, repo: repo, branch: "some-feature"}},
			expectedGitCalls: []mockGitCall{
				{call: "init"},
				{call: "remote get-url org-repo", exitCode: 1},
				{call: "remote add org-repo https://TOKEN@github.com/org/repo"},
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "aaa refs/heads/some-feature\n"},
				{call: "ls-remote --heads org-repo", output: "aaa refs/heads/other-branch\n"},
			},
		},
		{
			description: "release source branch does not exist -> error",
			branches:    []location{{org: org, repo: repo, branch: "release-4.21"}},
			expectedGitCalls: []mockGitCall{
				{call: "init"},
				{call: "remote get-url org-repo", exitCode: 1},
				{call: "remote add org-repo https://TOKEN@github.com/org/repo"},
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "aaa refs/heads/release-4.21\n"},
				{call: "ls-remote --heads org-repo", output: "aaa refs/heads/other-branch\n"},
			},
			expectError: true,
		},
		{
			description: "main source branch does not exist -> error",
			branches:    []location{{org: org, repo: repo, branch: "main"}},
			expectedGitCalls: []mockGitCall{
				{call: "init"},
				{call: "remote get-url org-repo", exitCode: 1},
				{call: "remote add org-repo https://TOKEN@github.com/org/repo"},
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "aaa refs/heads/main\n"},
				{call: "ls-remote --heads org-repo", output: "aaa refs/heads/other-branch\n"},
			},
			expectError: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			git := mockGit{
				expected: tc.expectedGitCalls,
				t:        t,
			}
			s := gitSyncer{
				logger:               logrus.WithField("test", tc.description),
				prefix:               defaultPrefix,
				token:                token,
				root:                 t.TempDir(),
				git:                  git.exec,
				confirm:              false,
				failOnNonexistentDst: tc.failOnNonexistentDst,
				gitName:              "openshift-bot",
				gitEmail:             "openshift-bot@redhat.com",
			}
			err := s.syncRepo(org, repo, targetOrg, repo, tc.branches)
			if err == nil && tc.expectError {
				t.Error("expected error, got nil")
			}
			if err != nil && !tc.expectError {
				t.Errorf("unexpected error: %v", err)
			}
			if err := git.check(); err != nil {
				t.Errorf("bad git operation: %v", err)
			}
		})
	}
}

func TestDestinationNaming(t *testing.T) {
	testCases := []struct {
		name         string
		sourceOrg    string
		sourceRepo   string
		targetOrg    string
		onlyOrg      string
		flattenOrgs  []string
		expectedRepo string
	}{
		{
			name:         "repo from only-org keeps original name",
			sourceOrg:    "openshift",
			sourceRepo:   "api",
			targetOrg:    "openshift-priv",
			onlyOrg:      "openshift",
			flattenOrgs:  nil,
			expectedRepo: "api",
		},
		{
			name:         "repo from different org gets prefixed name",
			sourceOrg:    "migtools",
			sourceRepo:   "must-gather",
			targetOrg:    "openshift-priv",
			onlyOrg:      "openshift",
			flattenOrgs:  nil,
			expectedRepo: "migtools-must-gather",
		},
		{
			name:         "no only-org specified, non-default orgs get prefixed name",
			sourceOrg:    "migtools",
			sourceRepo:   "must-gather",
			targetOrg:    "openshift-priv",
			onlyOrg:      "",
			flattenOrgs:  nil,
			expectedRepo: "migtools-must-gather",
		},
		{
			name:         "repo from flatten-org keeps original name",
			sourceOrg:    "openshift-eng",
			sourceRepo:   "ocp-build-data",
			targetOrg:    "openshift-priv",
			onlyOrg:      "openshift",
			flattenOrgs:  []string{"openshift-eng", "migtools"},
			expectedRepo: "ocp-build-data",
		},
		{
			name:         "repo from flatten-org without only-org keeps original name",
			sourceOrg:    "openshift-eng",
			sourceRepo:   "ocp-build-data",
			targetOrg:    "openshift-priv",
			onlyOrg:      "",
			flattenOrgs:  []string{"openshift-eng"},
			expectedRepo: "ocp-build-data",
		},
		{
			name:         "repo not in flatten-org list gets prefixed",
			sourceOrg:    "custom-org",
			sourceRepo:   "custom-repo",
			targetOrg:    "openshift-priv",
			onlyOrg:      "openshift",
			flattenOrgs:  []string{"another-org"},
			expectedRepo: "custom-org-custom-repo",
		},
		{
			name:         "default flattened orgs keep original names without --only-org",
			sourceOrg:    "openshift",
			sourceRepo:   "installer",
			targetOrg:    "openshift-priv",
			onlyOrg:      "",
			flattenOrgs:  nil,
			expectedRepo: "installer",
		},
		{
			name:         "default flattened org openshift-eng keeps original name",
			sourceOrg:    "openshift-eng",
			sourceRepo:   "ocp-build-data",
			targetOrg:    "openshift-priv",
			onlyOrg:      "",
			flattenOrgs:  nil,
			expectedRepo: "ocp-build-data",
		},
		{
			name:         "default flattened org redhat-cne keeps original name",
			sourceOrg:    "redhat-cne",
			sourceRepo:   "cloud-event-proxy",
			targetOrg:    "openshift-priv",
			onlyOrg:      "",
			flattenOrgs:  nil,
			expectedRepo: "cloud-event-proxy",
		},
		{
			name:         "default flattened org ViaQ keeps original name",
			sourceOrg:    "ViaQ",
			sourceRepo:   "logging-fluentd",
			targetOrg:    "openshift-priv",
			onlyOrg:      "",
			flattenOrgs:  nil,
			expectedRepo: "logging-fluentd",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			o := &options{
				targetOrg:   tc.targetOrg,
				org:         tc.onlyOrg,
				flattenOrgs: tc.flattenOrgs,
			}

			source := location{
				org:    tc.sourceOrg,
				repo:   tc.sourceRepo,
				branch: "main",
			}

			destination := source
			destination.org = o.targetOrg

			// Apply the same logic as in main()
			// Start with the default flattened orgs for backwards compatibility
			flattenedOrgs := sets.New[string](privateorg.DefaultFlattenOrgs...)
			// Add any additional orgs specified via --flatten-org
			flattenedOrgs.Insert(o.flattenOrgs...)
			// The --only-org is also flattened if specified
			if o.org != "" {
				flattenedOrgs.Insert(o.org)
			}
			if !flattenedOrgs.Has(source.org) {
				destination.repo = fmt.Sprintf("%s-%s", source.org, source.repo)
			}

			if destination.repo != tc.expectedRepo {
				t.Errorf("expected destination repo %q, got %q", tc.expectedRepo, destination.repo)
			}
		})
	}
}

func TestIsBranchExcluded(t *testing.T) {
	o := &options{
		compiledExcludePatterns: []*regexp.Regexp{
			regexp.MustCompile(`^konflux/`),
			regexp.MustCompile(`^dependabot/`),
		},
	}

	testCases := []struct {
		branch   string
		excluded bool
	}{
		{branch: "main", excluded: false},
		{branch: "release-4.18", excluded: false},
		{branch: "konflux/mintmaker/release-1.6/eslint-plugin-react-7.x", excluded: true},
		{branch: "dependabot/npm/lodash-4.17.21", excluded: true},
		{branch: "feature/konflux-support", excluded: false},
	}
	for _, tc := range testCases {
		t.Run(tc.branch, func(t *testing.T) {
			if got := o.isBranchExcluded(tc.branch); got != tc.excluded {
				t.Errorf("isBranchExcluded(%q) = %v, want %v", tc.branch, got, tc.excluded)
			}
		})
	}
}

func TestGetWhitelistedLocationsExcludesBranches(t *testing.T) {
	git := func(_ *logrus.Entry, _ string, command ...string) (string, int, error) {
		if command[0] == "ls-remote" {
			return "sha1 refs/heads/main\nsha2 refs/heads/release-4.18\nsha3 refs/heads/konflux/mintmaker/dep-update\n", 0, nil
		}
		return "", 0, nil
	}

	excludeKonflux := func(branch string) bool {
		return regexp.MustCompile(`^konflux/`).MatchString(branch)
	}
	noExclusions := func(string) bool { return false }

	whitelist := map[string][]string{"org": {"repo"}}

	t.Run("with exclusion pattern", func(t *testing.T) {
		locations, errs := getWhitelistedLocations(whitelist, git, defaultPrefix, "", excludeKonflux)
		if len(errs) > 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		if len(locations) != 2 {
			t.Errorf("expected 2 locations, got %d: %v", len(locations), locations)
		}
		excluded := location{org: "org", repo: "repo", branch: "konflux/mintmaker/dep-update"}
		if _, ok := locations[excluded]; ok {
			t.Error("konflux branch should have been excluded")
		}
	})

	t.Run("without exclusion pattern", func(t *testing.T) {
		locations, errs := getWhitelistedLocations(whitelist, git, defaultPrefix, "", noExclusions)
		if len(errs) > 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		if len(locations) != 3 {
			t.Errorf("expected 3 locations, got %d: %v", len(locations), locations)
		}
	})
}
