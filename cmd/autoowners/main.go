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
	"k8s.io/test-infra/prow/labels"
	"k8s.io/test-infra/prow/repoowners"
)

const (
	doNotEdit     = "# DO NOT EDIT; this file is auto-generated using https://github.com/openshift/ci-tools."
	ownersComment = "# See the OWNERS docs: https://git.k8s.io/community/contributors/guide/owners.md"

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
	ownersFileExists bool
}

// resolveOwnerAliases computes the resolved (simple or full config) format of the OWNERS file
func (r httpResult) resolveOwnerAliases(cleaner ownersCleaner) interface{} {
	if !r.simpleConfig.Empty() {
		return SimpleConfig{
			Config: repoowners.Config{
				Approvers:         cleaner(r.repoAliases.ExpandAliases(repoowners.NormLogins(r.simpleConfig.Approvers)).List()),
				Reviewers:         cleaner(r.repoAliases.ExpandAliases(repoowners.NormLogins(r.simpleConfig.Reviewers)).List()),
				RequiredReviewers: cleaner(r.repoAliases.ExpandAliases(repoowners.NormLogins(r.simpleConfig.RequiredReviewers)).List()),
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
				Approvers:         cleaner(r.repoAliases.ExpandAliases(repoowners.NormLogins(v.Approvers)).List()),
				Reviewers:         cleaner(r.repoAliases.ExpandAliases(repoowners.NormLogins(v.Reviewers)).List()),
				RequiredReviewers: cleaner(r.repoAliases.ExpandAliases(repoowners.NormLogins(v.RequiredReviewers)).List()),
				Labels:            sets.NewString(v.Labels...).List(),
			}
		}
		return fc
	}
}

type FileGetter interface {
	GetFile(org, repo, filepath, commit string) ([]byte, error)
}

func getOwnersHTTP(fg FileGetter, orgRepo orgRepo) (httpResult, error) {
	var httpResult httpResult

	for _, filename := range []string{"OWNERS", "OWNERS_ALIASES"} {
		data, err := fg.GetFile(orgRepo.Organization, orgRepo.Repository, filename, "")
		if err != nil {
			if _, nf := err.(*github.FileNotFound); nf {
				logrus.WithField("orgRepo", orgRepo.repoString()).WithField("filename", filename).
					Debug("Not found file in the upstream repo")
				continue
			} else {
				return httpResult, err
			}
		}

		switch filename {
		case "OWNERS":
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

func writeOwners(orgRepo orgRepo, httpResult httpResult, cleaner ownersCleaner) error {
	for _, directory := range orgRepo.Directories {
		path := filepath.Join(directory, "OWNERS")
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}

		logrus.WithField("path", path).Debug("Writing to path ...")
		err = nil
		config := httpResult.resolveOwnerAliases(cleaner)
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
		err = addHeader(path, strings.Join([]string{doNotEdit, ownersComment, "", ""}, "\n"))
		if err != nil {
			return err
		}
	}
	return nil
}

func pullOwners(gc github.Client, directory string, blacklist sets.String) error {
	orgRepos, err := loadRepos(directory, blacklist)
	if err != nil {
		return err
	}

	cleaner, err := ownersCleanerFactory(gc)
	if err != nil {
		return fmt.Errorf("failed to construct owners cleaner: %w", err)
	}

	for _, orgRepo := range orgRepos {
		logrus.WithField("orgRepo", orgRepo.repoString()).Info("handling repo ...")
		httpResult, err := getOwnersHTTP(gc, orgRepo)
		if err != nil {
			// TODO we might need to handle errors from `yaml.Unmarshal` if OWNERS is not a valid yaml file
			return err
		}
		if !httpResult.ownersFileExists {
			logrus.WithField("orgRepo", fmt.Sprintf("%s/%s", orgRepo.Organization, orgRepo.Repository)).
				Warn("Ignoring the repo with no OWNERS file in the upstream repo.")
			continue
		}
		if err := writeOwners(orgRepo, httpResult, cleaner); err != nil {
			return err
		}
	}

	return nil
}

type options struct {
	dryRun      bool
	githubLogin string
	gitName     string
	gitEmail    string
	assign      string
	targetDir   string
	blacklist   flagutil.Strings
	debugMode   bool
	selfApprove bool
	flagutil.GitHubOptions
}

func parseOptions() options {
	var o options
	//To avoid flag from dependencies such as https://github.com/openshift/ci-tools/blob/5b5410293f7cd318540d1fb333c68b93ddab2b60/vendor/github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1/artifact_pvc.go#L30
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually create the pull request with github client")
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.gitName, "git-name", "", "The name to use on the git commit. Requires --git-email. If not specified, uses the system default.")
	fs.StringVar(&o.gitEmail, "git-email", "", "The email to use on the git commit. Requires --git-name. If not specified, uses the system default.")
	fs.StringVar(&o.assign, "assign", githubTeam, "The github username or group name to assign the created pull request to.")
	fs.StringVar(&o.targetDir, "target-dir", "", "The directory containing the target repo.")
	fs.Var(&o.blacklist, "ignore-repo", "The repo for which syncing OWNERS file is disabled.")
	fs.BoolVar(&o.debugMode, "debug-mode", false, "Enable the DEBUG level of logs if true.")
	fs.BoolVar(&o.selfApprove, "self-approve", false, "Self-approve the PR by adding the `approved` and `lgtm` labels. Requires write permissions on the repo.")
	o.AddFlagsWithoutDefaultGitHubTokenPath(fs)
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
	if err := secretAgent.Start([]string{o.GitHubOptions.TokenPath}); err != nil {
		logrus.WithError(err).Fatalf("Error starting secrets agent.")
	}

	gc, err := o.GitHubOptions.GitHubClient(secretAgent, o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("error getting GitHub client")
	}

	logrus.Infof("Changing working directory to '%s' ...", o.targetDir)
	if err := os.Chdir(o.targetDir); err != nil {
		logrus.WithError(err).Fatal("Failed to change to root dir")
	}

	if err := pullOwners(gc, o.targetDir, sets.NewString(o.blacklist.Strings()...)); err != nil {
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
		string(secretAgent.GetTokenGenerator(o.GitHubOptions.TokenPath)()), o.githubLogin, githubRepo),
		remoteBranch, o.gitName, o.gitEmail, title, stdout, stderr); err != nil {
		logrus.WithError(err).Fatal("Failed to push changes.")
	}

	var labelsToAdd []string
	if o.selfApprove {
		logrus.Infof("Self-aproving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}
	if err := bumper.UpdatePullRequestWithLabels(gc, githubOrg, githubRepo, title,
		getBody(directories, o.assign), matchTitle, o.githubLogin+":"+remoteBranch, "master", labelsToAdd); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}

type ownersCleaner func([]string) []string

type githubOrgMemberLister interface {
	ListOrgMembers(org, role string) ([]github.TeamMember, error)
}

func ownersCleanerFactory(ghc githubOrgMemberLister) (ownersCleaner, error) {
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
