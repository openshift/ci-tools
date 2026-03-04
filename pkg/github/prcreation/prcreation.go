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

type PRCreationOptions struct {
	SelfApprove  bool
	PRSourceMode string
	flagutil.GitHubOptions
	GithubClient github.Client
}

type upsertContext struct {
	username         string
	sourceBranchName string
	stdout           io.Writer
	stderr           io.Writer
}

func (o *PRCreationOptions) AddFlags(fs *flag.FlagSet) {
	fs.BoolVar(&o.SelfApprove, "self-approve", false, "If the created PR should be self-approved by adding the lgtm+approved labels")
	fs.StringVar(&o.PRSourceMode, "pr-source-mode", "fork", "How to push PR source: fork or branch")
	o.GitHubOptions.AddFlags(fs)
}

func (o *PRCreationOptions) Finalize() error {
	switch o.PRSourceMode {
	case "fork", "branch":
	default:
		return fmt.Errorf("invalid --pr-source-mode %q, expected one of: fork, branch", o.PRSourceMode)
	}
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

// UpsertPR creates or updates a pull request. The PRTitle must be alphanumeric
// except for spaces, as it will be used as the branch name.
func (o *PRCreationOptions) UpsertPR(localSourceDir, org, repo, branch, prTitle string, setters ...PrOption) error {
	prArgs := &PrOptions{}
	for _, setter := range setters {
		setter(prArgs)
	}
	if prArgs.matchTitle == "" {
		prArgs.matchTitle = prTitle
	}

	if o.PRSourceMode == "fork" {
		if o.TokenPath == "" {
			return fmt.Errorf("--pr-source-mode=fork requires --github-token-path")
		}
		return o.upsertWithPAT(localSourceDir, org, repo, branch, prTitle, prArgs)
	}
	if o.PRSourceMode == "branch" {
		if o.AppID == "" {
			return fmt.Errorf("--pr-source-mode=branch requires --github-app-id and --github-app-private-key-path")
		}
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

func (o *PRCreationOptions) prepareUpsert(localSourceDir, prTitle string, l *logrus.Entry) (*upsertContext, error) {
	if err := os.Chdir(localSourceDir); err != nil {
		return nil, fmt.Errorf("failed to chdir into %s: %w", localSourceDir, err)
	}
	if prTitle == "" {
		return nil, fmt.Errorf("pr title must not be empty")
	}

	changed, err := bumper.HasChanges()
	if err != nil {
		return nil, fmt.Errorf("failed to check for changes: %w", err)
	}
	if !changed {
		l.Info("No changes, not upserting PR")
		return nil, nil
	}

	user, err := o.GithubClient.BotUser()
	if err != nil {
		return nil, fmt.Errorf("failed to get bot user: %w", err)
	}
	username := user.Login

	ctx := &upsertContext{
		username:         username,
		sourceBranchName: branchNameFromTitle(prTitle),
		stdout:           bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: secret.Censor},
		stderr:           bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: secret.Censor},
	}
	if err := configureGitUser(username, ctx.stdout, ctx.stderr); err != nil {
		return nil, err
	}
	return ctx, nil
}

// upsertWithPAT uses a personal access token to fork the repo, commit, push
// to the fork, and create a cross-repo pull request.
func (o *PRCreationOptions) upsertWithPAT(localSourceDir, org, repo, branch, prTitle string, prArgs *PrOptions) error {
	l := logrus.WithFields(logrus.Fields{"org": org, "repo": repo})
	ctx, err := o.prepareUpsert(localSourceDir, prTitle, l)
	if err != nil {
		return err
	}
	if ctx == nil {
		return nil
	}

	token := secret.GetSecret(o.TokenPath)

	o.GithubClient.SetMax404Retries(0)
	if _, err := o.GithubClient.GetRepo(ctx.username, repo); err != nil {
		// Somehow github.IsNotFound doesn't recognize this?
		if !strings.Contains(err.Error(), "status code 404") {
			return fmt.Errorf("unexpected error when getting repo %s/%s: %w", ctx.username, repo, err)
		}
		l.Info("Creating fork")
		forkName, err := o.GithubClient.CreateFork(org, repo)
		if err != nil {
			return fmt.Errorf("failed to fork %s/%s: %w", org, repo, err)
		}
		repo = forkName
		if err := waitForRepo(ctx.username, repo, o.GithubClient); err != nil {
			return fmt.Errorf("failed to wait for repo %s/%s: %w", ctx.username, repo, err)
		}
	}

	if err := bumper.GitCommitAndPush(
		fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", ctx.username, string(token), ctx.username, repo),
		ctx.sourceBranchName,
		ctx.username,
		fmt.Sprintf("%s@users.noreply.github.com", ctx.username),
		prTitle+"\n\n"+commitMessage(prArgs.prBody, prArgs.gitCommitMessage),
		ctx.stdout,
		ctx.stderr,
		false,
	); err != nil {
		return fmt.Errorf("failed to push changes: %w", err)
	}
	l.WithField("branch", fmt.Sprintf("https://github.com/%s/%s/tree/%s", ctx.username, repo, ctx.sourceBranchName)).Info("Committed and pushed")

	if prArgs.skipPRCreation {
		l.Info("SkipPRCreation is set, not creating a PR")
		return nil
	}

	head := ctx.username + ":" + ctx.sourceBranchName
	return o.ensurePR(org, repo, branch, prTitle, head, ctx.sourceBranchName, false, prArgs)
}

// upsertWithAppAuth commits the local changes, pushes the branch directly to
// the upstream repository using the GitHub App installation token (via prow's
// GitClientFactory), and creates or updates a same-repo pull request. This
// avoids forking and requires only App auth — no PAT needed.
func (o *PRCreationOptions) upsertWithAppAuth(localSourceDir, org, repo, branch, prTitle string, prArgs *PrOptions) error {
	l := logrus.WithFields(logrus.Fields{"org": org, "repo": repo, "auth": "github-app"})
	ctx, err := o.prepareUpsert(localSourceDir, prTitle, l)
	if err != nil {
		return err
	}
	if ctx == nil {
		return nil
	}

	// Create branch, stage, and commit locally
	if err := bumper.Call(ctx.stdout, ctx.stderr, "git", []string{"checkout", "-B", ctx.sourceBranchName}); err != nil {
		return fmt.Errorf("failed to create branch %s: %w", ctx.sourceBranchName, err)
	}
	if err := bumper.Call(ctx.stdout, ctx.stderr, "git", []string{"add", "-A"}); err != nil {
		return fmt.Errorf("failed to stage changes: %w", err)
	}
	commitArgs := []string{
		"commit", "-m", prTitle + "\n\n" + commitMessage(prArgs.prBody, prArgs.gitCommitMessage),
		"--author", fmt.Sprintf("%s <%s@users.noreply.github.com>", ctx.username, ctx.username),
	}
	if err := bumper.Call(ctx.stdout, ctx.stderr, "git", commitArgs); err != nil {
		return fmt.Errorf("failed to commit changes: %w", err)
	}

	// Prow's GitClientFactory handles App auth token generation transparently.
	// ClientFromDir wires a central remote URL with the installation token
	// embedded, and PushToCentral pushes directly to the upstream repo.
	gitClientFactory, err := o.GitHubOptions.GitClientFactory("", nil, false, false)
	if err != nil {
		return fmt.Errorf("failed to create git client factory: %w", err)
	}
	defer func() {
		if cleanErr := gitClientFactory.Clean(); cleanErr != nil {
			logrus.WithError(cleanErr).Warn("Failed to clean git client factory")
		}
	}()

	repoClient, err := gitClientFactory.ClientFromDir(org, repo, ".")
	if err != nil {
		return fmt.Errorf("failed to create repo client for %s/%s: %w", org, repo, err)
	}

	l.WithField("branch", ctx.sourceBranchName).Info("Pushing branch directly to upstream repo")
	if err := repoClient.PushToCentral(ctx.sourceBranchName, true); err != nil {
		return fmt.Errorf("failed to push branch %s to %s/%s: %w", ctx.sourceBranchName, org, repo, err)
	}
	l.WithField("branch", fmt.Sprintf("https://github.com/%s/%s/tree/%s", org, repo, ctx.sourceBranchName)).Info("Pushed branch to upstream")

	if prArgs.skipPRCreation {
		l.Info("SkipPRCreation is set, not creating a PR")
		return nil
	}

	// For same-repo PRs, head is just the branch name (no user: prefix)
	return o.ensurePR(org, repo, branch, prTitle, ctx.sourceBranchName, ctx.sourceBranchName, true, prArgs)
}

// ensurePR creates or updates the pull request and applies labels.
// head is the full head ref (e.g. "user:branch" for cross-repo, or "branch" for same-repo).
// headBranch is the bare branch name used for label/update operations.
func (o *PRCreationOptions) ensurePR(org, repo, branch, prTitle, head, headBranch string, isAppAuth bool, prArgs *PrOptions) error {
	labelsToAdd := prArgs.additionalLabels
	if o.SelfApprove {
		logrus.Infof("Self-approving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}

	prBody := prArgs.prBody + formatAssigneeCC(prArgs.prAssignee)

	// Wrap the client so FindIssues routes through FindIssuesWithOrg,
	// which is required for GitHub App auth.
	prClient := github.Client(&OrgAwareClient{Client: o.GithubClient, Org: org, IsAppAuth: isAppAuth})

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
