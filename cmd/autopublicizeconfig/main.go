package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/cmd/generic-autobumper/bumper"
	"sigs.k8s.io/prow/pkg/config/secret"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/labels"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/github/orgclient"
	"github.com/openshift/ci-tools/pkg/privateorg"
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
	flattenOrgs     privateorg.ArrayFlags

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
	fs.Var(&o.flattenOrgs, "flatten-org", "Organizations whose repos should not have org prefix (can be specified multiple times)")

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

	if o.GitHubOptions.TokenPath != "" {
		if err := secret.Add(o.GitHubOptions.TokenPath); err != nil {
			logrus.WithError(err).Fatal("Failed to start secrets agent")
		}
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
	flattenedOrgs := sets.New[string](privateorg.DefaultFlattenOrgs...)
	flattenedOrgs.Insert(o.flattenOrgs...)
	for org, repos := range orgRepos {
		for repo := range repos {
			mirroredRepo := privateorg.MirroredRepoName(org, repo, flattenedOrgs)
			privateOrgRepo := fmt.Sprintf("%s/%s", privateOrg, mirroredRepo)
			publicOrgRepo := fmt.Sprintf("%s/%s", org, repo)
			publicizeRepos[privateOrgRepo] = publicOrgRepo
		}
	}

	cfg := &Config{}
	cfg.Repositories = publicizeRepos

	b, err := yaml.Marshal(&cfg)
	if err != nil {
		logrus.WithError(err).Fatal("couldn't marshal the publicize configuration")
	}

	logrus.Info("Generating publicize config file")
	if err := os.MkdirAll(path.Dir(o.publicizeConfig), os.ModePerm); err != nil && !os.IsExist(err) {
		logrus.WithError(err).Fatal("failed to ensure directory existed for new publicize configuration")
	}
	if err := os.WriteFile(o.publicizeConfig, b, 0664); err != nil {
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

	var prHead string
	if o.GitHubOptions.TokenPath != "" {
		if err := bumper.GitCommitAndPush(fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin, string(secret.GetTokenGenerator(o.GitHubOptions.TokenPath)()), o.githubLogin, githubRepo), remoteBranch, o.gitName, o.gitEmail, title, stdout, stderr, o.dryRun); err != nil {
			logrus.WithError(err).Fatal("Failed to push changes.")
		}
		prHead = fmt.Sprintf("%s:%s", o.githubLogin, remoteBranch)
	} else {
		if err := pushWithAppAuth(&o, remoteBranch, o.gitName, o.gitEmail, title, stdout, stderr); err != nil {
			logrus.WithError(err).Fatal("Failed to push changes.")
		}
		prHead = remoteBranch
	}

	var labelsToAdd []string
	if o.selfApprove {
		logrus.Infof("Self-aproving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}

	repo, err := gc.GetRepo(githubOrg, githubRepo)
	if err != nil {
		logrus.WithError(err).Fatalf("Error retrieving repository data: %v", err)
	}
	isAppAuth := o.GitHubOptions.TokenPath == ""
	prClient := github.Client(&orgclient.OrgAwareClient{Client: gc, Org: githubOrg, IsAppAuth: isAppAuth})
	if err := bumper.UpdatePullRequestWithLabels(prClient, githubOrg, githubRepo, title, description, prHead, repo.DefaultBranch, remoteBranch, true, labelsToAdd, o.dryRun); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}

func pushWithAppAuth(o *options, branch, gitName, gitEmail, commitMsg string, stdout, stderr bumper.HideSecretsWriter) error {
	if err := bumper.Call(stdout, stderr, "git", []string{"add", "-A"}); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	commitArgs := []string{"commit", "-m", commitMsg}
	if gitName != "" && gitEmail != "" {
		commitArgs = append(commitArgs, "--author", fmt.Sprintf("%s <%s>", gitName, gitEmail))
	}
	if err := bumper.Call(stdout, stderr, "git", commitArgs); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	if out, err := exec.Command("git", "checkout", "-B", branch).CombinedOutput(); err != nil {
		return fmt.Errorf("error creating local branch %s: %w\n%s", branch, err, string(out))
	}

	gcf, err := o.GitHubOptions.GitClientFactory("", nil, false, false)
	if err != nil {
		return fmt.Errorf("error creating git client factory: %w", err)
	}
	defer func() {
		if err := gcf.Clean(); err != nil {
			logrus.WithError(err).Warn("Failed to clean git client factory cache.")
		}
	}()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("error getting working directory: %w", err)
	}

	repoClient, err := gcf.ClientFromDir(githubOrg, githubRepo, cwd)
	if err != nil {
		return fmt.Errorf("error creating repo client: %w", err)
	}

	return repoClient.PushToCentral(branch, true)
}

func getReposForPrivateOrg(releaseRepoPath string, allowlist map[string][]string) (map[string]sets.Set[string], error) {
	ret := make(map[string]sets.Set[string])

	for org, repos := range allowlist {
		for _, repo := range repos {
			if _, ok := ret[org]; !ok {
				ret[org] = sets.New[string](repo)
			} else {
				ret[org].Insert(repo)
			}
		}
	}

	callback := func(c *api.ReleaseBuildConfiguration, i *config.Info) error {
		if !api.BuildsAnyOfficialImages(c, api.WithoutOKD) {
			return nil
		}

		repos, exist := ret[i.Org]
		if !exist {
			repos = sets.New[string]()
		}
		ret[i.Org] = repos.Insert(i.Repo)

		return nil
	}

	if err := config.OperateOnCIOperatorConfigDir(filepath.Join(releaseRepoPath, config.CiopConfigInRepoPath), callback); err != nil {
		return ret, fmt.Errorf("error while operating in ci-operator configuration files: %w", err)
	}

	return ret, nil
}
