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
	"k8s.io/test-infra/prow/labels"

	"github.com/openshift/ci-tools/pkg/promotion"
)

const (
	githubOrg   = "openshift"
	githubRepo  = "release"
	githubLogin = "openshift-bot"
	githubTeam  = "openshift/openshift-team-developer-productivity-test-platform"
	matchTitle  = "Automate config brancher"
)

var (
	count = 0
)

type options struct {
	promotion.FutureOptions
	githubLogin string
	gitName     string
	gitEmail    string
	targetDir   string
	assign      string
	selfApprove bool
	flagutil.GitHubOptions
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	o.FutureOptions.Bind(fs)
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.gitName, "git-name", "", "The name to use on the git commit. Requires --git-email. If not specified, uses the system default.")
	fs.StringVar(&o.gitEmail, "git-email", "", "The email to use on the git commit. Requires --git-name. If not specified, uses the system default.")
	fs.StringVar(&o.targetDir, "target-dir", "", "The directory containing the target repo.")
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
	if o.targetDir == "" {
		return fmt.Errorf("--target-dir is mandatory")
	}
	if o.assign == "" {
		return fmt.Errorf("--assign is mandatory")
	}
	if err := o.FutureOptions.Validate(); err != nil {
		return err
	}
	return o.GitHubOptions.Validate(!o.Confirm)
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

	sa := &secret.Agent{}
	if err := sa.Start([]string{o.GitHubOptions.TokenPath}); err != nil {
		logrus.WithError(err).Fatal("Failed to start secrets agent")
	}

	gc, err := o.GitHubOptions.GitHubClient(sa, !o.Confirm)
	if err != nil {
		logrus.WithError(err).Fatal("error getting GitHub client")
	}

	logrus.Infof("Changing working directory to '%s' ...", o.targetDir)
	if err := os.Chdir(o.targetDir); err != nil {
		logrus.WithError(err).Fatal("Failed to change to root dir")
	}

	cmd := "/usr/bin/determinize-ci-operator"
	args := []string{"--config-dir", o.ConfigDir, "--confirm"}
	run(cmd, args...)

	author := fmt.Sprintf("%s <%s>", o.gitName, o.gitEmail)
	commitIfNeeded("determinize-ci-operator --confirm", author)

	cmd = "/usr/bin/config-brancher"
	args = []string{"--config-dir", o.ConfigDir, "--current-release", o.CurrentRelease}
	for _, fr := range o.FutureReleases.Strings() {
		args = append(args, []string{"--future-release", fr}...)
	}
	args = append(args, "--confirm")
	run(cmd, args...)

	commitIfNeeded(fmt.Sprintf("config-brancher --current-release %s --future-release %s", o.CurrentRelease, strings.Join(o.FutureReleases.Strings(), ",")), author)

	cmd = "/usr/bin/ci-operator-prowgen"
	args = []string{"--from-dir", o.ConfigDir, "--to-dir", "./ci-operator/jobs"}
	run(cmd, args...)

	commitIfNeeded("ci-operator-prowgen --from-dir ./ci-operator/config --to-dir ./ci-operator/jobs", author)

	cmd = "/usr/bin/ci-operator-config-mirror"
	args = []string{"--config-path", o.ConfigDir, "--to-org", "openshift-priv"}
	run(cmd, args...)

	commitIfNeeded("ci-operator-config-mirror --config-path ./ci-operator/config --to-org openshift-priv", author)

	cmd = "/usr/bin/ci-operator-prowgen"
	args = []string{"--from-dir", o.ConfigDir, "--to-dir", "./ci-operator/jobs"}
	run(cmd, args...)

	commitIfNeeded("ci-operator-prowgen --from-dir ./ci-operator/config --to-dir ./ci-operator/jobs", author)

	cmd = "/usr/bin/private-prow-configs-mirror"
	args = []string{"--release-repo-path", "."}
	run(cmd, args...)

	commitIfNeeded("private-prow-configs-mirror --release-repo-path .", author)

	cmd = "/usr/bin/sanitize-prow-jobs"
	args = []string{"--prow-jobs-dir", "./ci-operator/jobs", "--config-path", "./core-services/sanitize-prow-jobs/_config.yaml"}
	run(cmd, args...)
	commitIfNeeded("sanitize-prow-jobs  --prow-jobs-dir ./ci-operator/jobs", author)

	if count == 0 {
		logrus.Info("no new commits, existing ...")
		return
	}

	remoteBranch := "auto-config-brancher"
	title := fmt.Sprintf("%s by auto-config-brancher job at %s", matchTitle, time.Now().Format(time.RFC1123))
	cmd = "git"
	args = []string{"push", "-f", fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin,
		string(sa.GetTokenGenerator(o.GitHubOptions.TokenPath)()), o.githubLogin, githubRepo),
		fmt.Sprintf("HEAD:%s", remoteBranch)}
	run(cmd, args...)

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
