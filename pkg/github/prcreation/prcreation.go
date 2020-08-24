package prcreation

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/experiment/autobumper/bumper"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/labels"
)

type PRCreationOptions struct {
	SelfApprove bool
	flagutil.GitHubOptions
	secretAgent  *secret.Agent
	GithubClient github.Client
}

func (o *PRCreationOptions) AddFlags(fs *flag.FlagSet) {
	fs.BoolVar(&o.SelfApprove, "self-approve", false, "If the created PR should be self-approved by adding the lgtm+approved labels")
	o.GitHubOptions.AddFlags(fs)
}

func (o *PRCreationOptions) Finalize() error {
	if err := o.GitHubOptions.Validate(false); err != nil {
		return err
	}
	o.secretAgent = &secret.Agent{}
	if err := o.secretAgent.Start([]string{o.TokenPath}); err != nil {
		return fmt.Errorf("failed to start secretAgent: %w", err)
	}
	var err error
	o.GithubClient, err = o.GitHubClient(o.secretAgent, false)
	if err != nil {
		return fmt.Errorf("failed to construct github client: %w", err)
	}

	return nil
}

// UpsertPR upserts a PR. The PRTitle must be alphanumeric except for spaces, as it will be used as the
// branchname on the bots fork.
func (o *PRCreationOptions) UpsertPR(localSourceDir, org, repo, branch, prTitle, prBody string) error {
	if err := os.Chdir(localSourceDir); err != nil {
		return fmt.Errorf("failed to chdir into %s: %w", localSourceDir, err)
	}

	changed, err := bumper.HasChanges()
	if err != nil {
		return fmt.Errorf("failed to check for changes: %w", err)
	}

	l := logrus.WithFields(logrus.Fields{"org": org, "repo": repo})

	if !changed {
		l.Info("No changes, not upserting PR")
		return nil
	}

	username, err := o.GithubClient.BotName()
	if err != nil {
		return fmt.Errorf("failed to get botname: %w", err)
	}
	token := o.secretAgent.GetSecret(o.TokenPath)
	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: o.secretAgent}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: o.secretAgent}

	sourceBranchName := strings.ReplaceAll(strings.ToLower(prTitle), " ", "-")
	o.GithubClient.SetMax404Retries(0)
	if _, err := o.GithubClient.GetRepo(username, repo); err != nil {
		// Somehow github.IsNotFound doesn't recognize this?
		if !strings.Contains(err.Error(), "status code 404") {
			return fmt.Errorf("unexpected error when getting repo %s/%s: %w", username, repo, err)
		}
		l.Info("Creating fork")
		forkName, err := o.GithubClient.CreateFork(org, repo)
		if err != nil {
			return fmt.Errorf("failed to fork %s/%s: %w", org, repo, err)
		}
		repo = forkName
		if err := waitForRepo(username, repo, o.GithubClient); err != nil {
			return fmt.Errorf("failed to wait for repo %s/%s: %w", username, repo, err)
		}
	}

	// Even when --author is passed on committing, a committer is needed, and that one can not be passed as flag.
	if err := bumper.Call(stdout, stderr, "git", "config", "--local", "user.email", fmt.Sprintf("%s@users.noreply.github.com", username)); err != nil {
		return fmt.Errorf("failed to configure email address: %w", err)
	}
	if err := bumper.Call(stdout, stderr, "git", "config", "--local", "user.name", username); err != nil {
		return fmt.Errorf("failed to configure email address: %w", err)
	}

	if err := bumper.Call(stdout, stderr, "git", "remote", "add", "fork", fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", username, string(token), username, repo)); err != nil {
		return fmt.Errorf("failed to add remote for fork: %w", err)
	}

	fetchStderr := &bytes.Buffer{}
	var remoteTreeRef string
	if err := bumper.Call(stdout, fetchStderr, "git", "fetch", "fork", sourceBranchName); err != nil && !strings.Contains(fetchStderr.String(), fmt.Sprintf("couldn't find remote ref %s", sourceBranchName)) {
		return fmt.Errorf("failed to fetch from fork: %w", err)
	} else {
		var err error
		remoteTreeRef, err = getTreeRef(stderr, fmt.Sprintf("refs/remotes/fork/%s", sourceBranchName))
		if err != nil {
			return fmt.Errorf("failed to get remote tree ref: %w", err)
		}
	}

	if err := bumper.Call(stdout, stderr, "git", "add", "-A"); err != nil {
		return fmt.Errorf("failed to git add: %w", err)
	}
	if err := bumper.Call(stdout, stderr, "git", "commit", "-m", prTitle, "--author", fmt.Sprintf("%s <%s@users.noreply.github.com>", username, username)); err != nil {
		return fmt.Errorf("failed to git commit: %v", err)
	}
	localTreeRef, err := getTreeRef(stderr, "HEAD")
	if err != nil {
		return fmt.Errorf("failed to get local tree ref: %w", err)
	}

	// Avoid doing metadata-only pushes that re-trigger tests and remove lgtm
	if localTreeRef != remoteTreeRef {
		if err := bumper.GitPush("fork", sourceBranchName, stdout, stderr); err != nil {
			return fmt.Errorf("%v", err)
		}
		l.Info("Updated remote branch")
	} else {
		l.Info("Not pushing as up-to-date remote branch already exists")
	}

	var labelsToAdd []string
	if o.SelfApprove {
		l.Infof("Self-aproving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}

	if err := bumper.UpdatePullRequestWithLabels(
		o.GithubClient,
		org,
		repo,
		prTitle,
		prBody,
		prTitle,
		username+":"+sourceBranchName,
		branch,
		true,
		labelsToAdd,
	); err != nil {
		return fmt.Errorf("failed to upsert PR: %w", err)
	}

	return nil
}

func waitForRepo(owner, name string, ghc github.Client) error {
	// Wait for at most 5 minutes for the fork to appear on GitHub.
	// The documentation instructs us to contact support if this
	// takes longer than five minutes.
	after := time.After(6 * time.Minute)
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()

	var ghErr string
	for {
		select {
		case <-tick.C:
			repo, err := ghc.GetRepo(owner, name)
			if err != nil {
				ghErr = fmt.Sprintf(": %v", err)
				logrus.WithError(err).Warn("Error getting bot repository.")
				continue
			}
			ghErr = ""
			if repoExists(owner+"/"+name, []github.Repo{repo.Repo}) {
				return nil
			}
		case <-after:
			return fmt.Errorf("timed out waiting for %s to appear on GitHub%s", owner+"/"+name, ghErr)
		}
	}
}

func repoExists(repo string, repos []github.Repo) bool {
	for _, r := range repos {
		if !r.Fork {
			continue
		}
		if r.FullName == repo {
			return true
		}
	}
	return false
}

func getTreeRef(stderr io.Writer, refname string) (string, error) {
	revParseStdout := &bytes.Buffer{}
	if err := bumper.Call(revParseStdout, stderr, "git", "rev-parse", refname+":"); err != nil {
		return "", fmt.Errorf("failed to parse ref: %w", err)
	}
	fields := strings.Fields(revParseStdout.String())
	if n := len(fields); n < 1 {
		return "", errors.New("got no otput when trying to rev-parse")
	}
	return fields[0], nil
}
