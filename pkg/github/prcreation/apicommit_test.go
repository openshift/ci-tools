package prcreation

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	githubql "github.com/shurcooL/githubv4"

	"sigs.k8s.io/prow/pkg/github"
)

type fakeAPICommitClient struct {
	refs   map[string]string
	calls  []string
	input  githubql.Input
	newOid string
}

func (f *fakeAPICommitClient) GetRef(org, repo, ref string) (string, error) {
	f.calls = append(f.calls, "GetRef "+ref)
	sha, ok := f.refs[ref]
	if !ok {
		return "", github.NewNotFound()
	}
	return sha, nil
}

func (f *fakeAPICommitClient) CreateRef(org, repo, ref, sha string) error {
	f.calls = append(f.calls, "CreateRef "+ref+" "+sha)
	f.refs[strings.TrimPrefix(ref, "refs/")] = sha
	return nil
}

func (f *fakeAPICommitClient) UpdateRef(org, repo, ref, sha string, force bool) error {
	f.calls = append(f.calls, fmt.Sprintf("UpdateRef %s %s force=%t", ref, sha, force))
	f.refs[ref] = sha
	return nil
}

func (f *fakeAPICommitClient) MutateWithGitHubAppsSupport(_ context.Context, m interface{}, input githubql.Input, _ map[string]interface{}, org string) error {
	f.calls = append(f.calls, "Mutate "+org)
	f.input = input
	m.(*struct {
		CreateCommitOnBranch struct {
			Commit struct {
				Oid githubql.GitObjectID
			}
		} `graphql:"createCommitOnBranch(input: $input)"`
	}).CreateCommitOnBranch.Commit.Oid = githubql.GitObjectID(f.newOid)
	return nil
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// newRepo creates a git repository with an initial commit and returns its
// directory and HEAD SHA.
func newRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(dir, "modified.yaml"), "old\n", 0644)
	writeFile(t, filepath.Join(dir, "deleted.yaml"), "gone\n", 0644)
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "initial")
	return dir, runGit(t, dir, "rev-parse", "HEAD")
}

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func TestCommitViaAPI(t *testing.T) {
	testCases := []struct {
		name          string
		existingRefs  func(base string) map[string]string
		expectedCalls func(base string) []string
	}{
		{
			name:         "branch does not exist yet",
			existingRefs: func(string) map[string]string { return map[string]string{} },
			expectedCalls: func(base string) []string {
				return []string{"GetRef heads/autobump", "CreateRef refs/heads/autobump " + base, "Mutate org"}
			},
		},
		{
			name:         "existing branch is reset to the base",
			existingRefs: func(string) map[string]string { return map[string]string{"heads/autobump": "stale"} },
			expectedCalls: func(base string) []string {
				return []string{"GetRef heads/autobump", "UpdateRef heads/autobump " + base + " force=true", "Mutate org"}
			},
		},
		{
			name:         "branch already at the base is left alone",
			existingRefs: func(base string) map[string]string { return map[string]string{"heads/autobump": base} },
			expectedCalls: func(string) []string {
				return []string{"GetRef heads/autobump", "Mutate org"}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dir, base := newRepo(t)
			writeFile(t, filepath.Join(dir, "modified.yaml"), "new\n", 0644)
			writeFile(t, filepath.Join(dir, "sub", "added.yaml"), "added\n", 0644)
			if err := os.Remove(filepath.Join(dir, "deleted.yaml")); err != nil {
				t.Fatal(err)
			}
			runGit(t, dir, "add", "-A")

			client := &fakeAPICommitClient{refs: tc.existingRefs(base), newOid: "abc123"}
			oid, err := commitViaAPI(context.Background(), client, dir, "org", "repo", "autobump", "Bump things", "Details")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if oid != "abc123" {
				t.Errorf("expected oid abc123, got %q", oid)
			}
			if diff := cmp.Diff(tc.expectedCalls(base), client.calls); diff != "" {
				t.Errorf("unexpected calls (-want +got):\n%s", diff)
			}
			expectedInput := CreateCommitOnBranchInput{
				Branch:          CommittableBranch{RepositoryNameWithOwner: "org/repo", BranchName: "autobump"},
				ExpectedHeadOid: base,
				Message:         CommitMessage{Headline: "Bump things", Body: "Details"},
				FileChanges: FileChanges{
					Additions: []FileAddition{
						{Path: "modified.yaml", Contents: b64("new\n")},
						{Path: "sub/added.yaml", Contents: b64("added\n")},
					},
					Deletions: []FileDeletion{{Path: "deleted.yaml"}},
				},
			}
			if diff := cmp.Diff(expectedInput, client.input); diff != "" {
				t.Errorf("unexpected mutation input (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCommitViaAPIRejectsUnsupportedModes(t *testing.T) {
	testCases := []struct {
		name   string
		change func(t *testing.T, dir string)
	}{
		{
			name: "executable file",
			change: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "script.sh"), "#!/bin/sh\n", 0755)
			},
		},
		{
			name: "symlink",
			change: func(t *testing.T, dir string) {
				if err := os.Symlink("modified.yaml", filepath.Join(dir, "link.yaml")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := newRepo(t)
			tc.change(t, dir)
			runGit(t, dir, "add", "-A")

			client := &fakeAPICommitClient{refs: map[string]string{}}
			_, err := commitViaAPI(context.Background(), client, dir, "org", "repo", "autobump", "Bump", "")
			if err == nil || !strings.Contains(err.Error(), "is not supported") {
				t.Fatalf("expected unsupported mode error, got %v", err)
			}
			if len(client.calls) != 0 {
				t.Errorf("expected no GitHub calls before rejecting, got %v", client.calls)
			}
		})
	}
}
