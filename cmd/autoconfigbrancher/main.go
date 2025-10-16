package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/robfig/cron.v2"

	"sigs.k8s.io/prow/cmd/generic-autobumper/bumper"
	"sigs.k8s.io/prow/pkg/config/secret"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/labels"

	"github.com/openshift/ci-tools/pkg/promotion"
	"github.com/openshift/ci-tools/pkg/rehearse"
)

const (
	githubOrg    = "openshift"
	githubRepo   = "release"
	githubLogin  = "openshift-bot"
	githubTeam   = "openshift/test-platform"
	matchTitle   = "Automate config brancher"
	remoteBranch = "auto-config-brancher"

	prowConfigDir = "./core-services/prow/02_config/"
)

type options struct {
	selfApprove bool

	githubLogin    string
	gitName        string
	gitEmail       string
	targetDir      string
	assign         string
	whitelist      string
	rebalancerCron string

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
	fs.StringVar(&o.targetDir, "target-dir", "", "The directory containing the target repo.")
	fs.StringVar(&o.assign, "assign", githubTeam, "The github username or group name to assign the created pull request to.")
	fs.StringVar(&o.whitelist, "whitelist-file", "", "The path of the whitelisted repositories file.")
	fs.StringVar(&o.rebalancerCron, "rebalancer-cron", "", "Cron expression defining how often rebalancer should run (plus/minus 1h time window). If not specified, rebalancer will not run.")
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

func runAndCommitIfNeeded(stdout, stderr io.Writer, author, cmd string, args []string) (bool, error) {
	fullCommand := fmt.Sprintf("%s %s", filepath.Base(cmd), strings.Join(args, " "))

	logrus.Infof("Running: %s", fullCommand)
	if err := bumper.Call(stdout, stderr, cmd, args); err != nil {
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
	addArgs := []string{"add", "."}
	if err := bumper.Call(stdout, stderr, gitCmd, addArgs); err != nil {
		return false, fmt.Errorf("failed to 'git add .': %w", err)
	}

	commitArgs := []string{"commit", "-m", fullCommand, "--author", author}
	if err := bumper.Call(stdout, stderr, gitCmd, commitArgs); err != nil {
		return false, fmt.Errorf("fail to %s %s: %w", gitCmd, strings.Join(commitArgs, " "), err)
	}

	return true, nil
}

type step struct {
	command   string
	arguments []string
}

func main() {
	o := parseOptions()
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("Invalid arguments.")
	}

	versionSplit := strings.Split(o.CurrentRelease, ".")
	if len(versionSplit) != 2 {
		logrus.WithError(fmt.Errorf("version %s split by dot doesn't have two elements", o.CurrentRelease)).Fatal("Failed to parse the current version")
	}

	if err := secret.Add(o.GitHubOptions.TokenPath); err != nil {
		logrus.WithError(err).Fatal("Failed to start secrets agent")
	}

	gc, err := o.GitHubOptions.GitHubClient(!o.Confirm)
	if err != nil {
		logrus.WithError(err).Fatal("error getting GitHub client")
	}

	rebalance, err := withinWindow(o.rebalancerCron)
	if err != nil {
		logrus.WithError(err).Fatal("failed to parse cron")
	}

	logrus.Infof("Changing working directory to '%s' ...", o.targetDir)
	if err := os.Chdir(o.targetDir); err != nil {
		logrus.WithError(err).Fatal("Failed to change to root dir")
	}

	steps := []step{
		{
			command: "/usr/bin/registry-replacer",
			arguments: []string{
				"--github-token-path", "/etc/github/oauth",
				"--github-endpoint", "http://ghproxy",
				"--config-dir", "./ci-operator/config",
				"--registry", "./ci-operator/step-registry",
				"--prune-unused-replacements",
				"--prune-ocp-builder-replacements",
				"--prune-unused-base-images",
				"--current-release-minor=" + versionSplit[1],
				"--ensure-correct-promotion-dockerfile-ignored-repos", "openshift/origin-aggregated-logging",
				"--ensure-correct-promotion-dockerfile-ignored-repos", "openshift/console",
				"--ensure-correct-promotion-dockerfile-ignored-repos", "openshift/linuxptp-daemon",
			},
		},
		{
			command: "/usr/bin/config-brancher",
			arguments: func() []string {
				args := []string{"--config-dir", "./ci-operator/config", "--current-release", o.CurrentRelease, "--skip-periodics"}
				for _, fr := range o.FutureReleases.Strings() {
					args = append(args, "--future-release", fr)
				}
				args = append(args, "--confirm")
				return args
			}(),
		},
		{
			command: "/usr/bin/ci-operator-config-mirror",
			arguments: func() []string {
				args := []string{"--config-dir", o.ConfigDir, "--to-org", "openshift-priv", "--only-org", "openshift"}
				if o.whitelist != "" {
					args = append(args, "--whitelist-file", o.whitelist)
				}
				return args

			}(),
		},
		{
			command:   "/usr/bin/determinize-ci-operator",
			arguments: []string{"--config-dir", o.ConfigDir, "--confirm"},
		},
		{
			command:   "/usr/bin/ci-operator-prowgen",
			arguments: []string{"--from-dir", o.ConfigDir, "--to-dir", "./ci-operator/jobs"},
		},
		{
			command: "/usr/bin/private-prow-configs-mirror",
			arguments: func() []string {
				args := []string{"--release-repo-path", ".",
					"--github-token-path", "/etc/github/oauth",
					"--github-endpoint", "http://ghproxy",
					"--dry-run", "false"}
				if o.whitelist != "" {
					args = append(args, "--whitelist-file", o.whitelist)
				}
				return args
			}(),
		},
		{
			command: "/usr/bin/determinize-prow-config",
			arguments: []string{
				fmt.Sprintf("--prow-config-dir=%s", prowConfigDir),
				fmt.Sprintf("--sharded-prow-config-base-dir=%s", prowConfigDir),
				fmt.Sprintf("--sharded-plugin-config-base-dir=%s", prowConfigDir),
			},
		},
		{
			command:   "/usr/bin/sanitize-prow-jobs",
			arguments: []string{"--prow-jobs-dir", "./ci-operator/jobs", "--config-path", "./core-services/sanitize-prow-jobs/_config.yaml"},
		},
		{
			command: "/usr/bin/clusterimageset-updater",
			arguments: []string{
				"--pools", "./clusters/hosted-mgmt/hive/pools",
				"--imagesets", "./clusters/hosted-mgmt/hive/pools",
			},
		},
		{
			command: "/usr/bin/promoted-image-governor",
			arguments: []string{
				"--ci-operator-config-path", "./ci-operator/config",
				"--release-controller-mirror-config-dir", "./core-services/release-controller/_releases",
				"--openshift-mapping-dir", "./core-services/image-mirroring/openshift",
				"--openshift-mapping-config", "./core-services/image-mirroring/openshift/_config.yaml",
				"--dry-run=true",
			},
		},
	}

	if rebalance {
		steps = append([]step{{
			command: "/usr/bin/rebalancer",
			arguments: []string{
				"--profiles=aws,aws-2,aws-3,aws-4,aws-5",
				"--profiles=gcp-openshift-gce-devel-ci-2,gcp,gcp-3",
				"--prometheus-bearer-token-path=/etc/prometheus/token",
			},
		}}, steps...)
	}

	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: secret.Censor}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: secret.Censor}
	author := fmt.Sprintf("%s <%s>", o.gitName, o.gitEmail)
	needsPushing, err := runSteps(steps, author, stdout, stderr)
	if err != nil {
		logrus.WithError(err).Fatal("failed to run steps")
	}
	if !needsPushing {
		return
	}

	title := fmt.Sprintf("%s by auto-config-brancher job at %s", matchTitle, time.Now().Format(time.RFC1123))
	if err := bumper.GitPush(fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin, string(secret.GetTokenGenerator(o.GitHubOptions.TokenPath)()), o.githubLogin, githubRepo), remoteBranch, stdout, stderr, ""); err != nil {
		logrus.WithError(err).Fatal("Failed to push changes.")
	}

	labelsToAdd := []string{
		"tide/merge-method-merge",
		rehearse.RehearsalsAckLabel,
	}
	if o.selfApprove {
		logrus.Infof("Self-approving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}
	repo, err := gc.GetRepo(githubOrg, githubRepo)
	if err != nil {
		logrus.WithError(err).Fatalf("Error retrieving repository data: %v", err)
	}
	if err := bumper.UpdatePullRequestWithLabels(gc, githubOrg, githubRepo, title, fmt.Sprintf("/cc @%s", o.assign), o.githubLogin+":"+remoteBranch, repo.DefaultBranch, remoteBranch, true, labelsToAdd, false); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}

func runSteps(steps []step, author string, stdout, stderr io.Writer) (needsPushing bool, err error) {
	startCommitOut, err := exec.Command("git", "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("failed to execute `git rev-parse HEAD`: %w\noutput:%s\n", err, string(startCommitOut))
	}
	startCommitSHA := strings.TrimSpace(string(startCommitOut))

	var didCommit bool
	for _, step := range steps {
		committed, err := runAndCommitIfNeeded(stdout, stderr, author, step.command, step.arguments)
		if err != nil {
			return false, fmt.Errorf("failed to run command and commit the changes: %w", err)
		}

		didCommit = didCommit || committed
	}

	if !didCommit {
		logrus.Info("No new commits")
		return false, nil
	}

	overallDiff, err := exec.Command("git", "diff", startCommitSHA).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("failed to check the overall diff: %w, out:\n%s\n", err, string(overallDiff))
	}
	if strings.TrimSpace(string(overallDiff)) == "" {
		logrus.Info("Empty overall diff")
		return false, nil
	}

	return true, nil
}

// withinWindow returns true if the schedule fires at any time
// between now-1h and now+1h.
func withinWindow(cronExpr string) (bool, error) {
	if cronExpr == "" {
		return false, nil
	}
	schedule, err := cron.Parse(cronExpr)
	if err != nil {
		return false, err
	}

	now := time.Now()
	windowStart := now.Add(-1 * time.Hour)
	windowEnd := now.Add(+1 * time.Hour)

	firstFire := schedule.Next(windowStart)

	if firstFire.Before(windowEnd) {
		return true, nil
	}
	return false, nil
}
