package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"

	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/plugins"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

const (
	openshiftPrivOrg   = "openshift-priv"
	approvePluginValue = "approve"
)

type options struct {
	releaseRepoPath string
}

type orgReposWithOfficialImages map[string]sets.String

func (o orgReposWithOfficialImages) isOfficialRepo(org, repo string) bool {
	if _, ok := o[org]; ok {
		if o[org].Has(repo) {
			return true
		}
	}
	return false
}

func (o *orgReposWithOfficialImages) isOfficialRepoFull(orgRepo string) bool {
	if orgRepoList := strings.Split(orgRepo, "/"); len(orgRepoList) == 2 {
		return o.isOfficialRepo(orgRepoList[0], orgRepoList[1])
	}
	return false
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.releaseRepoPath, "release-repo-path", "", "Path to a openshift/release repository directory")
	fs.Parse(os.Args[1:])
	return o
}

func (o *options) validate() error {
	if len(o.releaseRepoPath) == 0 {
		return errors.New("--release-repo-path is not defined")
	}
	return nil
}

func loadProwPlugins(pluginsPath string) (*plugins.Configuration, error) {
	agent := plugins.ConfigAgent{}
	if err := agent.Load(pluginsPath, false); err != nil {
		return nil, err
	}
	return agent.Config(), nil
}

func updateProwConfig(configFile string, config prowconfig.ProwConfig) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("could not marshal Prow configuration: %v", err)
	}

	return ioutil.WriteFile(configFile, data, 0644)
}

func updateProwPlugins(pluginsFile string, config *plugins.Configuration) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("could not marshal Prow configuration: %v", err)
	}

	return ioutil.WriteFile(pluginsFile, data, 0644)
}

func privateOrgRepo(repo string) string {
	return fmt.Sprintf("%s/%s", openshiftPrivOrg, repo)
}

func getOrgReposWithOfficialImages(releaseRepoPath string) (orgReposWithOfficialImages, error) {
	ret := make(orgReposWithOfficialImages)

	callback := func(c *api.ReleaseBuildConfiguration, i *config.Info) error {
		if !promotion.BuildsOfficialImages(c) {
			return nil
		}

		if _, ok := ret[i.Org]; !ok {
			ret[i.Org] = sets.NewString(i.Repo)
		} else {
			ret[i.Org].Insert(i.Repo)
		}

		return nil
	}

	if err := config.OperateOnCIOperatorConfigDir(filepath.Join(releaseRepoPath, config.CiopConfigInRepoPath), callback); err != nil {
		return ret, fmt.Errorf("error while operating in ci-operator configuration files: %v", err)
	}

	return ret, nil
}

func injectPrivateBranchProtection(branchProtection prowconfig.BranchProtection, orgRepos orgReposWithOfficialImages) {
	privateOrg := prowconfig.Org{
		Repos: make(map[string]prowconfig.Repo),
	}

	logrus.Info("Processing...")
	for orgName, orgValue := range branchProtection.Orgs {
		for repoName, repoValue := range orgValue.Repos {
			if orgRepos.isOfficialRepo(orgName, repoName) {
				logrus.WithField("repo", repoName).Info("Found")
				privateOrg.Repos[repoName] = repoValue
			}
		}

	}

	if len(privateOrg.Repos) > 0 {
		branchProtection.Orgs[openshiftPrivOrg] = privateOrg
	}
}

func injectPrivateTideOrgContextPolicy(contextOptions prowconfig.TideContextPolicyOptions, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")
	privateOrgRepos := make(map[string]prowconfig.TideRepoContextPolicy)

	for orgName, orgValue := range contextOptions.Orgs {
		for repoName, repoValue := range orgValue.Repos {
			if orgRepos.isOfficialRepo(orgName, repoName) {
				logrus.WithField("repo", repoName).Info("Found")
				privateOrgRepos[repoName] = repoValue
			}
		}
	}

	if len(privateOrgRepos) > 0 {
		contextOptions.Orgs[openshiftPrivOrg] = prowconfig.TideOrgContextPolicy{Repos: privateOrgRepos}
	}
}

func injectPrivateReposTideQueries(tideQueries []prowconfig.TideQuery, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	for index, tideQuery := range tideQueries {
		repos := sets.NewString(tideQuery.Repos...)

		for _, orgRepo := range tideQuery.Repos {
			if orgRepos.isOfficialRepoFull(orgRepo) {
				repo := strings.Split(orgRepo, "/")[1]

				logrus.WithField("repo", repo).Info("Found")
				repos.Insert(privateOrgRepo(repo))
			}
		}

		tideQueries[index].Repos = repos.List()
	}
}

