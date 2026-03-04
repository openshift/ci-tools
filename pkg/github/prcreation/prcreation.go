package prcreation

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/cmd/generic-autobumper/bumper"
	"sigs.k8s.io/prow/pkg/config/secret"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/labels"
)

// PRCreationOptions holds configuration for creating pull requests.
// Authentication is determined by which flags are provided:
//   - --github-token-path: uses PAT auth, pushes to a fork and creates a cross-repo PR
//   - --github-app-id + --github-app-private-key-path: uses GitHub App auth,
//     pushes the branch directly to the upstream repo and creates a same-repo PR
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
	if o.TokenPath != "" {
		if err := secret.Add(o.TokenPath); err != nil {
			return fmt.Errorf("failed to start secretAgent: %w", err)
		}
	}
	var err error
	o.GithubClient, err = o.GitHubClient(false)
	if err != nil {
		return fmt.Errorf("failed to construct github client: %w", err)
	}

	return nil
}

// PrOptions allows optional parameters to UpsertPR.
type PrOptions struct {
	prBody           string
	matchTitle       string
	additionalLabels []string
	prAssignee       string
	gitCommitMessage string
	skipPRCreation   bool
}

// PrOption is a functional option for configuring PR creation.
type PrOption func(*PrOptions)

// PrBody sets the body/description of the pull request.
func PrBody(prBody string) PrOption {
	return func(args *PrOptions) {
		args.prBody = prBody
	}
}

// GitCommitMessage sets an explicit git commit message different from the PR body.
// This is useful when you wish to provide large markdown information for the PR
// but keep the commit message concise.
func GitCommitMessage(gitCommitMessage string) PrOption {
	return func(args *PrOptions) {
		args.gitCommitMessage = gitCommitMessage
	}
}

// MatchTitle sets a custom title to match existing PRs against.
// When not set, the PR title itself is used for matching.
func MatchTitle(matchTitle string) PrOption {
	return func(args *PrOptions) {
		args.matchTitle = matchTitle
	}
}

// AdditionalLabels sets extra labels to apply to the pull request.
func AdditionalLabels(additionalLabels []string) PrOption {
	return func(args *PrOptions) {
		args.additionalLabels = additionalLabels
	}
}

// PrAssignee sets the comma-separated list of GitHub users to assign the PR to.
func PrAssignee(assignee string) PrOption {
	return func(args *PrOptions) {
		args.prAssignee = assignee
	}
}

// SkipPRCreation skips the actual PR creation after committing and pushing.
func SkipPRCreation() PrOption {
	return func(args *PrOptions) {
		args.skipPRCreation = true
	}
}

// UpsertPR creates or updates a pull request. The PRTitle must be alphanumeric
// except for spaces, as it will be used as the branch name.
//
// The authentication method is auto-detected from the configured flags:
//   - PAT auth (--github-token-path): forks the repo, pushes to the fork,
//     creates a cross-repo PR from fork to upstream
//   - App auth (--github-app-id): pushes the branch directly to the upstream
//     repo, creates a same-repo PR
func (o *PRCreationOptions) UpsertPR(localSourceDir, org, repo, branch, prTitle string, setters ...PrOption) error {
	prArgs := &PrOptions{}
	for _, setter := range setters {
		setter(prArgs)
	}
	if prArgs.matchTitle == "" {
		prArgs.matchTitle = prTitle
	}

	if o.AppID != "" {
		return o.upsertWithAppAuth(localSourceDir, org, repo, branch, prTitle, prArgs)
	}
	return o.upsertWithPAT(localSourceDir, org, repo, branch, prTitle, prArgs)
}

// branchNameFromTitle derives a git branch name from the PR match title.
func branchNameFromTitle(title string) string {
	name := strings.ReplaceAll(strings.ToLower(title), " ", "-")
	name = strings.ReplaceAll(name, ":", "-")
	return name
}

// commitMessage returns the appropriate commit message, preferring the explicit
// git commit message over the PR body.
func commitMessage(prBody, gitCommitMessage string) string {
	if gitCommitMessage != "" {
		return gitCommitMessage
	}
	return prBody
}

// configureGitUser sets the local git user name, email, and disables GPG signing.
func configureGitUser(username string, stdout, stderr io.Writer) error {
	for _, args := range [][]string{
		{"config", "--local", "user.email", fmt.Sprintf("%s@users.noreply.github.com", username)},
		{"config", "--local", "user.name", username},
		{"config", "--local", "commit.gpgsign", "false"},
	} {
		if err := bumper.Call(stdout, stderr, "git", args); err != nil {
			return fmt.Errorf("failed to run git %v: %w", args, err)
		}
	}
	return nil
}

