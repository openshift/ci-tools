package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/cmd/generic-autobumper/bumper"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	pluginflagutil "k8s.io/test-infra/prow/flagutil/plugins"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/labels"
	"k8s.io/test-infra/prow/plugins"
	"k8s.io/test-infra/prow/plugins/ownersconfig"
	"k8s.io/test-infra/prow/repoowners"
)

const (
	doNotEdit     = "DO NOT EDIT; this file is auto-generated using https://github.com/openshift/ci-tools."
	ownersComment = "See the OWNERS docs: https://git.k8s.io/community/contributors/guide/owners.md"

	githubOrg   = "openshift"
	githubRepo  = "release"
	githubLogin = "openshift-bot"

	defaultPRAssignee = "openshift/test-platform"

	configSubDirs      = "jobs,config,templates"
	targetSubDirectory = "ci-operator"

	defaultBaseBranch = "master"
)

type SimpleConfig = repoowners.SimpleConfig

type FullConfig = repoowners.FullConfig

type RepoAliases = repoowners.RepoAliases

type orgRepo struct {
	Directories  []string
	Organization string
	Repository   string
}

func (orgRepo orgRepo) repoString() string {
	return fmt.Sprintf("%s/%s", orgRepo.Organization, orgRepo.Repository)
}

func loadRepos(configRootDir string, blocked blocklist, configSubDirs, extraDirs []string, githubOrg string, githubRepo string) ([]orgRepo, error) {
	orgRepos := map[string]*orgRepo{}
	configSubDirectories := make([]string, 0, len(configSubDirs)+len(extraDirs))
	for _, sourceSubDir := range configSubDirs {
		configSubDirectories = append(configSubDirectories, filepath.Join(configRootDir, sourceSubDir))
	}

	for _, subdirectory := range append(configSubDirectories, extraDirs...) {
		orgDirs, err := ioutil.ReadDir(subdirectory)
		if err != nil {
			return nil, err
		}
		for _, orgDir := range orgDirs {
			if !orgDir.IsDir() {
				continue
			}
			logrus.WithField("orgDir.Name()", orgDir.Name()).Debug("loading orgDir ...")
			org := filepath.Base(orgDir.Name())
			if blocked.orgs.Has(org) {
				logrus.WithField("org", org).Info("org is on organization blocklist, skipping")
				continue
			}
			repoDirs, err := ioutil.ReadDir(filepath.Join(subdirectory, orgDir.Name()))
			if err != nil {
				return nil, err
			}
			for _, repoDir := range repoDirs {
				if !repoDir.IsDir() {
					continue
				}
				repo := repoDir.Name()
				if org == githubOrg && repo == githubRepo {
					continue
				}
				var repoConfigDirs []string
				for _, sourceSubDir := range configSubDirectories {
					repoConfigDirs = append(repoConfigDirs, filepath.Join(sourceSubDir, org, repo))
				}
				for _, extraDir := range extraDirs {
					repoConfigDirs = append(repoConfigDirs, filepath.Join(extraDir, org, repo))
				}
				var dirs []string
				for _, d := range repoConfigDirs {
					fileInfo, err := os.Stat(d)
					logrus.WithField("err", err).Debug("os.Stat(d): checking error ...")
					if !os.IsNotExist(err) && fileInfo.IsDir() {
						logrus.WithField("d", d).WithField("blocked-directories", blocked.directories).
							Debug("trying to determine if the directory is in the repo blocklist")
						if blocked.directories.Has(d) {
							logrus.WithField("repository", d).Info("repository is on repository blocklist, skipping")
							continue
						}
						dirs = append(dirs, d)
					}
				}
				if _, found := orgRepos[org+"/"+repo]; !found {
					orgRepos[org+"/"+repo] = &orgRepo{Organization: org, Repository: repo}
				}
				orgRepos[org+"/"+repo].Directories = append(orgRepos[org+"/"+repo].Directories, dirs...)
			}
		}
	}

	var result []orgRepo
	for _, orgRepo := range orgRepos {
		orgRepo.Directories = sets.NewString(orgRepo.Directories...).List()
		result = append(result, *orgRepo)
	}
	return result, nil
}

