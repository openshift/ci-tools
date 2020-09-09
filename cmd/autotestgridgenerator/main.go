package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/experiment/autobumper/bumper"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
)

const (
	githubOrg    = "kubernetes"
	githubRepo   = "test-infra"
	githubLogin  = "openshift-bot"
	githubTeam   = "openshift/openshift-team-developer-productivity-test-platform"
	matchTitle   = "Update OpenShift testgrid definitions"
	remoteBranch = "auto-testgrid-generator"
)

type options struct {
	assign            string
	workingDir        string
	testgridConfigDir string
	releaseConfigDir  string
	prowJobsDir       string
	allowList         string
	githubLogin       string
	githubOrg         string
	bumper.GitAuthorOptions
	flagutil.GitHubOptions
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.assign, "assign", githubTeam, "The github username or group name to assign the created pull request to. Set to DPTP by default")
	fs.StringVar(&o.workingDir, "working-dir", ".", "Working directory for git")
	fs.StringVar(&o.testgridConfigDir, "testgrid-config", "", "The directory where the testgrid output will be stored")
	fs.StringVar(&o.releaseConfigDir, "release-config", "", "The directory of release config files")
	fs.StringVar(&o.prowJobsDir, "prow-jobs-dir", "", "The directory where prow-job configs are stored")
	fs.StringVar(&o.allowList, "allow-list", "", "File containing release-type information to override the defaults")
	fs.StringVar(&o.githubOrg, "github-org", githubOrg, "The github org to use for testing with a dummy repository.")

	o.GitAuthorOptions.AddFlags(fs)
	o.GitHubOptions.AddFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Errorf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func validateOptions(o options) error {
	if err := o.GitAuthorOptions.Validate(); err != nil {
		return err
	}
	return o.GitHubOptions.Validate(false)
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

	gc, err := o.GitHubOptions.GitHubClient(sa, false)
	if err != nil {
		logrus.WithError(err).Fatal("error getting GitHub client")
	}

	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: sa}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: sa}
	author := fmt.Sprintf("%s <%s>", o.GitName, o.GitEmail)

	command := "/usr/bin/testgrid-config-generator"
	arguments := []string{
		"-testgrid-config", o.testgridConfigDir,
		"-release-config", o.releaseConfigDir,
		"-prow-jobs-dir", o.prowJobsDir,
		"-allow-list", o.allowList,
	}
	committed, err := bumper.RunAndCommitIfNeeded(stdout, stderr, author, command, arguments, o.workingDir)
	if err != nil {
		logrus.WithError(err).Fatal("failed to run command and commit the changes")
	}

	if !committed {
		logrus.Info("no new commits, exiting ...")
		os.Exit(0)
	}

	title := fmt.Sprintf("%s by auto-testgrid-generator job at %s", matchTitle, time.Now().Format(time.RFC1123))
	if err := bumper.GitPush(fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin, string(sa.GetTokenGenerator(o.GitHubOptions.TokenPath)()), o.githubLogin, githubRepo), remoteBranch, stdout, stderr, o.workingDir); err != nil {
		logrus.WithError(err).Fatal("Failed to push changes.")
	}

	if err := bumper.UpdatePullRequest(gc, o.githubOrg, githubRepo, title, fmt.Sprintf("/cc @%s", o.assign),
		matchTitle, o.githubLogin+":"+remoteBranch, "master", true); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}
