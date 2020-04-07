package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"k8s.io/test-infra/prow/config/org"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

type gitHubClient interface {
	GetRepo(owner, name string) (github.FullRepo, error)
}

type options struct {
	peribolosConfig string
	destOrg         string
	releaseRepoPath string
	whitelist       flagutil.Strings
	github          flagutil.GitHubOptions

	whitelistByOrg map[string]sets.String
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.peribolosConfig, "peribolos-config", "", "Peribolos configuration file")
	fs.StringVar(&o.releaseRepoPath, "release-repo-path", "", "Path to a openshift/release repository directory")
	fs.StringVar(&o.destOrg, "destination-org", "", "Destination name of the peribolos configuration organzation")

	fs.Var(&o.whitelist, "whitelist", "One or more repositories that do not promote official images and need to be included")

	o.github.AddFlags(fs)
	fs.Parse(os.Args[1:])

	return o
}

func validateOptions(o *options) error {
	var validationErrors []error

	if len(o.releaseRepoPath) == 0 {
		validationErrors = append(validationErrors, errors.New("--release-repo-path is not specified"))
	}
	if len(o.peribolosConfig) == 0 {
		validationErrors = append(validationErrors, errors.New("--peribolos-config is not specified"))
	}
	if len(o.destOrg) == 0 {
		validationErrors = append(validationErrors, errors.New("--destination-org is not specified"))
	}

	if len(o.whitelist.Strings()) > 0 {
		if err := validateWhitelist(o.whitelist.Strings()); err != nil {
			validationErrors = append(validationErrors, err)
		} else {
			o.whitelistByOrg = getWhitelistByOrg(o.whitelist.Strings())
		}
	}

	if err := o.github.Validate(false); err != nil {
		validationErrors = append(validationErrors, err)
	}

	return kerrors.NewAggregate(validationErrors)
}

func validateWhitelist(whitelist []string) error {
	for _, repo := range whitelist {
		if len(strings.Split(repo, "/")) != 2 {
			return fmt.Errorf("--whitelist: %s must be in org/repo format", repo)
		}
	}
	return nil
}

func getWhitelistByOrg(whitelist []string) map[string]sets.String {
	ret := make(map[string]sets.String)

	for _, repo := range whitelist {
		orgRepo := strings.Split(repo, "/")

		orgName := orgRepo[0]
		repoName := orgRepo[1]

		repos, exist := ret[orgName]
		if !exist {
			repos = sets.NewString()
		}
		ret[orgName] = repos.Insert(repoName)

	}
	return ret
}

func main() {
	o := gatherOptions()
	err := validateOptions(&o)
	if err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}
	logger := logrus.WithField("destination-org", o.destOrg)

	go func() {
		interrupts.WaitForGracefulShutdown()
		os.Exit(1)
	}()

	b, err := ioutil.ReadFile(o.peribolosConfig)
	if err != nil {
		logger.WithError(err).Fatal("could not read peribolos configuration file")
	}

	var peribolosConfig org.FullConfig
	if err := yaml.Unmarshal(b, &peribolosConfig); err != nil {
		logger.WithError(err).Fatal("failed to unmarshal peribolos config")
	}

	secretAgent := &secret.Agent{}
	if err := secretAgent.Start([]string{o.github.TokenPath}); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}
	gc, err := o.github.GitHubClient(secretAgent, false)
	if err != nil {
		logger.WithError(err).Fatal("Error getting GitHub client.")
	}

	orgRepos, err := getReposForPrivateOrg(o.releaseRepoPath, o.whitelistByOrg)
	if err != nil {
		logger.WithError(err).Fatal("couldn't get the list of org/repos that promote official images")
	}

	peribolosRepos := generateRepositories(gc, orgRepos, logger)
	peribolosConfigByOrg := peribolosConfig.Orgs[o.destOrg]
	peribolosConfigByOrg.Repos = peribolosRepos
	peribolosConfig.Orgs[o.destOrg] = peribolosConfigByOrg

	out, err := yaml.Marshal(peribolosConfig)
	if err != nil {
		logrus.WithError(err).Fatalf("%s failed to marshal output.", o.peribolosConfig)
	}

	if err := ioutil.WriteFile(o.peribolosConfig, out, 0666); err != nil {
		logrus.WithError(err).Fatal("Failed to write output.")
	}
}

func generateRepositories(gc gitHubClient, orgRepos map[string]sets.String, logger *logrus.Entry) map[string]org.Repo {
	peribolosRepos := make(map[string]org.Repo)

	for orgName, repos := range orgRepos {
		for repo := range repos {
			logger.WithFields(logrus.Fields{"org": orgName, "repo": repo}).Info("Processing repository details...")

			fullRepo, err := gc.GetRepo(orgName, repo)
			if err != nil {
				logger.WithError(err).Fatal("couldn't get repo details")
			}

			peribolosRepos[fullRepo.Name] = org.PruneRepoDefaults(org.Repo{
				Description:      &fullRepo.Description,
				HomePage:         &fullRepo.Homepage,
				Private:          &fullRepo.Private,
				HasIssues:        &fullRepo.HasIssues,
				HasProjects:      &fullRepo.HasProjects,
				HasWiki:          &fullRepo.HasWiki,
				AllowMergeCommit: &fullRepo.AllowMergeCommit,
				AllowSquashMerge: &fullRepo.AllowSquashMerge,
				AllowRebaseMerge: &fullRepo.AllowRebaseMerge,
				Archived:         &fullRepo.Archived,
				DefaultBranch:    &fullRepo.DefaultBranch,
			})
		}
	}

	return peribolosRepos
}

// getReposForPrivateOrg itterates through the release repository directory and creates a map of
// repository sets by organization that promote official images or if they are whitelisted by user.
func getReposForPrivateOrg(releaseRepoPath string, includeRepos map[string]sets.String) (map[string]sets.String, error) {
	ret := make(map[string]sets.String)

	callback := makeCallback(includeRepos, ret)
	if err := config.OperateOnCIOperatorConfigDir(filepath.Join(releaseRepoPath, config.CiopConfigInRepoPath), callback); err != nil {
		return ret, fmt.Errorf("error while operating in ci-operator configuration files: %v", err)
	}

	return ret, nil
}

// makeCallback returns a function usable for OperateOnCIOperatorConfigDir that picks the orgs/repos
// that promotes official images and the one that are in whitelist.
func makeCallback(includeRepos, orgReposPicked map[string]sets.String) func(*api.ReleaseBuildConfiguration, *config.Info) error {
	return func(c *api.ReleaseBuildConfiguration, i *config.Info) error {

		// skip this repo unless it's in the whitelist
		if !promotion.BuildsOfficialImages(c) {
			if repos, _ := includeRepos[i.Org]; !repos.Has(i.Repo) {
				return nil
			}
		}

		repos, exist := orgReposPicked[i.Org]
		if !exist {
			repos = sets.NewString()
		}
		orgReposPicked[i.Org] = repos.Insert(i.Repo)

		return nil
	}
}