type httpResult struct {
	simpleConfig     SimpleConfig
	fullConfig       FullConfig
	repoAliases      RepoAliases
	ownersFileExists bool
}

// resolveOwnerAliases computes the resolved (simple or full config) format of the OWNERS file
func (r httpResult) resolveOwnerAliases(cleaner ownersCleaner) interface{} {
	if !r.simpleConfig.Empty() {
		sc := SimpleConfig{
			Config: repoowners.Config{
				Approvers:         cleaner(r.repoAliases.ExpandAliases(repoowners.NormLogins(r.simpleConfig.Approvers)).List()),
				Reviewers:         cleaner(r.repoAliases.ExpandAliases(repoowners.NormLogins(r.simpleConfig.Reviewers)).List()),
				RequiredReviewers: cleaner(r.repoAliases.ExpandAliases(repoowners.NormLogins(r.simpleConfig.RequiredReviewers)).List()),
				Labels:            sets.NewString(r.simpleConfig.Labels...).List(),
			},
			Options: r.simpleConfig.Options,
		}
		if len(sc.Reviewers) == 0 {
			sc.Reviewers = sc.Approvers
		}
		return sc
	} else {
		fc := FullConfig{
			Filters: map[string]repoowners.Config{},
			Options: r.fullConfig.Options,
		}
		for k, v := range r.fullConfig.Filters {
			cfg := repoowners.Config{
				Approvers:         cleaner(r.repoAliases.ExpandAliases(repoowners.NormLogins(v.Approvers)).List()),
				Reviewers:         cleaner(r.repoAliases.ExpandAliases(repoowners.NormLogins(v.Reviewers)).List()),
				RequiredReviewers: cleaner(r.repoAliases.ExpandAliases(repoowners.NormLogins(v.RequiredReviewers)).List()),
				Labels:            sets.NewString(v.Labels...).List(),
			}
			if len(cfg.Reviewers) == 0 {
				cfg.Reviewers = cfg.Approvers
			}
			fc.Filters[k] = cfg
		}
		return fc
	}
}

type FileGetter interface {
	GetFile(org, repo, filepath, commit string) ([]byte, error)
}

func getOwnersHTTP(fg FileGetter, orgRepo orgRepo, filenames ownersconfig.Filenames) (httpResult, error) {
	var httpResult httpResult

	for _, filename := range []string{filenames.Owners, filenames.OwnersAliases} {
		data, err := fg.GetFile(orgRepo.Organization, orgRepo.Repository, filename, "")
		if err != nil {
			if _, nf := err.(*github.FileNotFound); nf {
				logrus.WithField("orgRepo", orgRepo.repoString()).WithField("filename", filename).
					Debug("Not found file in the upstream repo")
				break
			}
			return httpResult, err
		}

		switch filename {
		case filenames.Owners:
			httpResult.ownersFileExists = true
			simple, err := repoowners.LoadSimpleConfig(data)
			if err != nil {
				logrus.WithError(err).Error("Unable to load simple config.")
				return httpResult, err
			}
			httpResult.simpleConfig = simple
			if httpResult.simpleConfig.Empty() {
				full, err := repoowners.LoadFullConfig(data)
				if err != nil {
					logrus.WithError(err).Error("Unable to load full config.")
					return httpResult, err
				}
				httpResult.fullConfig = full
			}
		case filenames.OwnersAliases:
			aliases, err := repoowners.ParseAliasesConfig(data)
			if err != nil {
				logrus.WithError(err).Error("Unable to parse aliases config.")
				return httpResult, err
			}
			httpResult.repoAliases = aliases
		default:
			return httpResult, fmt.Errorf("unrecognized filename %s", filename)
		}

	}

	return httpResult, nil
}