// upsertWithPAT uses a personal access token to fork the repo, commit, push
// to the fork, and create a cross-repo pull request.
func (o *PRCreationOptions) upsertWithPAT(localSourceDir, org, repo, branch, prTitle string, prArgs *PrOptions) error {
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

	sourceBranchName := branchNameFromTitle(prArgs.matchTitle)

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

	if err := configureGitUser(username, stdout, stderr); err != nil {
		return err
	}

	if err := bumper.GitCommitAndPush(
		fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", username, string(token), username, repo),
		sourceBranchName,
		username,
		fmt.Sprintf("%s@users.noreply.github.com", username),
		prTitle+"\n\n"+commitMessage(prArgs.prBody, prArgs.gitCommitMessage),
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

	head := username + ":" + sourceBranchName
	return o.ensurePR(org, repo, branch, prTitle, head, sourceBranchName, prArgs)
}

// upsertWithAppAuth commits the local changes, pushes the branch directly to
// the upstream repository using the GitHub App installation token (via prow's
// GitClientFactory), and creates or updates a same-repo pull request. This
// avoids forking and requires only App auth — no PAT needed.
func (o *PRCreationOptions) upsertWithAppAuth(localSourceDir, org, repo, branch, prTitle string, prArgs *PrOptions) error {
	if err := os.Chdir(localSourceDir); err != nil {
		return fmt.Errorf("failed to chdir into %s: %w", localSourceDir, err)
	}

	changed, err := bumper.HasChanges()
	if err != nil {
		return fmt.Errorf("failed to check for changes: %w", err)
	}

	l := logrus.WithFields(logrus.Fields{"org": org, "repo": repo, "auth": "github-app"})

	if !changed {
		l.Info("No changes, not upserting PR")
		return nil
	}

	user, err := o.GithubClient.BotUser()
	if err != nil {
		return fmt.Errorf("failed to get bot user: %w", err)
	}
	username := user.Login

	sourceBranchName := branchNameFromTitle(prArgs.matchTitle)
	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: secret.Censor}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: secret.Censor}

	if err := configureGitUser(username, stdout, stderr); err != nil {
		return err
	}

	// Create branch, stage, and commit locally
	if err := bumper.Call(stdout, stderr, "git", []string{"checkout", "-B", sourceBranchName}); err != nil {
		return fmt.Errorf("failed to create branch %s: %w", sourceBranchName, err)
	}
	if err := bumper.Call(stdout, stderr, "git", []string{"add", "-A"}); err != nil {
		return fmt.Errorf("failed to stage changes: %w", err)
	}
	commitArgs := []string{
		"commit", "-m", prTitle + "\n\n" + commitMessage(prArgs.prBody, prArgs.gitCommitMessage),
		"--author", fmt.Sprintf("%s <%s@users.noreply.github.com>", username, username),
	}
	if err := bumper.Call(stdout, stderr, "git", commitArgs); err != nil {
		return fmt.Errorf("failed to commit changes: %w", err)
	}

	// Prow's GitClientFactory handles App auth token generation transparently.
	// ClientFromDir wires a central remote URL with the installation token
	// embedded, and PushToCentral pushes directly to the upstream repo.
	gitClientFactory, err := o.GitHubOptions.GitClientFactory("", nil, false, false)
	if err != nil {
		return fmt.Errorf("failed to create git client factory: %w", err)
	}
	defer gitClientFactory.Clean()

	repoClient, err := gitClientFactory.ClientFromDir(org, repo, ".")
	if err != nil {
		return fmt.Errorf("failed to create repo client for %s/%s: %w", org, repo, err)
	}

	l.WithField("branch", sourceBranchName).Info("Pushing branch directly to upstream repo")
	if err := repoClient.PushToCentral(sourceBranchName, true); err != nil {
		return fmt.Errorf("failed to push branch %s to %s/%s: %w", sourceBranchName, org, repo, err)
	}
	l.WithField("branch", fmt.Sprintf("https://github.com/%s/%s/tree/%s", org, repo, sourceBranchName)).Info("Pushed branch to upstream")

	if prArgs.skipPRCreation {
		l.Info("SkipPRCreation is set, not creating a PR")
		return nil
	}

	// For same-repo PRs, head is just the branch name (no user: prefix)
	return o.ensurePR(org, repo, branch, prTitle, sourceBranchName, sourceBranchName, prArgs)
}

// ensurePR creates or updates the pull request and applies labels.
// head is the full head ref (e.g. "user:branch" for cross-repo, or "branch" for same-repo).
// headBranch is the bare branch name used for label/update operations.
func (o *PRCreationOptions) ensurePR(org, repo, branch, prTitle, head, headBranch string, prArgs *PrOptions) error {
	labelsToAdd := prArgs.additionalLabels
	if o.SelfApprove {
		logrus.Infof("Self-approving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}

	prBody := prArgs.prBody + formatAssigneeCC(prArgs.prAssignee)

	// Wrap the client so FindIssues routes through FindIssuesWithOrg,
	// which is required for GitHub App auth.
	prClient := github.Client(&OrgAwareClient{Client: o.GithubClient, Org: org, IsAppAuth: o.AppID != ""})

	return bumper.UpdatePullRequestWithLabels(
		prClient, org, repo, prTitle, prBody,
		head, branch, headBranch,
		true, labelsToAdd, false,
	)
}

func formatAssigneeCC(assigneeCSV string) string {
	if assigneeCSV == "" {
		return ""
	}
	var mentions []string
	for _, a := range strings.Split(assigneeCSV, ",") {
		a = strings.TrimSpace(a)
		if a != "" {
			mentions = append(mentions, "@"+a)
		}
	}
	if len(mentions) == 0 {
		return ""
	}
	return "\n/cc " + strings.Join(mentions, ", ")
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
