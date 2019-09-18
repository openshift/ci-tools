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

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/experiment/autobumper/bumper"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/repoowners"
)

const (
	doNotEdit     = "# DO NOT EDIT; this file is auto-generated using tools/populate-owners."
	ownersComment = "# See the OWNERS docs: https://git.k8s.io/community/contributors/guide/owners.md"
	//ownersAliasesComment = "# See the OWNERS_ALIASES docs: https://git.k8s.io/community/contributors/guide/owners.md#owners_aliases\n"

	githubOrg   = "openshift"
	githubRepo  = "release"
	githubLogin = "openshift-bot"
	githubTeam  = "openshift/openshift-team-developer-productivity-test-platform"
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

func loadRepos(dir string, blacklist sets.String) ([]orgRepo, error) {
	var orgRepos []orgRepo
	operatorRoot := filepath.Join(dir, "ci-operator")
	jobs := filepath.Join(operatorRoot, "jobs")
	config := filepath.Join(operatorRoot, "config")
	templates := filepath.Join(operatorRoot, "templates")

	orgDirs, err := ioutil.ReadDir(jobs)
	if err != nil {
		return nil, err
	}
	for _, orgDir := range orgDirs {
		if !orgDir.IsDir() {
			continue
		}
		logrus.WithField("orgDir.Name()", orgDir.Name()).Debug("loading orgDir ...")
		org := filepath.Base(orgDir.Name())
		repoDirs, err := ioutil.ReadDir(filepath.Join(jobs, orgDir.Name()))
		if err != nil {
			return nil, err
		}
		for _, repoDir := range repoDirs {
			if !orgDir.IsDir() {
				continue
			}
			repo := repoDir.Name()
			if org == "openshift" && repo == "release" {
				continue
			}
			var dirs []string
			for _, d := range []string{filepath.Join(jobs, org, repo), filepath.Join(config, org, repo), filepath.Join(templates, org, repo)} {
				fileInfo, err := os.Stat(d)
				logrus.WithField("err", err).Debug("os.Stat(d): checking error ...")
				if !os.IsNotExist(err) && fileInfo.IsDir() {
					logrus.WithField("d", d).WithField("blacklist", blacklist.List()).
						Debug("trying to determine if the directory is in the blacklist")
					if blacklist.Has(d) {
						logrus.WithField("directory", d).Info("Ignoring the directory in the blacklist.")
						continue
					}
					dirs = append(dirs, d)
				}
			}
			orgRepos = append(orgRepos, orgRepo{
				Directories:  dirs,
				Organization: org,
				Repository:   repo,
			})
		}
	}
	return orgRepos, err
}

type httpResult struct {
	simpleConfig     SimpleConfig
	fullConfig       FullConfig
	repoAliases      RepoAliases
	commit           string
	ownersFileExists bool
}

// resolveOwnerAliases computes the resolved (simple or full config) format of the OWNERS file
func (r httpResult) resolveOwnerAliases() interface{} {
	if !r.simpleConfig.Empty() {
		return SimpleConfig{
			Config: repoowners.Config{
				Approvers:         r.repoAliases.ExpandAliases(repoowners.NormLogins(r.simpleConfig.Approvers)).List(),
				Reviewers:         r.repoAliases.ExpandAliases(repoowners.NormLogins(r.simpleConfig.Reviewers)).List(),
				RequiredReviewers: r.repoAliases.ExpandAliases(repoowners.NormLogins(r.simpleConfig.RequiredReviewers)).List(),
				Labels:            sets.NewString(r.simpleConfig.Labels...).List(),
			},
			Options: r.simpleConfig.Options,
		}
	} else {
		fc := FullConfig{
			Filters: map[string]repoowners.Config{},
			Options: r.fullConfig.Options,
		}
		for k, v := range r.fullConfig.Filters {
			fc.Filters[k] = repoowners.Config{
				Approvers:         r.repoAliases.ExpandAliases(repoowners.NormLogins(v.Approvers)).List(),
				Reviewers:         r.repoAliases.ExpandAliases(repoowners.NormLogins(v.Reviewers)).List(),
				RequiredReviewers: r.repoAliases.ExpandAliases(repoowners.NormLogins(v.RequiredReviewers)).List(),
				Labels:            sets.NewString(v.Labels...).List(),
			}
		}
		return fc
	}
}

func getOwnersHTTP(orgRepo orgRepo) (httpResult, error) {
	var httpResult httpResult
	sha, err := gc.GetRef(orgRepo.Organization, orgRepo.Repository, "heads/master")
	if err != nil {
		return httpResult, err
	}
	httpResult.commit = sha

	for _, filename := range []string{"OWNERS", "OWNERS_ALIASES"} {
		data, err := gc.GetFile(orgRepo.Organization, orgRepo.Repository, filename, "")
		if err != nil {
			if _, nf := err.(*github.FileNotFound); nf {
				logrus.WithField("orgRepo", orgRepo.repoString()).WithField("filename", filename).
					Debug("Not found file in the upstream repo")
				httpResult.ownersFileExists = false
				continue
			} else {
				return httpResult, err
			}
		}

		httpResult.ownersFileExists = true
		switch filename {
		case "OWNERS":
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
		case "OWNERS_ALIASES":
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

func writeOwners(orgRepo orgRepo, httpResult httpResult) error {
	for _, directory := range orgRepo.Directories {
		path := filepath.Join(directory, "OWNERS")
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}

		logrus.WithField("path", path).Debug("Writing to path ...")
		err = nil
		config := httpResult.resolveOwnerAliases()
		switch config.(type) {
		case SimpleConfig:
			err = repoowners.SaveSimpleConfig(config.(SimpleConfig), path)
		case FullConfig:
			err = repoowners.SaveFullConfig(config.(FullConfig), path)
		default:
			return fmt.Errorf("unknown config type: %+v", config)
		}
		if err != nil {
			logrus.WithError(err).Error("error occurred when saving config")
			return err
		}
		err = addHeader(path, fmt.Sprintf("%s\n# from https://github.com/%s/%s/blob/%s/OWNERS\n%s\n\n",
			doNotEdit,
			orgRepo.Organization,
			orgRepo.Repository,
			httpResult.commit,
			ownersComment))
		if err != nil {
			return err
		}
	}
	return nil
}

func pullOwners(directory string, blacklist sets.String) error {
	orgRepos, err := loadRepos(directory, blacklist)
	if err != nil {
		return err
	}

	for _, orgRepo := range orgRepos {
		logrus.WithField("orgRepo", orgRepo.repoString()).Info("handling repo ...")
		httpResult, err := getOwnersHTTP(orgRepo)
		if err != nil {
			// TODO we might need to handle errors from `yaml.Unmarshal` if OWNERS is not a valid yaml file
			return err
		}
		if !httpResult.ownersFileExists {
			logrus.WithField("orgRepo", fmt.Sprintf("%s/%s", orgRepo.Organization, orgRepo.Repository)).
				Warn("Ignoring the repo with no OWNERS file in the upstream repo.")
			continue
		}
		if err := writeOwners(orgRepo, httpResult); err != nil {
			return err
		}
	}

	return nil
}

const (
	usage = `Update the OWNERS files from remote repositories.

Usage:
  %s [repo-name-regex]

Args:
  [repo-name-regex]    A go regex which which matches the repos to update, by default all repos are selected

`
)

var (
	gc github.Client
)

type options struct {
	githubLogin string
	githubToken string
	gitName     string
	gitEmail    string
	assign      string
	targetDir   string
	blacklist   flagutil.Strings
	debugMode   bool
}

func parseOptions() options {
	var o options
	//To avoid flag from dependencies such as https://github.com/openshift/ci-tools/blob/5b5410293f7cd318540d1fb333c68b93ddab2b60/vendor/github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1/artifact_pvc.go#L30
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flag.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	flag.StringVar(&o.githubToken, "github-token", "", "The path to the GitHub token file.")
	flag.StringVar(&o.gitName, "git-name", "", "The name to use on the git commit. Requires --git-email. If not specified, uses the system default.")
	flag.StringVar(&o.gitEmail, "git-email", "", "The email to use on the git commit. Requires --git-name. If not specified, uses the system default.")
	flag.StringVar(&o.assign, "assign", githubTeam, "The github username or group name to assign the created pull request to.")
	flag.StringVar(&o.targetDir, "target-dir", "", "The directory containing the target repo.")
	flag.Var(&o.blacklist, "ignore-repo", "The repo for which syncing OWNERS file is disabled.")
	flag.BoolVar(&o.debugMode, "debug-mode", false, "Enable the DEBUG level of logs if true.")
	flag.Parse()
	return o
}

func validateOptions(o options) error {
	if o.githubLogin == "" {
		return fmt.Errorf("--github-login is mandatory")
	}
	if o.githubToken == "" {
		return fmt.Errorf("--github-token is mandatory")
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
	return nil
}

func getBody(directories []string, assign string) string {
	lines := []string{"The OWNERS file has been synced for the following folder(s):", ""}
	for _, d := range directories {
		lines = append(lines, fmt.Sprintf("* %s", d))
	}
	lines = append(lines, []string{"", fmt.Sprintf("/cc @%s", assign), ""}...)
	return strings.Join(lines, "\n")
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

func listUpdatedDirectories(sa *secret.Agent) ([]string, error) {
	w := &OutputWriter{}
	e := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: sa}
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

func main() {
	o := parseOptions()
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("Invalid arguments.")
	}

	if o.debugMode {
		logrus.Info("debug mode is enabled")
		logrus.SetLevel(logrus.DebugLevel)
	}

	secretAgent := &secret.Agent{}
	if err := secretAgent.Start([]string{o.githubToken}); err != nil {
		logrus.WithError(err).Fatalf("Error starting secrets agent.")
	}
	gc = github.NewClient(secretAgent.GetTokenGenerator(o.githubToken), secretAgent.Censor, github.DefaultGraphQLEndpoint, github.DefaultAPIEndpoint)

	logrus.Infof("Changing working directory to %s...", o.targetDir)
	if err := os.Chdir(o.targetDir); err != nil {
		logrus.WithError(err).Fatal("Failed to change to root dir")
	}

	if err := pullOwners(o.targetDir, sets.NewString(o.blacklist.Strings()...)); err != nil {
		logrus.WithError(err).Fatal("Error occurred when walking through the target dir.")
	}

	directories, err := listUpdatedDirectories(secretAgent)
	if err != nil {
		logrus.WithError(err).Fatal("Error occurred when listing updated directories.")
	}
	if len(directories) == 0 {
		logrus.Info("No OWNERS files got updated, exiting ...")
		return
	}

	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: secretAgent}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: secretAgent}

	remoteBranch := "autoowners"
	matchTitle := "Sync OWNERS files"
	title := getTitle(matchTitle, time.Now().Format(time.RFC1123))
	if err := bumper.GitCommitAndPush(fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin,
		string(secretAgent.GetTokenGenerator(o.githubToken)()), o.githubLogin, githubRepo),
		remoteBranch, o.gitName, o.gitEmail, title, stdout, stderr); err != nil {
		logrus.WithError(err).Fatal("Failed to push changes.")
	}

	if err := bumper.UpdatePullRequest(gc, githubOrg, githubRepo, title,
		getBody(directories, o.assign), matchTitle, o.githubLogin+":"+remoteBranch, "master"); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}