func addHeader(path string, header string) error {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, append([]byte(header), content...), 0644)
}

func writeOwners(orgRepo orgRepo, httpResult httpResult, cleaner ownersCleaner, header string) error {
	for _, directory := range orgRepo.Directories {
		path := filepath.Join(directory, "OWNERS")
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}

		logrus.WithField("path", path).Debug("Writing to path ...")
		config := httpResult.resolveOwnerAliases(cleaner)
		switch cfg := config.(type) {
		case SimpleConfig:
			err = repoowners.SaveSimpleConfig(cfg, path)
		case FullConfig:
			err = repoowners.SaveFullConfig(cfg, path)
		default:
			return fmt.Errorf("unknown config type: %+v", config)
		}
		if err != nil {
			logrus.WithError(err).Error("error occurred when saving config")
			return err
		}

		err = addHeader(path, header)
		if err != nil {
			return err
		}
	}
	return nil
}

func makeHeader(destOrg, srcOrg, srcRepo string) string {
	lines := []string{
		doNotEdit,
		fmt.Sprintf("Fetched from https://github.com/%s/%s root OWNERS", srcOrg, srcRepo),
		"If the repo had OWNERS_ALIASES then the aliases were expanded",
		fmt.Sprintf("Logins who are not members of '%s' organization were filtered out", destOrg),
		ownersComment,
	}
	for i := range lines {
		lines[i] = fmt.Sprintf("# %s\n", lines[i])
	}

	return strings.Join(lines, "") + "\n"
}

