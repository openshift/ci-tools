package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/experiment/autobumper/bumper"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
)

const (
	githubOrg      = "openshift"
	githubRepo     = "config"
	githubLogin    = "openshift-bot"
	remoteBranch   = "auto-peribolos-sync"
	destinationOrg = "openshift-priv"
	matchTitle     = "Automate peribolos configuration sync"
	description    = "Updates the repositories of the openshift-priv organization"
)

type options struct {
	dryRun          bool
	githubLogin     string
	gitName         string
	gitEmail        string
	peribolosConfig string
	releaseRepoPath string

	flagutil.GitHubOptions
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually create the pull request with github client")
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.gitName, "git-name", "", "The name to use on the git commit. Requires --git-email. If not specified, uses the system default.")
	fs.StringVar(&o.gitEmail, "git-email", "", "The email to use on the git commit. Requires --git-name. If not specified, uses the system default.")

	fs.StringVar(&o.peribolosConfig, "peribolos-config", "", "The peribolos configuration to be updated. Assuming that the file exists in the working directory.")
	fs.StringVar(&o.releaseRepoPath, "release-repo-path", "", "Path to a openshift/release repository directory")

	o.AddFlagsWithoutDefaultGitHubTokenPath(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Errorf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func validateOptions(o options) error {
	if o.githubLogin == "" {
		return errors.New("--github-login is not specified")
	}
	if (o.gitEmail == "") != (o.gitName == "") {
		return errors.New("--git-name and --git-email must be specified together")
	}
	if len(o.releaseRepoPath) == 0 {
		return errors.New("--release-repo-path is not specified")
	}
	if o.peribolosConfig == "" {
		return errors.New("--peribolos-config is not specified")
	}
	return o.GitHubOptions.Validate(o.dryRun)
}

func main() {
	o := parseOptions()
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("Invalid arguments.")
	}

	sa := &secret.Agent{}
	if err := sa.Start([]string{o.GitHubOptions.TokenPath}); err != nil {
		logrus.WithError(err).Fatal("Failed to start secrets agent")
	}

	gc, err := o.GitHubOptions.GitHubClient(sa, o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("error getting GitHub client")
	}

	cmd := "/usr/bin/private-org-peribolos-sync"
	args := []string{
		"--destination-org", destinationOrg,
		"--peribolos-config", o.peribolosConfig,
		"--release-repo-path", o.releaseRepoPath,
		"--github-token-path", o.TokenPath,
	}

	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: sa}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: sa}
	fullCommand := fmt.Sprintf("%s %s", filepath.Base(cmd), strings.Join(args, " "))

	logrus.Infof("Running: %s", fullCommand)
	if err := bumper.Call(stdout, stderr, cmd, args...); err != nil {
		logrus.WithError(err).Fatalf("failed to run %s", fullCommand)
	}

	changed, err := bumper.HasChanges()
	if err != nil {
		logrus.WithError(err).Fatal("error occurred when checking changes")
	}

	if !changed {
		logrus.WithField("command", fullCommand).Info("No changes to commit")
		os.Exit(0)
	}

	title := fmt.Sprintf("%s %s", matchTitle, time.Now().Format(time.RFC1123))
	if err := bumper.GitCommitAndPush(fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin, string(sa.GetTokenGenerator(o.GitHubOptions.TokenPath)()), o.githubLogin, githubRepo), remoteBranch, o.gitName, o.gitEmail, title, stdout, stderr); err != nil {
		logrus.WithError(err).Fatal("Failed to push changes.")
	}

	source := fmt.Sprintf("%s:%s", o.githubLogin, remoteBranch)
	if err := bumper.UpdatePullRequest(gc, githubOrg, githubRepo, title, description, matchTitle, source, "master"); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}
