package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/cmd/generic-autobumper/bumper"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/labels"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

const (
	githubOrg    = "openshift"
	githubRepo   = "release"
	githubLogin  = "openshift-bot"
	remoteBranch = "auto-publicize-sync"
	privateOrg   = "openshift-priv"
	matchTitle   = "Automate publicize configuration sync"
	description  = "Updates the publicize plugin configuration"
)

type options struct {
	dryRun      bool
	selfApprove bool

	githubLogin     string
	gitName         string
	gitEmail        string
	publicizeConfig string
	releaseRepoPath string

	config.WhitelistOptions
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

	fs.StringVar(&o.publicizeConfig, "publicize-config", "", "The publicize configuration to be updated. Assuming that the file exists in the working directory.")
	fs.StringVar(&o.releaseRepoPath, "release-repo-path", "", "Path to a openshift/release repository directory")

	o.AddFlags(fs)
	o.WhitelistOptions.Bind(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Errorf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func defaultAndValidateOptions(o *options) error {
	if o.githubLogin == "" {
		return errors.New("--github-login is not specified")
	}
	if (o.gitEmail == "") != (o.gitName == "") {
		return fmt.Errorf("--git-name and --git-email must be specified together")
	}
	if len(o.releaseRepoPath) == 0 {
		return errors.New("--release-repo-path is not specified")
	}
	if o.publicizeConfig == "" {
		return errors.New("--publicize-config is not specified")
	}
	if err := o.WhitelistOptions.Validate(); err != nil {
		return fmt.Errorf("couldn't validate whitelist options: %w", err)
	}
	return o.GitHubOptions.Validate(o.dryRun)
}

type Config struct {
	Repositories map[string]string `json:"repositories,omitempty"`
}

func main() {
	o := parseOptions()
	if err := defaultAndValidateOptions(&o); err != nil {
		logrus.WithError(err).Fatal("Invalid arguments.")
	}

	if err := secret.Add(o.GitHubOptions.TokenPath); err != nil {
		logrus.WithError(err).Fatal("Failed to start secrets agent")
	}

	gc, err := o.GitHubOptions.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("error getting GitHub client")
	}

	logrus.Info("Getting repositories that exists in the whitelist or promote official images")
	orgRepos, err := getReposForPrivateOrg(o.releaseRepoPath, o.WhitelistOptions.WhitelistConfig.Whitelist)
	if err != nil {
		logrus.WithError(err).Fatal("couldn't get the list of org/repos that promote official images")
	}

	publicizeRepos := make(map[string]string)
	for org, repos := range orgRepos {
		for repo := range repos {
			privateOrgRepo := fmt.Sprintf("%s/%s", privateOrg, repo)
			publicOrgRepo := fmt.Sprintf("%s/%s", org, repo)
			publicizeRepos[privateOrgRepo] = publicOrgRepo
		}
	}

	config := &Config{}
	config.Repositories = publicizeRepos

	b, err := yaml.Marshal(&config)
	if err != nil {
		logrus.WithError(err).Fatal("couldn't marshal the publicize configuration")
	}

	logrus.Info("Generating publicize config file")
	if err := os.MkdirAll(path.Dir(o.publicizeConfig), os.ModePerm); err != nil && !os.IsExist(err) {
		logrus.WithError(err).Fatal("failed to ensure directory existed for new publicize configuration")
	}
	if err := ioutil.WriteFile(o.publicizeConfig, b, 0664); err != nil {
		logrus.WithError(err).Fatal("failed to write new publicize configuration")
	}

	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: secret.Censor}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: secret.Censor}

	changed, err := bumper.HasChanges()
	if err != nil {
		logrus.WithError(err).Fatal("error occurred when checking changes")
	}

	if !changed {
		logrus.Info("No changes to commit")
		os.Exit(0)
	}

	if o.dryRun {
		logrus.Info("Running in dry-run mode, not preparing a Pull Request")
		os.Exit(0)
	}

	logrus.Info("Preparing pull request")
	title := fmt.Sprintf("%s %s", matchTitle, time.Now().Format(time.RFC1123))
	if err := bumper.GitCommitAndPush(fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin, string(secret.GetTokenGenerator(o.GitHubOptions.TokenPath)()), o.githubLogin, githubRepo), remoteBranch, o.gitName, o.gitEmail, title, stdout, stderr, o.dryRun); err != nil {
		logrus.WithError(err).Fatal("Failed to push changes.")
	}

	var labelsToAdd []string
	if o.selfApprove {
		logrus.Infof("Self-aproving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}

	source := fmt.Sprintf("%s:%s", o.githubLogin, remoteBranch)
	if err := bumper.UpdatePullRequestWithLabels(gc, githubOrg, githubRepo, title, description, source, "master", remoteBranch, true, labelsToAdd, o.dryRun); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}

func getReposForPrivateOrg(releaseRepoPath string, allowlist map[string][]string) (map[string]sets.String, error) {
	ret := make(map[string]sets.String)

	for org, repos := range allowlist {
		for _, repo := range repos {
			if _, ok := ret[org]; !ok {
				ret[org] = sets.NewString(repo)
			} else {
				ret[org].Insert(repo)
			}
		}
	}

	callback := func(c *api.ReleaseBuildConfiguration, i *config.Info) error {
		if !promotion.BuildsOfficialImages(c, promotion.WithoutOKD) {
			return nil
		}

		if i.Org != "openshift" {
			logrus.WithField("org", i.Org).WithField("repo", i.Repo).Warn("Dropping repo in non-openshift org, this is currently not supported")
			return nil
		}

		repos, exist := ret[i.Org]
		if !exist {
			repos = sets.NewString()
		}
		ret[i.Org] = repos.Insert(i.Repo)

		return nil
	}

	if err := config.OperateOnCIOperatorConfigDir(filepath.Join(releaseRepoPath, config.CiopConfigInRepoPath), callback); err != nil {
		return ret, fmt.Errorf("error while operating in ci-operator configuration files: %w", err)
	}

	return ret, nil
}
