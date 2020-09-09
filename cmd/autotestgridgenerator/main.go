package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/experiment/autobumper/bumper"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/labels"
)

const (
	githubOrg    = "kubernetes"
	githubRepo   = "test-infra"
	githubLogin  = "openshift-bot"
	githubTeam   = "openshift/openshift-team-developer-productivity-test-platform"
	matchTitle   = "Automate testgrid generator"
	remoteBranch = "auto-testgrid-generator"
)

type options struct {
	selfApprove bool

	githubLogin       string
	gitName           string
	gitEmail          string
	targetDir         string
	assign            string
	testgridConfigDir string
	releaseConfigDir  string
	prowJobsDir       string
	allowList         string

	flagutil.GitHubOptions
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.gitName, "git-name", "", "The name to use on the git commit. Requires --git-email. If not specified, uses the system default.")
	fs.StringVar(&o.gitEmail, "git-email", "", "The email to use on the git commit. Requires --git-name. If not specified, uses the system default.")
	fs.StringVar(&o.targetDir, "target-dir", "", "The directory containing the target repo.")
	fs.StringVar(&o.assign, "assign", githubTeam, "The github username or group name to assign the created pull request to.")
	fs.StringVar(&o.testgridConfigDir, "testgrid-config", "../../../../kubernetes/test-infra/config/testgrids/openshift", "The directory where the testgrid output will be stored")
	fs.StringVar(&o.releaseConfigDir, "release-config", "../../../release/core-services/release-controller/_releases", "The directory of release config files")
	fs.StringVar(&o.prowJobsDir, "prow-jobs-dir", "../../../release/ci-operator/jobs", "The directory where prow-job configs are stored")
	fs.StringVar(&o.allowList, "allow-list", "../../../release/core-services/testgrid-config-generator/_allow-list.yaml", "File containing release-type information to override the defaults")

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
	if o.targetDir == "" {
		return fmt.Errorf("--target-dir is mandatory")
	}
	if o.assign == "" {
		return fmt.Errorf("--assign is mandatory")
	}
	return o.GitHubOptions.Validate(false)
}

func runAndCommitIfNeeded(stdout, stderr io.Writer, author, cmd string, args []string) (bool, error) {
	fullCommand := fmt.Sprintf("%s %s", filepath.Base(cmd), strings.Join(args, " "))

	logrus.Infof("Running: %s", fullCommand)
	if err := bumper.Call(stdout, stderr, cmd, args...); err != nil {
		return false, fmt.Errorf("failed to run %s: %w", fullCommand, err)
	}

	changed, err := bumper.HasChanges()
	if err != nil {
		return false, fmt.Errorf("error occurred when checking changes: %w", err)
	}

	if !changed {
		logrus.WithField("command", fullCommand).Info("No changes to commit")
		return false, nil
	}

	gitCmd := "git"
	if err := bumper.Call(stdout, stderr, gitCmd, []string{"add", "."}...); err != nil {
		return false, fmt.Errorf("failed to 'git add .': %w", err)
	}

	commitArgs := []string{"commit", "-m", fullCommand, "--author", author}
	if err := bumper.Call(stdout, stderr, gitCmd, commitArgs...); err != nil {
		return false, fmt.Errorf("fail to %s %s: %w", gitCmd, strings.Join(commitArgs, " "), err)
	}

	return true, nil
}

func main() {
	o := parseOptions()
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("Invalid arguments.")
	}
	// targetDir needs to be the test-infra directory
	logrus.Infof("Changing working directory to '%s' ...", o.targetDir)
	if err := os.Chdir(o.targetDir); err != nil {
		logrus.WithError(err).Fatal("Failed to change to root dir")
	}

	sa := &secret.Agent{}
	if err := sa.Start([]string{o.GitHubOptions.TokenPath}); err != nil {
		logrus.WithError(err).Fatal("Failed to start secrets agent")
	}

	gc, err := o.GitHubOptions.GitHubClient(sa, false)
	if err != nil {
		logrus.WithError(err).Fatal("error getting GitHub client")
	}

	steps := []struct {
		command   string
		arguments []string
	}{
		{
			command: "/usr/bin/testgrid-config-generator",
			arguments: []string{
				"-testgrid-config", o.testgridConfigDir, // "../../../../k8s.io/test-infra/config/testgrids/openshift",
				"-release-config", o.releaseConfigDir, // "../../../release/core-services/release-controller/_releases",
				"-prow-jobs-dir", o.prowJobsDir, // "../../../release/ci-operator/jobs",
				"-allow-list", o.allowList, // ".../../../release/core-services/testgrid-config-generator/_allow-list.yaml",
			},
		},
	}

	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: sa}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: sa}
	author := fmt.Sprintf("%s <%s>", o.gitName, o.gitEmail)
	commitsCounter := 0

	for _, step := range steps {
		committed, err := runAndCommitIfNeeded(stdout, stderr, author, step.command, step.arguments)
		if err != nil {
			logrus.WithError(err).Fatal("failed to run command and commit the changes")
		}

		if committed {
			commitsCounter++
		}
	}
	if commitsCounter == 0 {
		logrus.Info("no new commits, exiting ...")
		os.Exit(0)
	}

	title := fmt.Sprintf("%s by auto-testgrid-generator job at %s", matchTitle, time.Now().Format(time.RFC1123))
	if err := bumper.GitPush(fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin, string(sa.GetTokenGenerator(o.GitHubOptions.TokenPath)()), o.githubLogin, githubRepo), remoteBranch, stdout, stderr); err != nil {
		logrus.WithError(err).Fatal("Failed to push changes.")
	}

	var labelsToAdd []string
	if o.selfApprove {
		logrus.Infof("Self-aproving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}
	if err := bumper.UpdatePullRequestWithLabels(gc, githubOrg, githubRepo, title, fmt.Sprintf("/cc @%s", o.assign),
		matchTitle, o.githubLogin+":"+remoteBranch, "master", true, labelsToAdd); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}