func pullOwners(gc github.Client, configRootDir string, blocklist blocklist, configSubDirs, extraDirs []string, githubOrg string, githubRepo string, pc plugins.Configuration) error {
	orgRepos, err := loadRepos(configRootDir, blocklist, configSubDirs, extraDirs, githubOrg, githubRepo)
	if err != nil {
		return err
	}

	cleaner, err := ownersCleanerFactory(githubOrg, gc)
	if err != nil {
		return fmt.Errorf("failed to construct owners cleaner: %w", err)
	}

	var errs []error
	for _, orgRepo := range orgRepos {
		logrus.WithField("orgRepo", orgRepo.repoString()).Info("handling repo ...")
		httpResult, err := getOwnersHTTP(gc, orgRepo, pc.OwnersFilenames(orgRepo.Organization, orgRepo.Repository))
		if err != nil {
			// TODO we might need to handle errors from `yaml.Unmarshal` if OWNERS is not a valid yaml file
			errs = append(errs, err)
			continue
		}
		if !httpResult.ownersFileExists {
			logrus.WithField("orgRepo", fmt.Sprintf("%s/%s", orgRepo.Organization, orgRepo.Repository)).
				Warn("Ignoring the repo with no OWNERS file in the upstream repo.")
			continue
		}

		if err := writeOwners(orgRepo, httpResult, cleaner, makeHeader(githubOrg, orgRepo.Organization, orgRepo.Repository)); err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

type options struct {
	dryRun             bool
	githubLogin        string
	githubOrg          string
	githubRepo         string
	gitName            string
	gitEmail           string
	gitSignoff         bool
	assign             string
	targetDir          string
	targetSubDirectory string
	configSubDirs      flagutil.Strings
	extraDirs          flagutil.Strings
	blockedRepos       flagutil.Strings
	blockedOrgs        flagutil.Strings
	debugMode          bool
	selfApprove        bool
	prBaseBranch       string
	plugins            pluginflagutil.PluginOptions
	flagutil.GitHubOptions
}

func parseOptions() options {
	var o options
	// To avoid flag from dependencies such as https://github.com/openshift/ci-tools/blob/5b5410293f7cd318540d1fb333c68b93ddab2b60/vendor/github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1/artifact_pvc.go#L30
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually create the pull request with github client")
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.githubOrg, "org", githubOrg, "The downstream GitHub org name.")
	fs.StringVar(&o.githubRepo, "repo", githubRepo, "The downstream GitHub repository name.")
	fs.StringVar(&o.gitName, "git-name", "", "The name to use on the git commit. Requires --git-email. If not specified, uses the system default.")
	fs.StringVar(&o.gitEmail, "git-email", "", "The email to use on the git commit. Requires --git-name. If not specified, uses the system default.")
	fs.BoolVar(&o.gitSignoff, "git-signoff", false, "Whether to signoff the commit. (https://git-scm.com/docs/git-commit#Documentation/git-commit.txt---signoff)")
	fs.StringVar(&o.assign, "assign", defaultPRAssignee, "The github username or group name to assign the created pull request to.")
	fs.StringVar(&o.targetDir, "target-dir", "", "The directory containing the target repo.")
	fs.StringVar(&o.targetSubDirectory, "target-subdir", targetSubDirectory, "The sub-directory of the target repo where the configurations are stored.")
	fs.Var(&o.configSubDirs, "config-subdir", "The sub-directory where configuration is stored. (Default list of directories: "+configSubDirs+")")
	fs.Var(&o.extraDirs, "extra-config-dir", "The directory path from the repo root where extra configuration is stored.")
	fs.Var(&o.blockedRepos, "ignore-repo", "The repo for which syncing OWNERS file is disabled.")
	fs.Var(&o.blockedOrgs, "ignore-org", "The orgs for which syncing OWNERS file is disabled.")
	fs.BoolVar(&o.debugMode, "debug-mode", false, "Enable the DEBUG level of logs if true.")
	fs.BoolVar(&o.selfApprove, "self-approve", false, "Self-approve the PR by adding the `approved` and `lgtm` labels. Requires write permissions on the repo.")
	fs.StringVar(&o.prBaseBranch, "pr-base-branch", defaultBaseBranch, "The base branch to use for the pull request.")
	o.AddFlags(fs)
	o.AllowAnonymous = true
	o.plugins.AddFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Errorf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func validateOptions(o options) error {
	if o.githubLogin == "" {
		return fmt.Errorf("--github-login is mandatory")
	}
	if (o.gitEmail == "") != (o.gitName == "") {
		return fmt.Errorf("--git-name and --git-email must be specified together")
	}
	if o.assign == "" {
		return fmt.Errorf("--assign is mandatory")
	}
	if o.targetDir == "" {
		return fmt.Errorf("--target-dir is mandatory")
	}
	return o.GitHubOptions.Validate(o.dryRun)
}

func getBody(directories []string, assign string) string {
	lines := []string{"The OWNERS file has been synced for the following folder(s):", ""}
	for _, d := range directories {
		lines = append(lines, fmt.Sprintf("* %s", d))
	}
	lines = append(lines, []string{"", fmt.Sprintf("/cc @%s", assign), ""}...)

	body := strings.Join(lines, "\n")

	if len(body) >= 65536 {
		body = body[:65530] + "..."
	}

	return body
}

func getTitle(matchTitle, datetime string) string {
	return fmt.Sprintf("%s by autoowners job at %s", matchTitle, datetime)
}

type OutputWriter struct {
	output []byte
}

func (w *OutputWriter) Write(content []byte) (n int, err error) {
	w.output = append(w.output, content...)
	return len(content), nil
}

func listUpdatedDirectories() ([]string, error) {
	w := &OutputWriter{}
	e := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: secret.Censor}
	if err := bumper.Call(w, e, "git", []string{"status", "--porcelain"}...); err != nil {
		return nil, err
	}
	return listUpdatedDirectoriesFromGitStatusOutput(string(w.output))
}

func listUpdatedDirectoriesFromGitStatusOutput(s string) ([]string, error) {
	var directories []string
	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		line := scanner.Text()
		file := line[strings.LastIndex(line, " ")+1:]
		if !strings.HasSuffix(file, "OWNERS") {
			return directories, fmt.Errorf("should not have modified the file: %s", file)
		}
		repo := path.Base(path.Dir(file))
		org := path.Base(path.Dir(path.Dir(file)))
		t := path.Base(path.Dir(path.Dir(path.Dir(file))))
		directories = append(directories, fmt.Sprintf("%s/%s/%s", t, org, repo))
	}
	return directories, nil
}

