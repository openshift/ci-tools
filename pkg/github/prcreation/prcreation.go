package prcreation

import (
	"flag"
	"fmt"
	"os"
	"strings"

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

	if !changed {
		logrus.Info("No changes, not upserting PR")
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

	if err := bumper.GitCommitAndPush(
		fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", username, string(token), org, repo),
		sourceBranchName,
		username,
		fmt.Sprintf("%s@users.noreply.github.com", username),
		prTitle+"\n\n"+prBody,
		stdout,
		stderr,
	); err != nil {
		return fmt.Errorf("failed to push changes: %w", err)
	}

	var labelsToAdd []string
	if o.SelfApprove {
		logrus.Infof("Self-aproving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
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
