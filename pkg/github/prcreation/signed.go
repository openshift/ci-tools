package prcreation

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/github"
)

// CreateCommitOnBranchInput is defined locally because the pinned githubv4
// version predates this mutation. Its name determines the GraphQL input type.
type CreateCommitOnBranchInput struct {
	Branch          committableBranch `json:"branch"`
	ExpectedHeadOid string            `json:"expectedHeadOid"`
	Message         apiCommitMessage  `json:"message"`
	FileChanges     apiFileChanges    `json:"fileChanges"`
}

// committableBranch identifies the temporary branch for API commit creation.
type committableBranch struct {
	RepositoryNameWithOwner string `json:"repositoryNameWithOwner"`
	RefName                 string `json:"refName"`
}

// apiCommitMessage separates the commit subject from its body.
type apiCommitMessage struct {
	Headline string `json:"headline"`
	Body     string `json:"body"`
}

// apiFileChanges describes the staged snapshot in GitHub API format.
type apiFileChanges struct {
	Additions []apiFileAddition `json:"additions,omitempty"`
	Deletions []apiFileDeletion `json:"deletions,omitempty"`
}

// apiFileAddition carries a regular file as base64-encoded bytes.
type apiFileAddition struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

// apiFileDeletion identifies a removed file.
type apiFileDeletion struct {
	Path string `json:"path"`
}

// signedCommitMutation requests the signature before publishing the PR branch.
type signedCommitMutation struct {
	CreateCommitOnBranch struct {
		Commit struct {
			Oid       string
			Signature *struct{ IsValid bool }
		}
	} `graphql:"createCommitOnBranch(input: $input)"`
}

// signedCommitClient contains the API operations needed to publish a verified commit.
type signedCommitClient interface {
	CreateRef(string, string, string, string) error
	UpdateRef(string, string, string, string, bool) error
	DeleteRef(string, string, string) error
	GetRef(string, string, string) (string, error)
	MutateWithGitHubAppsSupport(context.Context, interface{}, githubql.Input, map[string]interface{}, string) error
}

// gitOutput reads repository data without invoking a shell.
func gitOutput(args ...string) ([]byte, error) {
	output, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git %v failed: %w", args, err)
	}
	return output, nil
}

// stagedFileChanges reads blobs from the index, preserving binary contents and
// unusual filenames. GraphQL cannot represent symlinks, submodules or mode
// changes, so reject them rather than silently changing the generated tree.
func stagedFileChanges() (apiFileChanges, error) {
	changes := apiFileChanges{}
	raw, err := gitOutput("diff", "--cached", "--raw", "-z", "--no-renames", "HEAD", "--")
	if err != nil {
		return changes, err
	}
	fields := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	if len(raw) == 0 {
		return changes, nil
	}
	for i := 0; i < len(fields); i += 2 {
		if i+1 >= len(fields) {
			return changes, fmt.Errorf("invalid git raw diff")
		}
		metadata := strings.Fields(fields[i])
		if len(metadata) != 5 {
			return changes, fmt.Errorf("invalid git diff metadata %q", fields[i])
		}
		oldMode, newMode, status, path := strings.TrimPrefix(metadata[0], ":"), metadata[1], metadata[4], fields[i+1]
		if (oldMode != "000000" && oldMode != "100644") || (newMode != "000000" && newMode != "100644") {
			return changes, fmt.Errorf("GitHub-signed commit mode supports only non-executable regular files: %q (%s -> %s)", path, oldMode, newMode)
		}
		switch status {
		case "D":
			changes.Deletions = append(changes.Deletions, apiFileDeletion{Path: path})
		case "A", "M":
			content, err := gitOutput("cat-file", "blob", ":"+path)
			if err != nil {
				return changes, err
			}
			changes.Additions = append(changes.Additions, apiFileAddition{Path: path, Contents: base64.StdEncoding.EncodeToString(content)})
		default:
			return changes, fmt.Errorf("unsupported git change %q for %q", status, path)
		}
	}
	return changes, nil
}

// publishSignedCommit stages generated files and publishes their snapshot through GitHub.
func publishSignedCommit(client signedCommitClient, org, repo, branch, headline, body string) error {
	if _, err := gitOutput("add", "-A"); err != nil {
		return err
	}
	changes, err := stagedFileChanges()
	if err != nil {
		return err
	}
	if len(changes.Additions)+len(changes.Deletions) == 0 {
		return nil
	}
	base, err := gitOutput("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	return publishSignedChanges(client, org, repo, branch, strings.TrimSpace(string(base)), headline, body, changes)
}

// publishSignedChanges builds on the checked-out base, as the existing force-push implementation
// does. Only replace the PR branch after GitHub returns a verified signature.
func publishSignedChanges(client signedCommitClient, org, repo, branch, base, headline, body string, changes apiFileChanges) error {
	suffix := make([]byte, 12)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("generate temporary branch name: %w", err)
	}
	temporary := fmt.Sprintf("prcreator-signing-%x", suffix)
	if err := client.CreateRef(org, repo, "refs/heads/"+temporary, base); err != nil {
		return fmt.Errorf("create temporary signing branch: %w", err)
	}
	defer deleteSigningBranch(client, org, repo, temporary)

	input := CreateCommitOnBranchInput{
		Branch:          committableBranch{RepositoryNameWithOwner: org + "/" + repo, RefName: temporary},
		ExpectedHeadOid: base,
		Message:         apiCommitMessage{Headline: headline, Body: body},
		FileChanges:     changes,
	}
	mutation := signedCommitMutation{}
	if err := client.MutateWithGitHubAppsSupport(context.Background(), &mutation, input, nil, org); err != nil {
		return fmt.Errorf("create signed commit: %w", err)
	}
	commit := mutation.CreateCommitOnBranch.Commit
	if commit.Oid == "" || commit.Signature == nil || !commit.Signature.IsValid {
		return fmt.Errorf("GitHub did not return a verified signature; PR branch was not updated")
	}
	if _, err := client.GetRef(org, repo, "heads/"+branch); err != nil {
		if !github.IsNotFound(err) {
			return fmt.Errorf("get PR branch: %w", err)
		}
		return client.CreateRef(org, repo, "refs/heads/"+branch, commit.Oid)
	}
	return client.UpdateRef(org, repo, "heads/"+branch, commit.Oid, true)
}

// deleteSigningBranch reports cleanup failures without changing publication results.
func deleteSigningBranch(client signedCommitClient, org, repo, branch string) {
	if err := client.DeleteRef(org, repo, "heads/"+branch); err != nil {
		logrus.WithError(err).WithField("branch", branch).Warn("Failed to delete temporary signing branch")
	}
}
