package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/experiment/autobumper/bumper"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/repoowners"
)

const (
	doNotEdit     = "# DO NOT EDIT; this file is auto-generated using tools/populate-owners.\n"
	ownersComment = "# See the OWNERS docs: https://git.k8s.io/community/contributors/guide/owners.md\n"
	//ownersAliasesComment = "# See the OWNERS_ALIASES docs: https://git.k8s.io/community/contributors/guide/owners.md#owners_aliases\n"

	githubOrg   = "openshift"
	githubRepo  = "release"
	githubLogin = "openshift-bot"
	githubTeam  = "openshift/openshift-team-developer-productivity-test-platform"
)

type owners = repoowners.Config

type aliases struct {
	Aliases map[string][]string `json:"aliases,omitempty" yaml:"aliases,omitempty"`
}

type orgRepo struct {
	Directories  []string `json:"directories,omitempty" yaml:"directories,omitempty"`
	Organization string   `json:"organization,omitempty" yaml:"organization,omitempty"`
	Repository   string   `json:"repository,omitempty" yaml:"repository,omitempty"`
	Owners       *owners  `json:"owners,omitempty" yaml:"owners,omitempty"`
	Aliases      *aliases `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Commit       string   `json:"commit,omitempty" yaml:"commit,omitempty"`
}

func getRepoRoot(directory string) (root string, err error) {
	initialDir, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}

	path := initialDir
	for {
		info, err := os.Stat(filepath.Join(path, ".git"))
		if err == nil {
			if info.IsDir() {
				break
			}
		} else if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(path)
		if parent == path {
			return "", fmt.Errorf("no .git found under %q", initialDir)
		}

		path = parent
	}

	return path, nil
}

func orgRepos(dir string) (orgRepos []*orgRepo, err error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*", "*"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)

	orgRepos = make([]*orgRepo, 0, len(matches))
	for _, path := range matches {
		relpath, err := filepath.Rel(dir, path)
		if err != nil {
			return nil, err
		}
		org, repo := filepath.Split(relpath)
		org = strings.TrimSuffix(org, string(filepath.Separator))
		if org == "openshift" && repo == "release" {
			continue
		}
		orgRepos = append(orgRepos, &orgRepo{
			Directories:  []string{path},
			Organization: org,
			Repository:   repo,
		})
	}

	return orgRepos, err
}

func (orgRepo *orgRepo) String() string {
	return fmt.Sprintf("%s/%s", orgRepo.Organization, orgRepo.Repository)
}

func (orgRepo *orgRepo) getDirectories(dirs ...string) (err error) {
	for _, dir := range dirs {
		path := filepath.Join(dir, orgRepo.Organization, orgRepo.Repository)
		info, err := os.Stat(path)
		if err != nil {
			return err
		}

		if info.IsDir() {
			orgRepo.Directories = append(orgRepo.Directories, path)
		}
	}

	return nil
}

// getOwnersHTTP is fast (just the two files we need), but only works
// on public repos unless you have an auth token.
func (orgRepo *orgRepo) getOwnersHTTP() (err error) {
	sc, err := gc.GetSingleCommit(orgRepo.Organization, orgRepo.Repository, "HEAD")
	if err != nil {
		return err
	}

	for _, filename := range []string{"OWNERS", "OWNERS_ALIASES"} {
		data, err := gc.GetFile(orgRepo.Organization, orgRepo.Repository, filename, "HEAD")
		if err != nil {
			if _, nf := err.(*github.FileNotFound); nf {
				continue
			} else {
				return err
			}
		}

		var target interface{}
		switch filename {
		case "OWNERS":
			target = &orgRepo.Owners
		case "OWNERS_ALIASES":
			target = &orgRepo.Aliases
		default:
			return fmt.Errorf("unrecognized filename %q", target)
		}
		err = yaml.Unmarshal(data, target)
		if err != nil {
			logrus.WithField("data", string(data)).WithField("filename", filename).
				WithField("orgRepo.Organization", orgRepo.Organization).
				WithField("orgRepo.Repository", orgRepo.Repository).
				WithError(err).Error("Unable to parse data.")
			return err
		}
	}

	if orgRepo.Owners == nil && orgRepo.Aliases == nil {
		return nil
	}

	orgRepo.Commit = sc.Commit.Tree.SHA
	return nil

}

func writeYAML(path string, data interface{}, prefix []string) (rerr error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	defer func() {
		err := file.Close()
		if err != nil {
			rerr = err
		}
	}()

	for _, line := range prefix {
		_, err := file.Write([]byte(line))
		if err != nil {
			return err
		}
	}

	// https://github.com/ghodss/yaml
	// respects the tags for json
	bytes, err := yaml.Marshal(data)
	if err != nil {
		return err
	}
	_, err = file.Write(bytes)
	return err
}

// insertStringSlice inserts a string slice into another string slice
// replacing the elements starting with the begin index up to the end
// index.  The element at end index in the original slice will remain
// in the resulting slice.  Returns a new slice with the elements
// replaced. If the begin index is larger than the end, or either of the
// indexes are out of range of the slice, the original slice is returned
// unmodified.
func insertStringSlice(insert []string, intoSlice []string,
	begin int, end int) []string {
	if begin > end || begin < 0 || end > len(intoSlice) {
		return intoSlice
	}
	firstPart := intoSlice[:begin]
	secondPart := append(insert, intoSlice[end:]...)
	return append(firstPart, secondPart...)
}

// resolveAliases resolves names in the list of owners that
// match one of the given aliases.  Returns a list of owners
// with each alias replaced by the list of owners it represents.
func resolveAliases(aliases *aliases, owners []string) []string {
	offset := 0 // Keeps track of how many new names we've inserted
	for i, owner := range owners {
		if aliasOwners, ok := aliases.Aliases[owner]; ok {
			index := i + offset
			owners = insertStringSlice(aliasOwners, owners, index, (index + 1))
			offset += len(aliasOwners) - 1
		}
	}
	return owners
}

// resolveOwnerAliases checks whether the orgRepo includes any
// owner aliases, and attempts to resolve them to the appropriate
// set of owners.  Returns an owners which replaces any
// matching aliases with the set of owner names belonging to that alias.
func (orgRepo *orgRepo) resolveOwnerAliases() *owners {
	if orgRepo.Aliases == nil || len(orgRepo.Aliases.Aliases) == 0 {
		return orgRepo.Owners
	}

	return &owners{
		Approvers:         resolveAliases(orgRepo.Aliases, orgRepo.Owners.Approvers),
		Reviewers:         resolveAliases(orgRepo.Aliases, orgRepo.Owners.Reviewers),
		RequiredReviewers: orgRepo.Owners.RequiredReviewers,
		Labels:            orgRepo.Owners.Labels,
	}
}

func (orgRepo *orgRepo) writeOwners(whitelist []string) (err error) {
	for _, directory := range orgRepo.Directories {
		if inWhitelist(directory, whitelist) {
			logrus.WithField("directory", directory).Info("Ignoring the directory in the white list.")
			continue
		}
		path := filepath.Join(directory, "OWNERS")
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if orgRepo.Owners == nil {
			continue
		}

		err = writeYAML(path, orgRepo.resolveOwnerAliases(), []string{
			doNotEdit,
			fmt.Sprintf(
				"# from https://github.com/%s/%s/blob/%s/OWNERS\n",
				orgRepo.Organization,
				orgRepo.Repository,
				orgRepo.Commit,
			),
			ownersComment,
			"\n",
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func pullOwners(directory string, whitelist []string) ([]string, error) {
	var repos []string
	repoRoot, err := getRepoRoot(directory)
	if err != nil {
		return repos, err
	}

	operatorRoot := filepath.Join(repoRoot, "ci-operator")
	orgRepos, err := orgRepos(filepath.Join(operatorRoot, "jobs"))
	if err != nil {
		return repos, err
	}

	config := filepath.Join(operatorRoot, "config")
	templates := filepath.Join(operatorRoot, "templates")
	for _, orgRepo := range orgRepos {
		logrus.WithField("orgRepo", fmt.Sprintf("%+v", *orgRepo)).Info("handling repo ...")
		err = orgRepo.getDirectories(config, templates)
		if err != nil && !os.IsNotExist(err) {
			return repos, err
		}

		err = orgRepo.getOwnersHTTP()
		if err != nil && !os.IsNotExist(err) {
			return repos, err
		}

		err = orgRepo.writeOwners(whitelist)
		if err != nil {
			return repos, err
		}
		repoStr := orgRepo.String()
		repos = append(repos, repoStr)
		fmt.Fprintf(os.Stderr, "updated owners for %s\n", repoStr)
	}

	return repos, err
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
	whitelist   flagutil.Strings
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
	flag.Var(&o.whitelist, "ignore-repo", "The repo that syncing OWNERS file is disabled.")
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

func inWhitelist(path string, whitelist []string) bool {
	for _, e := range whitelist {
		if strings.HasSuffix(path, e) {
			return true
		}
	}
	return false
}

func getBody(repos []string, assign string) string {
	body := "The OWNERS file has been synced for the following repo(s):\n\n"
	for _, r := range repos {
		body = fmt.Sprintf("%s* %s\n", body, r)
	}
	body = fmt.Sprintf("%s\n%s\n", body, "/cc @"+assign)
	return body
}

func getTitle(matchTitle, datetime string) string {
	return fmt.Sprintf("%s by autoowners job at %s", matchTitle, datetime)
}

func main() {
	o := parseOptions()
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("Invalid arguments.")
	}

	secretAgent := &secret.Agent{}
	if err := secretAgent.Start([]string{o.githubToken}); err != nil {
		logrus.WithError(err).Fatalf("Error starting secrets agent.")
	}
	gc = github.NewClient(secretAgent.GetTokenGenerator(o.githubToken), secretAgent.Censor, github.DefaultGraphQLEndpoint, github.DefaultAPIEndpoint)

	repos, err := pullOwners(o.targetDir, o.whitelist.Strings())

	if err != nil {
		logrus.WithError(err).Fatal("Error occurred when walking through the target dir.")
	}

	if len(repos) == 0 {
		logrus.Info("No OWNERS file to update, exiting ...")
		return
	}

	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: secretAgent}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: secretAgent}

	remoteBranch := "autoowners"
	if err := bumper.GitCommitAndPush(fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin,
		string(secretAgent.GetTokenGenerator(o.githubToken)()), o.githubLogin, githubRepo),
		remoteBranch, o.gitName, o.gitEmail, "", stdout, stderr); err != nil {
		logrus.WithError(err).Fatal("Failed to push changes.")
	}

	matchTitle := "Sync OWNERS files"
	if err := bumper.UpdatePullRequest(gc, githubOrg, githubRepo, getTitle(matchTitle, time.Now().Format(time.RFC1123)),
		getBody(repos, o.assign), matchTitle, o.githubLogin+":"+remoteBranch, "master"); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}
