package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/experiment/autobumper/bumper"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
)

const (
	githubOrg   = "openshift"
	githubRepo  = "release"
	githubLogin = "openshift-bot"
	githubTeam  = "openshift/openshift-team-developer-productivity-test-platform"
)

var extraFiles = map[string]bool{
	"hack/images.sh": true,
}

type options struct {
	dryRun      bool
	githubLogin string
	gitName     string
	gitEmail    string
	targetDir   string
	assign      string
	flagutil.GitHubOptions
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually create the pull request with github client")
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.gitName, "git-name", "", "The name to use on the git commit. Requires --git-email. If not specified, uses the system default.")
	fs.StringVar(&o.gitEmail, "git-email", "", "The email to use on the git commit. Requires --git-name. If not specified, uses the system default.")
	fs.StringVar(&o.targetDir, "target-dir", "", "The directory containing the target repo.")
	fs.StringVar(&o.assign, "assign", githubTeam, "The github username or group name to assign the created pull request to.")
	o.AddFlagsWithoutDefaultGitHubTokenPath(fs)
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

	logrus.Infof("Changing working directory to '%s' ...", o.targetDir)
	if err := os.Chdir(o.targetDir); err != nil {
		logrus.WithError(err).Fatal("Failed to change to root dir")
	}
	images, err := bumper.UpdateReferences([]string{"clusters/", "cluster/ci/config/prow/", "core-services/prow", "ci-operator/", "hack/"}, extraFiles)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to update references.")
	}

	// The image tags are in the diff as context, even though they are not part of any change,
	// so we have to update the diff here.
	if out, err := exec.Command("make", "update-app-ci").CombinedOutput(); err != nil {
		logrus.WithError(err).Fatalf("Failed to run `make update-app-ci`: %s", string(out))
	}

	changed, err := bumper.HasChanges()
	if err != nil {
		logrus.WithError(err).Fatal("error occurred when checking changes")
	}

	if !changed {
		logrus.Info("no images updated, exiting ...")
		return
	}

	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: sa}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: sa}

	remoteBranch := "autobump"
	if err := bumper.MakeGitCommit(fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin,
		string(sa.GetTokenGenerator(o.GitHubOptions.TokenPath)()), o.githubLogin, githubRepo), remoteBranch, o.gitName, o.gitEmail, images, stdout, stderr); err != nil {
		logrus.WithError(err).Fatal("Failed to push changes.")
	}

	if err := bumper.UpdatePR(gc, githubOrg, githubRepo, images, "/cc @"+o.assign, "Update prow to", o.githubLogin+":"+remoteBranch, "master"); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}
