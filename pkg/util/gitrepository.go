package util

import (
	"fmt"
	"os/exec"
	"strings"
)

func getRemoteBranchCommitShaWithExecFunc(org, repo, branch string, execFunc func(
	string, string) ([]byte, error)) (string, error) {
	gitRepo := fmt.Sprintf("https://github.com/%s/%s.git", org, repo)
	out, err := execFunc(gitRepo, branch)
	if err != nil {
		return "", fmt.Errorf("'git ls-remote %s %s' failed with '%w'", gitRepo, branch, err)
	}
	resolved := strings.Split(strings.Split(string(out), "\n")[0], "\t")
	sha := resolved[0]
	if len(sha) == 0 {
		return "", fmt.Errorf("ref '%s' does not point to any commit in '%s/%s'", branch, org, repo)
	}
	// sanity check that regular refs are fully determined
	// copied over from https://github.com/openshift/ci-tools/commit/55e2aafd403d5e2726331b106d0c0dcc4b9c835c
	if strings.HasPrefix(resolved[1], "refs/heads/") && !strings.HasPrefix(branch, "refs/heads/") {
		if resolved[1] != ("refs/heads/" + branch) {
			trimmed := resolved[1][len("refs/heads/"):]
			// we could fix this for the user, but better to require them to be explicit
			return "", fmt.Errorf("ref '%s' does not point to any commit in '%s/%s' (did you mean '%s'?)", branch, org, repo, trimmed)
		}
	}
	return sha, nil
}

func GetRemoteBranchCommitSha(org, repo, branch string) (string, error) {
	return getRemoteBranchCommitShaWithExecFunc(org, repo, branch, func(gitRepo, branch string) ([]byte, error) {
		return exec.Command("git", "ls-remote", gitRepo, branch).Output()
	})
}
