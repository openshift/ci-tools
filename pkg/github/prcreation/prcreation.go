package prcreation

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/cmd/generic-autobumper/bumper"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/labels"
)

type PRCreationOptions struct {
	SelfApprove bool
	flagutil.GitHubOptions
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
	if err := secret.Add(o.TokenPath); err != nil {
		return fmt.Errorf("failed to start secretAgent: %w", err)
	}
	var err error
	o.GithubClient, err = o.GitHubClient(false)
	if err != nil {
		return fmt.Errorf("failed to construct github client: %w", err)
	}

	return nil
}

// PrOptions allows optional parameters to upsertPR
type PrOptions struct {
	prBody           string
	matchTitle       string
	additionalLabels []string
	prAssignee       string
	gitCommitMessage string
	skipPRCreation   bool
}

// PrOption is the type for Optional Parameters
type PrOption func(*PrOptions)

// PrBody is the wrapper to pass in PrBody as a parameter
func PrBody(prBody string) PrOption {
	return func(args *PrOptions) {
		args.prBody = prBody
	}
}

// GitCommitMessage is the wrapper to pass in PrCommitMessage that's different from the PrBody
// This is useful when you wish to provide large markdown information for the PR, but wish to keep the commit simple.
func GitCommitMessage(gitCommitMessage string) PrOption {
	return func(args *PrOptions) {
		args.gitCommitMessage = gitCommitMessage
	}
}

// MatchTitle is the wrapper to pass in MatchTitle as a parameter
func MatchTitle(matchTitle string) PrOption {
	return func(args *PrOptions) {
		args.matchTitle = matchTitle
	}
}

func AdditionalLabels(additionalLabels []string) PrOption {
	return func(args *PrOptions) {
		args.additionalLabels = additionalLabels
	}
}

// PrAssignee is the user to whom the PR is assigned
func PrAssignee(assignee string) PrOption {
	return func(args *PrOptions) {
		args.prAssignee = assignee
	}
}

// SkipPRCreation skips the actual pr creation after
// committing and pushing
func SkipPRCreation() PrOption {
	return func(args *PrOptions) {
		args.skipPRCreation = true
	}
}

// UpsertPR upserts a PR. The PRTitle must be alphanumeric except for spaces, as it will be used as the
// branchname on the bots fork.
func (o *PRCreationOptions) UpsertPR(localSourceDir, org, repo, branch, prTitle string, setters ...PrOption) error {
	prArgs := &PrOptions{}
	for _, setter := range setters {
		setter(prArgs)
	}
	if prArgs.matchTitle == "" {
		prArgs.matchTitle = prTitle
	}
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

	user, err := o.GithubClient.BotUser()
	if err != nil {
		return fmt.Errorf("failed to get botname: %w", err)
	}
	username := user.Login
	token := secret.GetSecret(o.TokenPath)
	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: secret.Censor}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: secret.Censor}

	sourceBranchName := strings.ReplaceAll(strings.ToLower(prArgs.matchTitle), " ", "-")
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
	if err := bumper.Call(stdout, stderr, "git", "config", "--local", "commit.gpgsign", "false"); err != nil {
		return fmt.Errorf("failed to configure disabling gpg signing: %w", err)
	}

	commitMessage := prArgs.prBody
	if prArgs.gitCommitMessage != "" {
		commitMessage = prArgs.gitCommitMessage
	}

	if err := bumper.GitCommitAndPush(
		fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", username, string(token), username, repo),
		sourceBranchName,
		username,
		fmt.Sprintf("%s@users.noreply.github.com", username),
		prTitle+"\n\n"+commitMessage,
		stdout,
		stderr,
		false,
	); err != nil {
		return fmt.Errorf("failed to push changes: %w", err)
	}
	l.WithField("branch", fmt.Sprintf("https://github.com/%s/%s/tree/%s", username, repo, sourceBranchName)).Info("Committed and pushed")

	if prArgs.skipPRCreation {
		l.Info("SkipPRCreation is set, not creating a PR")
		return nil
	}

	labelsToAdd := prArgs.additionalLabels
	if o.SelfApprove {
		l.Infof("Self-aproving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}

	if err := bumper.UpdatePullRequestWithLabels(
		o.GithubClient,
		org,
		repo,
		prTitle,
		prArgs.prBody+"\n/cc @"+prArgs.prAssignee,
		username+":"+sourceBranchName,
		branch,
		sourceBranchName,
		true,
		labelsToAdd,
		false,
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
