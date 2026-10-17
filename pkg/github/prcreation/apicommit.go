package prcreation

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"

	githubql "github.com/shurcooL/githubv4"

	"sigs.k8s.io/prow/pkg/github"
)

// regularFileMode is the only git file mode createCommitOnBranch can write.
// It cannot set the executable bit or create symlinks or submodules.
const regularFileMode = "100644"

// CreateCommitOnBranchInput mirrors the GitHub GraphQL input of the same
// name. The vendored githubv4 predates the mutation, and the GraphQL client
// derives the variable type from the Go type name, so the name must match.
type CreateCommitOnBranchInput struct {
	Branch          CommittableBranch `json:"branch"`
	ExpectedHeadOid string            `json:"expectedHeadOid"`
	Message         CommitMessage     `json:"message"`
	FileChanges     FileChanges       `json:"fileChanges"`
}

type CommittableBranch struct {
	RepositoryNameWithOwner string `json:"repositoryNameWithOwner"`
	BranchName              string `json:"branchName"`
}

type CommitMessage struct {
	Headline string `json:"headline"`
	Body     string `json:"body,omitempty"`
}

type FileChanges struct {
	Additions []FileAddition `json:"additions,omitempty"`
	Deletions []FileDeletion `json:"deletions,omitempty"`
}

type FileAddition struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

type FileDeletion struct {
	Path string `json:"path"`
}

// apiCommitClient is the subset of github.Client needed to create a commit
// through the API.
type apiCommitClient interface {
	GetRef(org, repo, ref string) (string, error)
	CreateRef(org, repo, ref, sha string) error
	UpdateRef(org, repo, ref, sha string, force bool) error
	MutateWithGitHubAppsSupport(ctx context.Context, m interface{}, input githubql.Input, vars map[string]interface{}, org string) error
}

// commitViaAPI creates a single commit on branch containing the staged
// changes in dir, using the createCommitOnBranch GraphQL mutation instead of
// git push. GitHub signs commits created this way when the caller is
// authenticated as a GitHub App, so they show as verified.
//
// branch is reset to the local HEAD first, which matches the force push used
// by the git-based flow. Returns the OID of the new commit.
func commitViaAPI(ctx context.Context, gc apiCommitClient, dir, org, repo, branch, headline, body string) (string, error) {
	baseSHA, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to resolve HEAD: %w", err)
	}
	baseSHA = strings.TrimSpace(baseSHA)

	changes, err := stagedFileChanges(dir)
	if err != nil {
		return "", err
	}

	if err := resetBranch(gc, org, repo, branch, baseSHA); err != nil {
		return "", err
	}

	input := CreateCommitOnBranchInput{
		Branch: CommittableBranch{
			RepositoryNameWithOwner: org + "/" + repo,
			BranchName:              branch,
		},
		ExpectedHeadOid: baseSHA,
		Message:         CommitMessage{Headline: headline, Body: body},
		FileChanges:     changes,
	}
	var m struct {
		CreateCommitOnBranch struct {
			Commit struct {
				Oid githubql.GitObjectID
			}
		} `graphql:"createCommitOnBranch(input: $input)"`
	}
	if err := gc.MutateWithGitHubAppsSupport(ctx, &m, input, nil, org); err != nil {
		return "", fmt.Errorf("failed to create commit on %s/%s:%s: %w", org, repo, branch, err)
	}
	return string(m.CreateCommitOnBranch.Commit.Oid), nil
}

// resetBranch points branch at sha, creating it if it does not exist yet.
func resetBranch(gc apiCommitClient, org, repo, branch, sha string) error {
	current, err := gc.GetRef(org, repo, "heads/"+branch)
	if err != nil {
		if !github.IsNotFound(err) {
			return fmt.Errorf("failed to get branch %s: %w", branch, err)
		}
		if err := gc.CreateRef(org, repo, "refs/heads/"+branch, sha); err != nil {
			return fmt.Errorf("failed to create branch %s: %w", branch, err)
		}
		return nil
	}
	if current == sha {
		return nil
	}
	if err := gc.UpdateRef(org, repo, "heads/"+branch, sha, true); err != nil {
		return fmt.Errorf("failed to reset branch %s to %s: %w", branch, sha, err)
	}
	return nil
}

// stagedFileChanges converts the changes staged in dir (relative to HEAD)
// into createCommitOnBranch file changes. Changes the API cannot express,
// such as executable files or symlinks, are rejected rather than silently
// committed with the wrong mode.
func stagedFileChanges(dir string) (FileChanges, error) {
	out, err := gitOutput(dir, "diff", "--cached", "--raw", "--no-renames", "-z", "HEAD")
	if err != nil {
		return FileChanges{}, fmt.Errorf("failed to list staged changes: %w", err)
	}

	var changes FileChanges
	// Each entry is ":<old mode> <new mode> <old sha> <new sha> <status>\x00<path>\x00".
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		meta, path := strings.Fields(strings.TrimPrefix(fields[i], ":")), fields[i+1]
		if len(meta) != 5 {
			return FileChanges{}, fmt.Errorf("unexpected git diff output %q", fields[i])
		}
		newMode, status := meta[1], meta[4]
		if status == "D" {
			changes.Deletions = append(changes.Deletions, FileDeletion{Path: path})
			continue
		}
		if newMode != regularFileMode {
			return FileChanges{}, fmt.Errorf("cannot commit %s through the API: file mode %s is not supported", path, newMode)
		}
		contents, err := gitShowStaged(dir, path)
		if err != nil {
			return FileChanges{}, err
		}
		changes.Additions = append(changes.Additions, FileAddition{
			Path:     path,
			Contents: base64.StdEncoding.EncodeToString(contents),
		})
	}
	return changes, nil
}

func gitShowStaged(dir, path string) ([]byte, error) {
	cmd := exec.Command("git", "show", ":"+path)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to read staged %s: %w: %s", path, err, stderr.String())
	}
	return out, nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return string(out), nil
}
