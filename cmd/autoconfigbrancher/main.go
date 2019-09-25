package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

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

var (
	count = 0
)

type options struct {
	dryRun         bool
	githubLogin    string
	targetDir      string
	assign         string
	currentRelease string
	futureReleases flagutil.Strings
	debugMode      bool
	flagutil.GitHubOptions
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually create the pull request with github client")
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.targetDir, "target-dir", "", "The directory containing the target repo.")
	fs.StringVar(&o.assign, "assign", githubTeam, "The github username or group name to assign the created pull request to.")
	fs.StringVar(&o.currentRelease, "current-release", "", "Configurations targeting this release will get branched.")
	fs.Var(&o.futureReleases, "future-release", "Configurations will get branched to target this release, provide one or more times.")
	fs.BoolVar(&o.debugMode, "debug-mode", false, "Enable the DEBUG level of logs if true.")
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
	if o.targetDir == "" {
		return fmt.Errorf("--target-dir is mandatory")
	}
	if o.assign == "" {
		return fmt.Errorf("--assign is mandatory")
	}
	if o.currentRelease == "" {
		return fmt.Errorf("--current-release is mandatory")
	}
	if len(o.futureReleases.Strings()) == 0 {
		return fmt.Errorf("--future-release is mandatory")
	}
	for _, rf := range o.futureReleases.Strings() {
		if rf == "" {
			return fmt.Errorf("--future-release cannot be empty")
		}
	}
	return o.GitHubOptions.Validate(o.dryRun)
}

func hasChanges() (bool, error) {
	cmd := "git"
	args := []string{"status", "--porcelain"}
	logrus.WithField("cmd", cmd).WithField("args", args).Info("running command ...")
	combinedOutput, err := exec.Command(cmd, args...).CombinedOutput()
	if err != nil {
		logrus.WithField("cmd", cmd).Debugf("output is '%s'", string(combinedOutput))
		return false, err
	}
	return len(strings.TrimSuffix(string(combinedOutput), "\n")) > 0, nil
}

func commitIfNeeded(msg, author string) {
	changed, err := hasChanges()
	if err != nil {
		logrus.WithError(err).Fatal("error occurred when checking changes")
	}
	if changed {
		count++
		addAndCommit(msg, author)
	}
}

func addAndCommit(msg, author string) {
	cmd := "git"
	args := []string{"add", "."}
	run(cmd, args...)
	cmd = "git"
	args = []string{"commit", "-m", msg, "--author", author}
	run(cmd, args...)
}

func run(cmd string, args ...string) {
	logrus.WithField("cmd", cmd).WithField("args", args).Info("running command ...")
	if combinedOutput, err := exec.Command(cmd, args...).CombinedOutput(); err != nil {
		logrus.WithField("cmd", cmd).Debugf("output is '%s'", string(combinedOutput))
		logrus.Fatalf("Failed to run command:'%s'", err)
	}
}

func main() {
	o := parseOptions()
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("Invalid arguments.")
	}

	if o.debugMode {
		logrus.Info("debug mode is enabled")
		logrus.SetLevel(logrus.DebugLevel)
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

	botUser, err := gc.BotUser()
	if err != nil || botUser == nil {
		logrus.WithError(err).Fatal("Failed to get bot user data.")
	}

	cmd := "/usr/bin/determinize-ci-operator"
	args := []string{"--config-dir", "./ci-operator/config", "--current-release", o.currentRelease}
	for _, fr := range o.futureReleases.Strings() {
		args = append(args, []string{"--future-release", fr}...)
	}
	args = append(args, "--confirm")
	run(cmd, args...)

	author := fmt.Sprintf("%s <%s>", botUser.Name, botUser.Email)
	commitIfNeeded(fmt.Sprintf("%s --current-release %s --future-release %s", "determinize-ci-operator",
		o.currentRelease, strings.Join(o.futureReleases.Strings(), ",")), author)

	cmd = "/usr/bin/config-brancher"
	run(cmd, args...)

	commitIfNeeded(fmt.Sprintf("%s --current-release %s --future-release %s", "config-brancher",
		o.currentRelease, strings.Join(o.futureReleases.Strings(), ",")), author)

	cmd = "/usr/bin/ci-operator-prowgen"
	args = []string{"--from-dir", "./ci-operator/config", "--to-dir", "./ci-operator/jobs"}
	run(cmd, args...)

	commitIfNeeded(fmt.Sprintf("%s --from-dir ./ci-operator/config --to-dir ./ci-operator/jobs", "ci-operator-prowgen"), author)

	if count == 0 {
		logrus.Info("no new commits, existing ...")
		return
	}

	remoteBranch := "auto-config-brancher"
	matchTitle := "Automate config brancher"
	title := fmt.Sprintf("%s by auto-config-brancher job at %s", matchTitle, time.Now().Format(time.RFC1123))
	cmd = "git"
	args = []string{"push", "-f", fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin,
		string(sa.GetTokenGenerator(o.GitHubOptions.TokenPath)()), o.githubLogin, githubRepo),
		fmt.Sprintf("HEAD:%s", remoteBranch)}
	run(cmd, args...)

	if err := bumper.UpdatePullRequest(gc, githubOrg, githubRepo, title, fmt.Sprintf("/cc @%s", o.assign),
		matchTitle, o.githubLogin+":"+remoteBranch, "master"); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}