type blocklist struct {
	directories sets.String
	orgs        sets.String
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

	pc := plugins.Configuration{}
	if o.plugins.PluginConfigPath != "" {
		agent, err := o.plugins.PluginAgent()
		if err != nil {
			logrus.WithError(err).Fatal("failed to load plugin config file")
		}
		pc = *(agent.Config())
	}

	gc, err := o.GitHubOptions.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("error getting GitHub client")
	}
	gc.SetMax404Retries(0)

	logrus.Infof("Changing working directory to '%s' ...", o.targetDir)
	if err := os.Chdir(o.targetDir); err != nil {
		logrus.WithError(err).Fatal("Failed to change to root dir")
	}

	var configSubDirectories = o.configSubDirs.Strings()
	if len(configSubDirectories) == 0 {
		configSubDirectories = strings.Split(configSubDirs, ",")
	}
	configRootDirectory := filepath.Join(o.targetDir, o.targetSubDirectory)
	var blocked blocklist
	blocked.directories = sets.NewString(o.blockedRepos.Strings()...)
	blocked.orgs = sets.NewString(o.blockedOrgs.Strings()...)
	if err := pullOwners(gc, configRootDirectory, blocked, configSubDirectories, o.extraDirs.Strings(), o.githubOrg, o.githubRepo, pc); err != nil {
		logrus.WithError(err).Fatal("Error occurred when walking through the target dir.")
	}

	directories, err := listUpdatedDirectories()
	if err != nil {
		logrus.WithError(err).Fatal("Error occurred when listing updated directories.")
	}
	if len(directories) == 0 {
		logrus.Info("No OWNERS files got updated, exiting ...")
		return
	}

	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: secret.Censor}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: secret.Censor}

	remoteBranch := "autoowners"
	matchTitle := "Sync OWNERS files"
	title := getTitle(matchTitle, time.Now().Format(time.RFC1123))
	if err := bumper.GitCommitSignoffAndPush(fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin,
		string(secret.GetTokenGenerator(o.GitHubOptions.TokenPath)()), o.githubLogin, o.githubRepo),
		remoteBranch, o.gitName, o.gitEmail, title, stdout, stderr, o.gitSignoff, o.dryRun); err != nil {
		logrus.WithError(err).Fatal("Failed to push changes.")
	}

	var labelsToAdd []string
	if o.selfApprove {
		logrus.Infof("Self-aproving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}
	if err := bumper.UpdatePullRequestWithLabels(gc, o.githubOrg, o.githubRepo, title,
		getBody(directories, o.assign), o.githubLogin+":"+remoteBranch, o.prBaseBranch, remoteBranch, true, labelsToAdd, o.dryRun); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}

type ownersCleaner func([]string) []string

type githubOrgMemberLister interface {
	ListOrgMembers(org, role string) ([]github.TeamMember, error)
}

func ownersCleanerFactory(githubOrg string, ghc githubOrgMemberLister) (ownersCleaner, error) {
	members, err := ghc.ListOrgMembers(githubOrg, "all")
	if err != nil {
		return nil, fmt.Errorf("listOrgMembers failed: %w", err)
	}

	membersSet := sets.String{}
	for _, member := range members {
		membersSet.Insert(strings.ToLower(member.Login))
	}

	return func(unfilteredMembers []string) []string {
		var result []string
		for _, member := range unfilteredMembers {
			if lowercaseMember := strings.ToLower(member); membersSet.Has(lowercaseMember) {
				result = append(result, lowercaseMember)
			}
		}
		return result
	}, nil
}
