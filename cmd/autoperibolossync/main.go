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

	"sigs.k8s.io/prow/cmd/generic-autobumper/bumper"
	"sigs.k8s.io/prow/pkg/config/secret"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/labels"
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

type arrayFlags []string

func (i *arrayFlags) String() string {
	return strings.Join(*i, ",")
}

func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

type options struct {
	dryRun      bool
	selfApprove bool

	githubLogin     string
	gitName         string
	gitEmail        string
	peribolosConfig string
	releaseRepoPath string
	whitelist       string
	onlyOrg         string
	flattenOrgs     arrayFlags

	flagutil.GitHubOptions
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually create the pull request with github client")
	fs.BoolVar(&o.selfApprove, "self-approve", false, "Self-approve the PR by adding the `approved` and `lgtm` labels. Requires write permissions on the repo.")

	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.gitName, "git-name", "", "The name to use on the git commit. Requires --git-email. If not specified, uses the system default.")
	fs.StringVar(&o.gitEmail, "git-email", "", "The email to use on the git commit. Requires --git-name. If not specified, uses the system default.")

	fs.StringVar(&o.peribolosConfig, "peribolos-config", "", "The peribolos configuration to be updated. Assuming that the file exists in the working directory.")
	fs.StringVar(&o.releaseRepoPath, "release-repo-path", "", "Path to a openshift/release repository directory")
	fs.StringVar(&o.whitelist, "whitelist-file", "", "Path to a whitelisted repositories file")
	fs.StringVar(&o.onlyOrg, "only-org", "", "Only dump config of repos belonging to this organization.")
	fs.Var(&o.flattenOrgs, "flatten-org", "Organizations whose repos should not have org prefix (can be specified multiple times)")

	o.AddFlags(fs)
	o.AllowAnonymous = true
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

	if err := secret.Add(o.GitHubOptions.TokenPath); err != nil {
		logrus.WithError(err).Fatal("Failed to start secrets agent")
	}

	gc, err := o.GitHubOptions.GitHubClient(o.dryRun)
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

	if o.whitelist != "" {
		args = append(args, "--whitelist-file")
		args = append(args, o.whitelist)
	}

	if o.onlyOrg != "" {
		args = append(args, "--only-org", o.onlyOrg)
	}

	for _, org := range o.flattenOrgs {
		args = append(args, "--flatten-org", org)
	}

	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: secret.Censor}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: secret.Censor}
	fullCommand := fmt.Sprintf("%s %s", filepath.Base(cmd), strings.Join(args, " "))

	logrus.Infof("Running: %s", fullCommand)
	if err := bumper.Call(stdout, stderr, cmd, args); err != nil {
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
	if err := bumper.GitCommitAndPush(fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin, string(secret.GetTokenGenerator(o.GitHubOptions.TokenPath)()), o.githubLogin, githubRepo), remoteBranch, o.gitName, o.gitEmail, title, stdout, stderr, o.dryRun); err != nil {
		logrus.WithError(err).Fatal("Failed to push changes.")
	}

	var labelsToAdd []string
	if o.selfApprove {
		logrus.Infof("Self-aproving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}

	// TODO fix the bumper in upstream to retrieve a default branch
	defaultBranch := "master"
	if err := bumper.UpdatePullRequestWithLabels(gc, githubOrg, githubRepo, title, description, o.githubLogin+":"+remoteBranch, defaultBranch, remoteBranch, true, labelsToAdd, o.dryRun); err != nil {
		logrus.WithError(err).Fatal("Failed to use 'master' branch")
		defaultBranch = "main"
		if err := bumper.UpdatePullRequestWithLabels(gc, githubOrg, githubRepo, title, description, o.githubLogin+":"+remoteBranch, defaultBranch, remoteBranch, true, labelsToAdd, o.dryRun); err != nil {
			logrus.WithError(err).Fatal("PR creation failed.")
		}
	}
}
