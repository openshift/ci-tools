package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/cmd/generic-autobumper/bumper"

	"github.com/openshift/ci-tools/pkg/github/prcreation"
)

const (
	githubOrg      = "kubernetes"
	githubRepo     = "test-infra"
	githubLogin    = "openshift-bot"
	githubTeam     = "openshift/test-platform"
	matchTitle     = "Update OpenShift testgrid definitions by auto-testgrid-generator job"
	upstreamBranch = "main"
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
	upstreamBranch    string
	prcreation.PRCreationOptions
	bumper.GitAuthorOptions
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
	fs.StringVar(&o.upstreamBranch, "upstream-branch", upstreamBranch, "The repository branch name where the PR will be created.")

	o.GitAuthorOptions.AddFlags(fs)
	o.PRCreationOptions.GitHubOptions.AddFlags(fs)
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
	if err := o.PRCreationOptions.Finalize(); err != nil {
		logrus.WithError(err).Fatal("failed to finalize PR creation options")
	}

	command := "/usr/bin/testgrid-config-generator"
	arguments := []string{
		"-testgrid-config", o.testgridConfigDir,
		"-release-config", o.releaseConfigDir,
		"-prow-jobs-dir", o.prowJobsDir,
		"-allow-list", o.allowList,
	}

	fullCommand := fmt.Sprintf("%s %s", command, strings.Join(arguments, " "))
	logrus.Infof("Running: %s", fullCommand)
	logrus.WithField("cmd", command).
		WithField("args", arguments).
		Info("running command")

	c := exec.Command(command, arguments...)
	c.Stderr = os.Stderr
	err := c.Run()
	if err != nil {
		logrus.WithError(err).Fatalf("failed to run %s", fullCommand)
	}
	title := fmt.Sprintf("%s at %s", matchTitle, time.Now().Format(time.RFC1123))
	err = o.PRCreationOptions.UpsertPR(o.workingDir, o.githubOrg, githubRepo, o.upstreamBranch, title, prcreation.PrAssignee(o.assign), prcreation.MatchTitle(matchTitle))
	if err != nil {
		logrus.WithError(err).Fatalf("failed to upsert PR")
	}
}
