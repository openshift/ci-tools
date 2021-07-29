package main

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
	"k8s.io/test-infra/prow/labels"

	"github.com/openshift/ci-tools/pkg/promotion"
)

const (
	githubOrg    = "openshift"
	githubRepo   = "release"
	githubLogin  = "openshift-bot"
	githubTeam   = "openshift/openshift-team-developer-productivity-test-platform"
	matchTitle   = "Automate clusterimageset-updater"
	remoteBranch = "auto-clusterimageset-updater"

	poolsDirectory     = "./clusters/hive/pools"
	imagesetsDirectory = "./clusters/hive/pools"
)

type options struct {
	selfApprove bool

	githubLogin string
	gitName     string
	gitEmail    string
	assign      string

	promotion.FutureOptions
	flagutil.GitHubOptions
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	o.FutureOptions.Bind(fs)
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.gitName, "git-name", "", "The name to use on the git commit. Requires --git-email. If not specified, uses the system default.")
	fs.StringVar(&o.gitEmail, "git-email", "", "The email to use on the git commit. Requires --git-name. If not specified, uses the system default.")
	fs.StringVar(&o.assign, "assign", githubTeam, "The github username or group name to assign the created pull request to.")

	fs.BoolVar(&o.selfApprove, "self-approve", false, "Self-approve the PR by adding the `approved` and `lgtm` labels. Requires write permissions on the repo.")
	o.AddFlags(fs)
	o.AllowAnonymous = true
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Errorf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func validateOptions(o options) error {
	if o.githubLogin == "" {
		return fmt.Errorf("--github-login cannot be empty string")
	}
	if (o.gitEmail == "") != (o.gitName == "") {
		return fmt.Errorf("--git-name and --git-email must be specified together")
	}
	if o.assign == "" {
		return fmt.Errorf("--assign is mandatory")
	}
	if err := o.FutureOptions.Validate(); err != nil {
		return err
	}
	return o.GitHubOptions.Validate(!o.Confirm)
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

	gc, err := o.GitHubOptions.GitHubClient(sa, !o.Confirm)
	if err != nil {
		logrus.WithError(err).Fatal("error getting GitHub client")
	}

	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: sa}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: sa}
	author := fmt.Sprintf("%s <%s>", o.gitName, o.gitEmail)

	cmd := "/usr/bin/clusterimageset-updater"
	args := []string{"--pools", poolsDirectory, "--imagesets", imagesetsDirectory}
	fullCommand := fmt.Sprintf("%s %s", cmd, strings.Join(args, " "))

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

	gitCmd := "git"
	if err := bumper.Call(stdout, stderr, gitCmd, []string{"add", "."}...); err != nil {
		logrus.WithError(err).Fatal("failed to 'git add .'")
	}

	commitArgs := []string{"commit", "-m", fullCommand, "--author", author}
	if err := bumper.Call(stdout, stderr, gitCmd, commitArgs...); err != nil {
		logrus.WithError(err).Fatalf("fail to %s %s", gitCmd, strings.Join(commitArgs, " "))
	}

	title := fmt.Sprintf("%s by auto-clusterimageset-updater job at %s", matchTitle, time.Now().Format(time.RFC1123))
	if err := bumper.GitPush(fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin, string(sa.GetTokenGenerator(o.GitHubOptions.TokenPath)()), o.githubLogin, githubRepo), remoteBranch, stdout, stderr, ""); err != nil {
		logrus.WithError(err).Fatal("Failed to push changes.")
	}

	var labelsToAdd []string
	if o.selfApprove {
		logrus.Infof("Self-approving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}
	if err := bumper.UpdatePullRequestWithLabels(gc, githubOrg, githubRepo, title, fmt.Sprintf("/cc @%s", o.assign), o.githubLogin+":"+remoteBranch, "master", remoteBranch, true, labelsToAdd, false); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}