func injectPrivateMergeType(tideMergeTypes map[string]github.PullRequestMergeType, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	for orgRepo, value := range tideMergeTypes {
		if orgRepos.isOfficialRepoFull(orgRepo) {
			repo := strings.Split(orgRepo, "/")[1]

			logrus.WithField("repo", repo).Info("Found")
			tideMergeTypes[privateOrgRepo(repo)] = value
		}
	}
}

func injectPrivatePRStatusBaseURLs(prStatusBaseURLs map[string]string, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	for orgRepo, value := range prStatusBaseURLs {
		if orgRepos.isOfficialRepoFull(orgRepo) {
			repo := strings.Split(orgRepo, "/")[1]

			logrus.WithField("repo", repo).Info("Found")
			prStatusBaseURLs[privateOrgRepo(repo)] = value
		}
	}
}

func injectPrivatePlankDefaultDecorationConfigs(defaultDecorationConfigs map[string]*prowapi.DecorationConfig, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	for orgRepo, value := range defaultDecorationConfigs {
		if orgRepos.isOfficialRepoFull(orgRepo) {
			repo := strings.Split(orgRepo, "/")[1]

			logrus.WithField("repo", repo).Info("Found")
			defaultDecorationConfigs[privateOrgRepo(repo)] = value
		}
	}
}

func injectPrivateJobURLPrefixConfig(jobURLPrefixConfig map[string]string, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	for orgRepo, value := range jobURLPrefixConfig {
		if orgRepos.isOfficialRepoFull(orgRepo) {
			repo := strings.Split(orgRepo, "/")[1]

			logrus.WithField("repo", repo).Info("Found")
			jobURLPrefixConfig[privateOrgRepo(repo)] = value
		}
	}
}

func injectPrivateApprovePlugin(approves []plugins.Approve, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	for index, approve := range approves {
		repos := sets.NewString(approve.Repos...)

		for _, orgRepo := range approve.Repos {
			if orgRepos.isOfficialRepoFull(orgRepo) {
				repo := strings.Split(orgRepo, "/")[1]

				logrus.WithField("repo", repo).Info("Found")
				repos.Insert(privateOrgRepo(repo))
			}
		}

		approves[index].Repos = repos.List()
	}
}

func injectPrivateLGTMPlugin(lgtms []plugins.Lgtm, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	for index, lgtm := range lgtms {
		repos := sets.NewString(lgtm.Repos...)

		for _, orgRepo := range lgtm.Repos {
			if orgRepos.isOfficialRepoFull(orgRepo) {
				repo := strings.Split(orgRepo, "/")[1]

				logrus.WithField("repo", repo).Info("Found")
				repos.Insert(privateOrgRepo(repo))
			}
		}

		lgtms[index].Repos = repos.List()
	}
}

func injectPrivateBugzillaPlugin(bugzillaPlugins plugins.Bugzilla, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	privateRepos := make(map[string]plugins.BugzillaRepoOptions)
	for org, orgValue := range bugzillaPlugins.Orgs {
		for repo, value := range orgValue.Repos {
			if orgRepos.isOfficialRepo(org, repo) {
				logrus.WithField("repo", repo).Info("Found")
				privateRepos[repo] = value
			}
		}
	}

	if len(privateRepos) > 0 {
		bugzillaPlugins.Orgs[openshiftPrivOrg] = plugins.BugzillaOrgOptions{Repos: privateRepos}
	}
}

func injectPrivatePlugins(plugins map[string][]string, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	var allPrivate []string

	privateReposWithValues := make(map[string][]string)

	for orgRepo, value := range plugins {
		if orgRepos.isOfficialRepoFull(orgRepo) {
			newValue := value

			// We want to append the org level values, because the private repo
			// can exist in different org with different configuration.
			org := strings.Split(orgRepo, "/")[0]
			if _, ok := plugins[org]; ok {
				newValue = append(newValue, plugins[org]...)
			}

			privateReposWithValues[privateOrgRepo(strings.Split(orgRepo, "/")[1])] = newValue
			allPrivate = append(allPrivate, newValue...)
		}
	}

	mergeCommonPrivatePlugins(plugins, privateReposWithValues, getRepeatedValues(allPrivate))
}

