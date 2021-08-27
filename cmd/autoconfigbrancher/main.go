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
	matchTitle   = "Automate config brancher"
	remoteBranch = "auto-config-brancher"

	prowConfigDir = "./core-services/prow/02_config/"
)

type options struct {
	selfApprove bool

	githubLogin string
	gitName     string
	gitEmail    string
	targetDir   string
	assign      string
	whitelist   string

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

type step struct {
	command   string
	arguments []string
}

func main() {
	o := parseOptions()
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("Invalid arguments.")
	}

	if err := secret.Add(o.GitHubOptions.TokenPath); err != nil {
		logrus.WithError(err).Fatal("Failed to start secrets agent")
	}

	gc, err := o.GitHubOptions.GitHubClient(!o.Confirm)
	if err != nil {
		logrus.WithError(err).Fatal("error getting GitHub client")
	}

	logrus.Infof("Changing working directory to '%s' ...", o.targetDir)
	if err := os.Chdir(o.targetDir); err != nil {
		logrus.WithError(err).Fatal("Failed to change to root dir")
	}

	steps := []step{
		{
			// Will check if repos that are part of the OCP payload
			// have an up-to-date .ci-operator.yaml and if yes, update
			// their config to set .build_root.from_repository: true
			command: "/usr/bin/ci-operator-yaml-creator",
			arguments: []string{
				"--max-concurrency", "1",
				"--github-token-path", "/etc/github/oauth",
				"--github-endpoint", "http://ghproxy",
				"--ci-operator-config-dir=ci-operator/config",
				// Only update o/release
				"--create-prs=false",
			},
		},
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
				"--ensure-correct-promotion-dockerfile",
				"--current-release-minor=8",
				"--ensure-correct-promotion-dockerfile-ignored-repos", "openshift/origin-aggregated-logging",
				"--ensure-correct-promotion-dockerfile-ignored-repos", "openshift/console",
			},
		},
		{
			command: "/usr/bin/config-brancher",
			arguments: func() []string {
				args := []string{"--config-dir", "./ci-operator/config", "--current-release", o.CurrentRelease}
				for _, fr := range o.FutureReleases.Strings() {
					args = append(args, []string{"--future-release", fr}...)
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
					args = append(args, []string{"--whitelist-file", o.whitelist}...)
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
				args := []string{"--release-repo-path", "."}
				if o.whitelist != "" {
					args = append(args, []string{"--whitelist-file", o.whitelist}...)
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
			command: "/usr/bin/template-deprecator",
			arguments: []string{
				"--prow-config-path", "./core-services/prow/02_config/_config.yaml",
				"--plugin-config", "./core-services/prow/02_config/_plugins.yaml",
				"--prow-jobs-dir", "./ci-operator/jobs",
				"--allowlist-path", "./core-services/template-deprecation/_allowlist.yaml",
				"--prune=true",
			},
		},
		{
			command: "/usr/bin/clusterimageset-updater",
			arguments: []string{
				"--pools", "./clusters/hive/pools",
				"--imagesets", "./clusters/hive/pools",
			},
		},
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

	var labelsToAdd []string
	if o.selfApprove {
		logrus.Infof("Self-approving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}
	if err := bumper.UpdatePullRequestWithLabels(gc, githubOrg, githubRepo, title, fmt.Sprintf("/cc @%s", o.assign), o.githubLogin+":"+remoteBranch, "master", remoteBranch, true, labelsToAdd, false); err != nil {
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

		if committed {
			didCommit = didCommit || true
		}
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
