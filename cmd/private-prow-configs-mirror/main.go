package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"

	"k8s.io/apimachinery/pkg/util/sets"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/plugins"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/prowconfigsharding"
	"github.com/openshift/ci-tools/pkg/prowconfigutils"
)

const (
	openshiftPrivOrg = "openshift-priv"
)

type options struct {
	releaseRepoPath string

	config.WhitelistOptions
	config.Options

	github prowflagutil.GitHubOptions
	dryRun bool
}

type orgReposWithOfficialImages map[string]sets.Set[string]

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

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.releaseRepoPath, "release-repo-path", "", "Path to a openshift/release repository directory")

	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")
	o.github.AddFlags(fs)
	o.Options.Bind(fs)
	o.WhitelistOptions.Bind(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parrse flags: %w", err)
	}
	return o, nil
}

func (o *options) validate() error {
	if len(o.releaseRepoPath) == 0 {
		return errors.New("--release-repo-path is not defined")
	}

	o.ConfigDir = filepath.Join(o.releaseRepoPath, config.CiopConfigInRepoPath)
	if err := o.Options.Validate(); err != nil {
		return fmt.Errorf("failed to validate config options: %w", err)
	}
	if err := o.Options.Complete(); err != nil {
		return fmt.Errorf("failed to complete config options: %w", err)
	}
	if err := o.github.Validate(o.dryRun); err != nil {
		return err
	}
	return o.WhitelistOptions.Validate()
}

func loadProwPlugins(pluginsPath string) (*plugins.Configuration, error) {
	agent := plugins.ConfigAgent{}
	if err := agent.Load(pluginsPath, []string{filepath.Dir(config.PluginConfigFile)}, "_pluginconfig.yaml", false, true); err != nil {
		return nil, err
	}
	return agent.Config(), nil
}

func updateProwConfig(configFile string, config prowconfig.ProwConfig) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("could not marshal Prow configuration: %w", err)
	}

	return os.WriteFile(configFile, data, 0644)
}

func updateProwPlugins(pluginsFile string, config *plugins.Configuration) error {
	config, err := prowconfigsharding.WriteShardedPluginConfig(config, afero.NewBasePathFs(afero.NewOsFs(), filepath.Dir(pluginsFile)))
	if err != nil {
		return fmt.Errorf("failed to write plugin config shards: %w", err)
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("could not marshal Prow configuration: %w", err)
	}

	return os.WriteFile(pluginsFile, data, 0644)
}

func privateOrgRepo(repo string) string {
	return fmt.Sprintf("%s/%s", openshiftPrivOrg, repo)
}

