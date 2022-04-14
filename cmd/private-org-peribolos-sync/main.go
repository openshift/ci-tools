package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/config/org"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

type gitHubClient interface {
	GetRepo(owner, name string) (github.FullRepo, error)
}

type options struct {
	config.WhitelistOptions

	peribolosConfig string
	destOrg         string
	onlyOrg         string
	releaseRepoPath string
	github          flagutil.GitHubOptions
}

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.peribolosConfig, "peribolos-config", "", "Peribolos configuration file")
	fs.StringVar(&o.releaseRepoPath, "release-repo-path", "", "Path to a openshift/release repository directory")
	fs.StringVar(&o.destOrg, "destination-org", "", "Destination name of the peribolos configuration organzation")
	fs.StringVar(&o.onlyOrg, "only-org", "", "Only dump config of the repos belonging to this org.")

	o.github.AddFlags(fs)
	o.WhitelistOptions.Bind(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("faild to parse flags: %w", err)
	}

	return o, nil
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
	if err := o.github.Validate(false); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if err := o.Validate(); err != nil {
		validationErrors = append(validationErrors, err)
	}
	return utilerrors.NewAggregate(validationErrors)
}

func main() {
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}
	if err := validateOptions(&o); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}
	logger := logrus.WithField("destination-org", o.destOrg)

	go func() {
		interrupts.WaitForGracefulShutdown()
		os.Exit(1)
	}()

	b, err := gzip.ReadFileMaybeGZIP(o.peribolosConfig)
	if err != nil {
		logger.WithError(err).Fatal("could not read peribolos configuration file")
	}

	var peribolosConfig org.FullConfig
	if err := yaml.Unmarshal(b, &peribolosConfig); err != nil {
		logger.WithError(err).Fatal("failed to unmarshal peribolos config")
	}

	if err := secret.Add(o.github.TokenPath); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}
	gc, err := o.github.GitHubClient(false)
	if err != nil {
		logger.WithError(err).Fatal("Error getting GitHub client.")
	}

	orgRepos, err := getReposForPrivateOrg(o.releaseRepoPath, o.WhitelistOptions.WhitelistConfig.Whitelist, o.onlyOrg)
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
	yes := true

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
				Private:          &yes, // all repositories in private org should be private
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

// getReposForPrivateOrg iterates through the release repository directory and creates a map of
// repository sets by organization that promote official images.
func getReposForPrivateOrg(releaseRepoPath string, whitelist map[string][]string, onlyOrg string) (map[string]sets.String, error) {
	ret := make(map[string]sets.String)

	for org, repos := range whitelist {
		if onlyOrg != "" && onlyOrg != org {
			continue
		}
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

		if onlyOrg != "" && onlyOrg != i.Org {
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