// mergeCommonPrivatePlugins detects the common values from all the private repositories
// and merges them into the org `openshift-priv` level.
// In addition, it generates a repo. if it contains an extra non-common field.
func mergeCommonPrivatePlugins(plugins map[string][]string, privateReposWithValues map[string][]string, repeatedValues sets.String) {
	privateOrgPlugins := sets.NewString()

	for repoName, repoValues := range privateReposWithValues {
		repoLevelPlugins := sets.NewString()
		valuesSet := sets.NewString(repoValues...)

		if len(repeatedValues) > 0 {
			privateOrgPlugins = privateOrgPlugins.Union(valuesSet.Intersection(repeatedValues))
			repoLevelPlugins = repoLevelPlugins.Union(valuesSet.Difference(repeatedValues))

			// We want to keep the `approve` value in the repo level
			// because it interacts with the tide.queries.
			if valuesSet.Has(approvePluginValue) {
				repoLevelPlugins.Insert(approvePluginValue)
			}
		} else {
			repoLevelPlugins.Insert(repoValues...)
		}

		if len(repoLevelPlugins.List()) > 0 {
			logrus.WithFields(logrus.Fields{"repo": repoName, "value": repoLevelPlugins.List()}).Info("Generating repo")
			plugins[repoName] = repoLevelPlugins.List()
		}
	}

	if len(privateOrgPlugins.List()) > 0 {
		privateOrgPlugins.Delete(approvePluginValue)
		logrus.WithField("value", privateOrgPlugins.List()).Info("Generating openshift-priv org.")
		plugins[openshiftPrivOrg] = privateOrgPlugins.List()
	}
}

func getRepeatedValues(values []string) sets.String {
	temp := sets.NewString()
	repeated := sets.NewString()

	for _, value := range values {
		if temp.Has(value) {
			repeated.Insert(value)
		} else {
			temp.Insert(value)
		}
	}

	return repeated
}

func getAllConfigs(releaseRepoPath string, logger *logrus.Entry) (*config.ReleaseRepoConfig, error) {
	c := &config.ReleaseRepoConfig{}
	var err error
	ciopConfigPath := filepath.Join(releaseRepoPath, config.CiopConfigInRepoPath)
	c.CiOperator, err = config.LoadConfigByFilename(ciopConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load ci-operator configuration from release repo: %v", err)
	}

	prowConfigPath := filepath.Join(releaseRepoPath, config.ConfigInRepoPath)
	prowJobConfigPath := filepath.Join(releaseRepoPath, config.JobConfigInRepoPath)
	c.Prow, err = prowconfig.Load(prowConfigPath, prowJobConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load Prow configuration from release repo: %v", err)
	}

	return c, nil
}

func main() {
	logrus.SetReportCaller(true)
	logrus.SetFormatter(&logrus.TextFormatter{
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			return fmt.Sprintf("%s()", f.Function), fmt.Sprintf("%s:%d", path.Base(f.File), f.Line)
		},
	})

	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid option")
	}

	go func() {
		interrupts.WaitForGracefulShutdown()
		os.Exit(1)
	}()

	configs, err := getAllConfigs(o.releaseRepoPath, logrus.NewEntry(logrus.New()))
	if err != nil {
		logrus.Fatal("couldn't get the prow and ci-operator configs")
	}
	prowConfig := configs.Prow.ProwConfig

	pluginsConfigFile := filepath.Join(o.releaseRepoPath, config.PluginConfigInRepoPath)
	pluginsConfig, err := loadProwPlugins(pluginsConfigFile)
	if err != nil {
		logrus.WithError(err).Fatal("could not load Prow plugin configuration")
	}

	logrus.Info("Getting a summary of the orgs/repos that promote official images")
	orgRepos, err := getOrgReposWithOfficialImages(o.releaseRepoPath)
	if err != nil {
		logrus.WithError(err).Fatal("couldn't get the list of org/repos that promote official images")
	}

	injectPrivateBranchProtection(prowConfig.BranchProtection, orgRepos)
	injectPrivateTideOrgContextPolicy(prowConfig.Tide.ContextOptions, orgRepos)
	injectPrivateMergeType(prowConfig.Tide.MergeType, orgRepos)
	injectPrivateReposTideQueries(prowConfig.Tide.Queries, orgRepos)
	injectPrivatePRStatusBaseURLs(prowConfig.Tide.PRStatusBaseURLs, orgRepos)
	injectPrivatePlankDefaultDecorationConfigs(prowConfig.Plank.DefaultDecorationConfigs, orgRepos)
	injectPrivateJobURLPrefixConfig(prowConfig.Plank.JobURLPrefixConfig, orgRepos)
	injectPrivateApprovePlugin(pluginsConfig.Approve, orgRepos)
	injectPrivateLGTMPlugin(pluginsConfig.Lgtm, orgRepos)
	injectPrivatePlugins(pluginsConfig.Plugins, orgRepos)
	injectPrivateBugzillaPlugin(pluginsConfig.Bugzilla, orgRepos)

	if err := updateProwConfig(filepath.Join(o.releaseRepoPath, config.ConfigInRepoPath), prowConfig); err != nil {
		logrus.WithError(err).Fatal("couldn't update prow config file")
	}

	if err := updateProwPlugins(pluginsConfigFile, pluginsConfig); err != nil {
		logrus.WithError(err).Fatal("couldn't update prow plugins file")
	}
}
