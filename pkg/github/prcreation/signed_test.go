package prcreation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	githubql "github.com/shurcooL/githubv4"
	"sigs.k8s.io/prow/pkg/github"
)

func TestStagedFileChanges(t *testing.T) {
	for _, tc := range []struct{ name string }{{name: "binary additions, staged edits, deletions and unsupported modes"}} {
		t.Run(tc.name, testStagedFileChanges)
	}
}

// testStagedFileChanges checks index contents against real Git behavior.
func testStagedFileChanges(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	runTestGit(t, "init")
	runTestGit(t, "config", "user.name", "test")
	runTestGit(t, "config", "user.email", "test@example.com")
	writeTestFile(t, dir, "old", []byte("original"))
	writeTestFile(t, dir, "deleted", []byte("delete me"))
	runTestGit(t, "add", "-A")
	runTestGit(t, "-c", "commit.gpgsign=false", "commit", "-m", "base")
	writeTestFile(t, dir, "old", []byte("updated"))
	if err := os.Remove("deleted"); err != nil {
		t.Fatal(err)
	}
	binary := []byte{0, 255, 10, 13}
	writeTestFile(t, dir, "new\nfile", binary)
	runTestGit(t, "add", "-A")
	// Read the staged snapshot, not a later working-tree edit.
	writeTestFile(t, dir, "old", []byte("unstaged"))
	changes, err := stagedFileChanges()
	if err != nil {
		t.Fatal(err)
	}
	additions := map[string]string{}
	for _, a := range changes.Additions {
		b, err := base64.StdEncoding.DecodeString(a.Contents)
		if err != nil {
			t.Fatal(err)
		}
		additions[a.Path] = string(b)
	}
	if diff := cmp.Diff(map[string]string{"old": "updated", "new\nfile": string(binary)}, additions); diff != "" {
		t.Errorf("unexpected additions (-want +got):\n%s", diff)
	}
	if len(changes.Deletions) != 1 || changes.Deletions[0].Path != "deleted" {
		t.Fatalf("unexpected deletions: %#v", changes.Deletions)
	}
	runTestGit(t, "update-index", "--chmod=+x", "old")
	if _, err := stagedFileChanges(); err == nil {
		t.Fatal("expected executable mode rejection")
	}
}

// signingTestClient records branch publication while exercising the real GraphQL encoder.
type signingTestClient struct {
	github.Client
	graphql *githubql.Client
	events  []string
	refErr  error
}

func (c *signingTestClient) CreateRef(_, _, ref, sha string) error {
	c.events = append(c.events, "create:"+ref+":"+sha)
	return nil
}
func (c *signingTestClient) DeleteRef(_, _, ref string) error {
	c.events = append(c.events, "delete:"+ref)
	return nil
}
func (c *signingTestClient) GetRef(_, _, _ string) (string, error) { return "previous", c.refErr }
func (c *signingTestClient) UpdateRef(_, _, ref, sha string, force bool) error {
	if !force {
		return errors.New("expected branch replacement")
	}
	c.events = append(c.events, "update:"+ref+":"+sha)
	return nil
}
func (c *signingTestClient) MutateWithGitHubAppsSupport(ctx context.Context, m interface{}, input githubql.Input, vars map[string]interface{}, _ string) error {
	return c.graphql.Mutate(ctx, m, input, vars)
}

func TestPublishSignedChanges(t *testing.T) {
	for _, tc := range []struct {
		name, response     string
		refErr             error
		wantErr, published bool
	}{
		{name: "existing branch", response: `{"data":{"createCommitOnBranch":{"commit":{"oid":"signed","signature":{"isValid":true}}}}}`, published: true},
		{name: "new branch", response: `{"data":{"createCommitOnBranch":{"commit":{"oid":"signed","signature":{"isValid":true}}}}}`, refErr: github.NewNotFound(), published: true},
		{name: "unsigned", response: `{"data":{"createCommitOnBranch":{"commit":{"oid":"unsigned","signature":null}}}}`, wantErr: true},
		{name: "invalid", response: `{"data":{"createCommitOnBranch":{"commit":{"oid":"invalid","signature":{"isValid":false}}}}}`, wantErr: true},
		{name: "API failure", response: `{"errors":[{"message":"permission denied"}]}`, wantErr: true},
		{name: "lookup failure", response: `{"data":{"createCommitOnBranch":{"commit":{"oid":"signed","signature":{"isValid":true}}}}}`, refErr: errors.New("permission denied"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(signingResponseHandler{t: t, response: tc.response})
			defer server.Close()
			client := &signingTestClient{graphql: githubql.NewEnterpriseClient(server.URL, server.Client()), refErr: tc.refErr}
			err := publishSignedChanges(client, "org", "repo", "pr-branch", "base", "title", "body", apiFileChanges{Additions: []apiFileAddition{{Path: "config", Contents: "eA=="}}})
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(client.events) < 2 || !strings.HasPrefix(client.events[len(client.events)-1], "delete:heads/prcreator-signing-") {
				t.Fatalf("temporary branch not cleaned: %v", client.events)
			}
			published := false
			for _, event := range client.events {
				if strings.Contains(event, "heads/pr-branch:signed") {
					published = true
				}
			}
			if published != tc.published {
				t.Fatalf("unexpected publication: %v", client.events)
			}
		})
	}
}

func TestSignedCommitsRequireAppBranchMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options PRCreationOptions
	}{
		{name: "fork mode", options: PRCreationOptions{GitHubSignedCommits: true, PRSourceMode: "fork"}},
		{name: "missing app", options: PRCreationOptions{GitHubSignedCommits: true, PRSourceMode: "branch"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.options.Finalize()
			if err == nil || !strings.Contains(err.Error(), "--github-signed-commits requires") {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}

// signingResponseHandler checks the actual GraphQL wire format without GitHub credentials.
type signingResponseHandler struct {
	t        *testing.T
	response string
}

func (h signingResponseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Query     string
		Variables struct {
			Input CreateCommitOnBranchInput `json:"input"`
		} `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		h.t.Error(err)
	}
	if !strings.Contains(request.Query, "$input:CreateCommitOnBranchInput!") || !strings.Contains(request.Query, "signature{isValid}") {
		h.t.Errorf("unexpected mutation: %s", request.Query)
	}
	in := request.Variables.Input
	if in.ExpectedHeadOid != "base" || in.Branch.RepositoryNameWithOwner != "org/repo" || !strings.HasPrefix(in.Branch.RefName, "prcreator-signing-") || in.Message.Headline != "title" || in.Message.Body != "body" || len(in.FileChanges.Additions) != 1 {
		h.t.Errorf("unexpected input: %#v", in)
	}
	if _, err := w.Write([]byte(h.response)); err != nil {
		h.t.Error(err)
	}
}

// runTestGit creates deterministic repository fixtures without global Git configuration.
func runTestGit(t *testing.T, args ...string) {
	t.Helper()
	if b, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, b, err)
	}
}

// writeTestFile writes binary fixture content relative to a test repository.
func writeTestFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
		t.Fatal(err)
	}
}