func getOrgReposWithOfficialImages(configDir string, whitelist map[string][]string, reposInOpenShiftPrivOrg sets.Set[string]) (orgReposWithOfficialImages, error) {
	ret := make(orgReposWithOfficialImages)

	for org, repos := range whitelist {
		for _, repo := range repos {
			if _, ok := ret[org]; !ok {
				ret[org] = sets.New[string](repo)
			} else if reposInOpenShiftPrivOrg.Has(repo) {
				ret[org].Insert(repo)
			} else {
				logrus.WithField("repo", repo).Info("the repo does not exist in the openshift-priv org")
			}
		}
	}

	callback := func(c *api.ReleaseBuildConfiguration, i *config.Info) error {

		if !api.BuildsAnyOfficialImages(c, api.WithoutOKD) {
			return nil
		}

		if i.Org != "openshift" {
			logrus.WithField("org", i.Org).WithField("repo", i.Repo).Warn("Dropping repo in non-openshift org, this is currently not supported")
			return nil
		}

		if _, ok := ret[i.Org]; !ok {
			ret[i.Org] = sets.New[string](i.Repo)
		} else if reposInOpenShiftPrivOrg.Has(i.Repo) {
			ret[i.Org].Insert(i.Repo)
		} else {
			logrus.WithField("repo", i.Repo).Info("the repo does not exist in the openshift-priv org")
		}

		return nil
	}

	if err := config.OperateOnCIOperatorConfigDir(configDir, callback); err != nil {
		return ret, fmt.Errorf("error while operating in ci-operator configuration files: %w", err)
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

func setPrivateReposTideQueries(tideQueries []prowconfig.TideQuery, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	for index, tideQuery := range tideQueries {
		repos := sets.New[string](tideQuery.Repos...)

		for _, orgRepo := range tideQuery.Repos {
			if orgRepos.isOfficialRepoFull(orgRepo) {
				repo := strings.Split(orgRepo, "/")[1]

				logrus.WithField("repo", repo).Info("Found")
				repos.Insert(privateOrgRepo(repo))
			} else if strings.HasPrefix(orgRepo, openshiftPrivOrg) {
				repos.Delete(orgRepo)
			}
		}

		tideQueries[index].Repos = sets.List(repos)
	}
}

func injectPrivateMergeType(tideMergeTypes map[string]prowconfig.TideOrgMergeType, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	// FIXME(danilo-gemoli): iteration isn't deterministic as tideMergeTypes is a map
	for orgRepoBranch, orgMergeType := range tideMergeTypes {
		org, repo, branch := prowconfigutils.ExtractOrgRepoBranch(orgRepoBranch)
		switch {
		// org/repo@branch shorthand
		case org != "" && repo != "" && branch != "":
			if orgRepos.isOfficialRepoFull(org + "/" + repo) {
				logrus.WithField("repo", repo).Info("Found")
				tideMergeTypes[privateOrgRepo(repo)+"@"+branch] = orgMergeType
			}
		// org/repo shorthand
		case org != "" && repo != "":
			if orgRepos.isOfficialRepoFull(org + "/" + repo) {
				logrus.WithField("repo", repo).Info("Found")
				tideMergeTypes[privateOrgRepo(repo)] = orgMergeType
			}
		// org config
		default:
			// FIXME(danilo-gemoli): iteration isn't deterministic as orgMergeType.Repos is a map
			for repo, repoMergeType := range orgMergeType.Repos {
				if orgRepos.isOfficialRepoFull(org + "/" + repo) {
					logrus.WithField("repo", repo).Info("Found")
					if _, ok := tideMergeTypes[openshiftPrivOrg]; !ok {
						tideMergeTypes[openshiftPrivOrg] = prowconfig.TideOrgMergeType{}
					}
					privateOrgConfig := tideMergeTypes[openshiftPrivOrg]
					if _, ok := privateOrgConfig.Repos[repo]; !ok {
						privateOrgConfig.Repos[repo] = repoMergeType
					}

				}
			}
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
		repos := sets.New[string](approve.Repos...)

		for _, orgRepo := range approve.Repos {
			if orgRepos.isOfficialRepoFull(orgRepo) {
				repo := strings.Split(orgRepo, "/")[1]

				logrus.WithField("repo", repo).Info("Found")
				repos.Insert(privateOrgRepo(repo))
			}
		}

		approves[index].Repos = sets.List(repos)
	}
}

func injectPrivateLGTMPlugin(lgtms []plugins.Lgtm, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	for index, lgtm := range lgtms {
		repos := sets.New[string](lgtm.Repos...)

		for _, orgRepo := range lgtm.Repos {
			if orgRepos.isOfficialRepoFull(orgRepo) {
				repo := strings.Split(orgRepo, "/")[1]

				logrus.WithField("repo", repo).Info("Found")
				repos.Insert(privateOrgRepo(repo))
			}
		}

		lgtms[index].Repos = sets.List(repos)
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

func injectPrivatePlugins(prowPlugins plugins.Plugins, orgRepos orgReposWithOfficialImages) {
	privateRepoPlugins := make(map[string][]string)
	for org, repos := range orgRepos {

		for repo := range repos {
			values := sets.New[string]()
			values.Insert(prowPlugins[org].Plugins...)

			if repoValues, ok := prowPlugins[fmt.Sprintf("%s/%s", org, repo)]; ok {
				values.Insert(repoValues.Plugins...)
			}
			privateRepoPlugins[privateOrgRepo(repo)] = sets.List(values)
		}
	}

	commonPlugins := getCommonPlugins(privateRepoPlugins)
	for repo, values := range privateRepoPlugins {
		repoLevelPlugins := sets.New[string](values...)

		repoLevelPlugins = repoLevelPlugins.Difference(commonPlugins)

		if len(sets.List(repoLevelPlugins)) > 0 {
			logrus.WithFields(logrus.Fields{"repo": repo, "value": sets.List(repoLevelPlugins)}).Info("Generating repo")
			prowPlugins[repo] = plugins.OrgPlugins{Plugins: sets.List(repoLevelPlugins)}
		}
	}

	if len(sets.List(commonPlugins)) > 0 {
		logrus.WithField("value", sets.List(commonPlugins)).Info("Generating openshift-priv org.")
		prowPlugins[openshiftPrivOrg] = plugins.OrgPlugins{Plugins: sets.List(commonPlugins)}
	}
}

func getCommonPlugins(privateRepoPlugins map[string][]string) sets.Set[string] {
	var ret sets.Set[string]
	for _, values := range privateRepoPlugins {
		valuesSet := sets.New[string](values...)

		if ret == nil {
			ret = valuesSet
			continue
		}

		ret = ret.Intersection(valuesSet)
	}
	return ret
}

func getAllConfigs(releaseRepoPath string) (*config.ReleaseRepoConfig, error) {
	c := &config.ReleaseRepoConfig{}
	var err error
	ciopConfigPath := filepath.Join(releaseRepoPath, config.CiopConfigInRepoPath)
	c.CiOperator, err = config.LoadDataByFilename(ciopConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load ci-operator configuration from release repo: %w", err)
	}

	prowConfigPath := filepath.Join(releaseRepoPath, config.ConfigInRepoPath)
	prowJobConfigPath := filepath.Join(releaseRepoPath, config.JobConfigInRepoPath)
	c.Prow, err = prowconfig.Load(prowConfigPath, prowJobConfigPath, nil, "")
	if err != nil {
		return nil, fmt.Errorf("failed to load Prow configuration from release repo: %w", err)
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

	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to gather options")
	}
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid option")
	}

	go func() {
		interrupts.WaitForGracefulShutdown()
		os.Exit(1)
	}()

	configs, err := getAllConfigs(o.releaseRepoPath)
	if err != nil {
		logrus.WithError(err).Fatal("couldn't get the prow and ci-operator configs")
	}
	prowConfig := configs.Prow.ProwConfig

	pluginsConfigFile := filepath.Join(o.releaseRepoPath, config.PluginConfigInRepoPath)
	pluginsConfig, err := loadProwPlugins(pluginsConfigFile)
	if err != nil {
		logrus.WithError(err).Fatal("could not load Prow plugin configuration")
	}

	ghClient, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error creating github client.")
	}
	repos, err := ghClient.GetRepos("openshift-priv", false)
	if err != nil {
		logrus.WithError(err).Fatal("couldn't get openshift-priv repos")
	}
	reposInOpenShiftPrivOrg := sets.New[string]()
	for _, repo := range repos {
		reposInOpenShiftPrivOrg.Insert(repo.Name)
	}

	logrus.Info("Getting a summary of the orgs/repos that promote official images")
	orgRepos, err := getOrgReposWithOfficialImages(o.ConfigDir, o.WhitelistOptions.WhitelistConfig.Whitelist, reposInOpenShiftPrivOrg)
	if err != nil {
		logrus.WithError(err).Fatal("couldn't get the list of org/repos that promote official images")
	}
	// Reset this so pluginconfigs from removed repos get removed
	pluginsConfig = cleanStalePluginConfigs(pluginsConfig)

	injectPrivateBranchProtection(prowConfig.BranchProtection, orgRepos)
	injectPrivateTideOrgContextPolicy(prowConfig.Tide.ContextOptions, orgRepos)
	injectPrivateMergeType(prowConfig.Tide.MergeType, orgRepos)
	setPrivateReposTideQueries(prowConfig.Tide.Queries, orgRepos)
	injectPrivatePRStatusBaseURLs(prowConfig.Tide.PRStatusBaseURLs, orgRepos)
	injectPrivatePlankDefaultDecorationConfigs(prowConfig.Plank.DefaultDecorationConfigsMap, orgRepos)
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

func cleanStalePluginConfigs(config *plugins.Configuration) *plugins.Configuration {
	cleanedPlugins := make(map[string]plugins.OrgPlugins)
	for orgOrRepo, val := range config.Plugins {
		if strings.HasPrefix(orgOrRepo, openshiftPrivOrg) {
			continue
		}
		cleanedPlugins[orgOrRepo] = val
	}
	config.Plugins = cleanedPlugins

	return config
}
