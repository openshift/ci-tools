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
	"github.com/openshift/ci-tools/pkg/privateorg"
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

	flattenOrgs privateorg.ArrayFlags
}

type orgReposWithOfficialImages map[string]map[string]string

func (o orgReposWithOfficialImages) privateRepoName(org, repo string) (string, bool) {
	reposByOrg, ok := o[org]
	if !ok {
		return "", false
	}
	privateRepo, ok := reposByOrg[repo]
	if !ok {
		return "", false
	}
	return privateRepo, true
}

func (o *orgReposWithOfficialImages) privateOrgRepoFull(orgRepo string) (string, bool) {
	if orgRepoList := strings.Split(orgRepo, "/"); len(orgRepoList) == 2 {
		if privateRepo, ok := o.privateRepoName(orgRepoList[0], orgRepoList[1]); ok {
			return fmt.Sprintf("%s/%s", openshiftPrivOrg, privateRepo), true
		}
	}
	return "", false
}

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.releaseRepoPath, "release-repo-path", "", "Path to a openshift/release repository directory")

	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")
	fs.Var(&o.flattenOrgs, "flatten-org", "Organizations whose repos should not have org prefix (can be specified multiple times)")
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

func updateProwConfig(configFile string, config *prowconfig.Config) error {
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

func getOrgReposWithOfficialImages(configDir string, whitelist map[string][]string, reposInOpenShiftPrivOrg sets.Set[string], flattenedOrgs sets.Set[string]) (orgReposWithOfficialImages, error) {
	ret := make(orgReposWithOfficialImages)

	for org, repos := range whitelist {
		for _, repo := range repos {
			if _, ok := ret[org]; !ok {
				ret[org] = map[string]string{}
			}
			privateRepo := privateorg.MirroredRepoName(org, repo, flattenedOrgs)
			if reposInOpenShiftPrivOrg.Has(privateRepo) {
				ret[org][repo] = privateRepo
			} else {
				logrus.WithField("source_repo", fmt.Sprintf("%s/%s", org, repo)).WithField("private_repo", privateRepo).Info("the repo does not exist in the openshift-priv org")
			}
		}
	}

	callback := func(c *api.ReleaseBuildConfiguration, i *config.Info) error {

		if !api.BuildsAnyOfficialImages(c, api.WithoutOKD) {
			return nil
		}

		if _, ok := ret[i.Org]; !ok {
			ret[i.Org] = map[string]string{}
		}
		privateRepo := privateorg.MirroredRepoName(i.Org, i.Repo, flattenedOrgs)
		if reposInOpenShiftPrivOrg.Has(privateRepo) {
			ret[i.Org][i.Repo] = privateRepo
		} else {
			logrus.WithField("source_repo", fmt.Sprintf("%s/%s", i.Org, i.Repo)).WithField("private_repo", privateRepo).Info("the repo does not exist in the openshift-priv org")
		}

		return nil
	}

	if err := config.OperateOnCIOperatorConfigDir(configDir, callback); err != nil {
		return ret, fmt.Errorf("error while operating in ci-operator configuration files: %w", err)
	}

	return ret, nil
}

func injectPrivateBranchProtection(branchProtection prowconfig.BranchProtection, orgRepos orgReposWithOfficialImages) {
	delete(branchProtection.Orgs, openshiftPrivOrg)

	privateOrg := prowconfig.Org{
		Repos: make(map[string]prowconfig.Repo),
	}

	logrus.Info("Processing...")
	for orgName, orgValue := range branchProtection.Orgs {
		for repoName, repoValue := range orgValue.Repos {
			if privateRepoName, ok := orgRepos.privateRepoName(orgName, repoName); ok {
				logrus.WithField("source_repo", fmt.Sprintf("%s/%s", orgName, repoName)).WithField("private_repo", privateRepoName).Info("Found")
				privateOrg.Repos[privateRepoName] = repoValue
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
			if privateRepoName, ok := orgRepos.privateRepoName(orgName, repoName); ok {
				logrus.WithField("source_repo", fmt.Sprintf("%s/%s", orgName, repoName)).WithField("private_repo", privateRepoName).Info("Found")
				privateOrgRepos[privateRepoName] = repoValue
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
		repos := sets.New(tideQuery.Repos...)

		for _, orgRepo := range tideQuery.Repos {
			if privateOrgRepoName, ok := orgRepos.privateOrgRepoFull(orgRepo); ok {
				logrus.WithField("source_repo", orgRepo).WithField("private_repo", privateOrgRepoName).Info("Found")
				repos.Insert(privateOrgRepoName)
			} else if strings.HasPrefix(orgRepo, openshiftPrivOrg) {
				repos.Delete(orgRepo)
			}
		}

		tideQueries[index].Repos = sets.List(repos)
	}
}

func injectPrivateMergeType(tideMergeTypes map[string]prowconfig.TideOrgMergeType, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	sourceMergeTypes := make(map[string]prowconfig.TideOrgMergeType)
	for orgRepoBranch, orgMergeType := range tideMergeTypes {
		org, repo, _ := prowconfigutils.ExtractOrgRepoBranch(orgRepoBranch)
		if org == openshiftPrivOrg {
			// Drop existing openshift-priv repo-level entries to avoid retaining stale
			// legacy names (for example openshift-priv/testRepo3).
			if repo != "" {
				delete(tideMergeTypes, orgRepoBranch)
				continue
			}
			// Keep org-level settings while rebuilding mirrored repo entries.
			orgMergeType.Repos = map[string]prowconfig.TideRepoMergeType{}
			tideMergeTypes[orgRepoBranch] = orgMergeType
			continue
		}
		sourceMergeTypes[orgRepoBranch] = orgMergeType
	}

	// FIXME(danilo-gemoli): iteration isn't deterministic as tideMergeTypes is a map
	for orgRepoBranch, orgMergeType := range sourceMergeTypes {
		org, repo, branch := prowconfigutils.ExtractOrgRepoBranch(orgRepoBranch)
		switch {
		// org/repo@branch shorthand
		case org != "" && repo != "" && branch != "":
			if privateRepoName, ok := orgRepos.privateRepoName(org, repo); ok {
				logrus.WithField("source_repo", fmt.Sprintf("%s/%s", org, repo)).WithField("private_repo", privateRepoName).Info("Found")
				tideMergeTypes[fmt.Sprintf("%s/%s@%s", openshiftPrivOrg, privateRepoName, branch)] = orgMergeType
			}
		// org/repo shorthand
		case org != "" && repo != "":
			if privateRepoName, ok := orgRepos.privateRepoName(org, repo); ok {
				logrus.WithField("source_repo", fmt.Sprintf("%s/%s", org, repo)).WithField("private_repo", privateRepoName).Info("Found")
				tideMergeTypes[fmt.Sprintf("%s/%s", openshiftPrivOrg, privateRepoName)] = orgMergeType
			}
		// org config
		default:
			// FIXME(danilo-gemoli): iteration isn't deterministic as orgMergeType.Repos is a map
			for repo, repoMergeType := range orgMergeType.Repos {
				if privateRepoName, ok := orgRepos.privateRepoName(org, repo); ok {
					logrus.WithField("source_repo", fmt.Sprintf("%s/%s", org, repo)).WithField("private_repo", privateRepoName).Info("Found")
					if _, ok := tideMergeTypes[openshiftPrivOrg]; !ok {
						tideMergeTypes[openshiftPrivOrg] = prowconfig.TideOrgMergeType{
							Repos: make(map[string]prowconfig.TideRepoMergeType),
						}
					}
					privateOrgConfig := tideMergeTypes[openshiftPrivOrg]
					if privateOrgConfig.Repos == nil {
						privateOrgConfig.Repos = map[string]prowconfig.TideRepoMergeType{}
					}
					if _, ok := privateOrgConfig.Repos[privateRepoName]; !ok {
						privateOrgConfig.Repos[privateRepoName] = repoMergeType
					}
					tideMergeTypes[openshiftPrivOrg] = privateOrgConfig

				}
			}
		}
	}
}

func injectPrivatePRStatusBaseURLs(prStatusBaseURLs map[string]string, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	sourcePRStatusBaseURLs := make(map[string]string)
	for orgRepo, value := range prStatusBaseURLs {
		if strings.HasPrefix(orgRepo, fmt.Sprintf("%s/", openshiftPrivOrg)) {
			delete(prStatusBaseURLs, orgRepo)
			continue
		}
		sourcePRStatusBaseURLs[orgRepo] = value
	}

	for orgRepo, value := range sourcePRStatusBaseURLs {
		if privateOrgRepoName, ok := orgRepos.privateOrgRepoFull(orgRepo); ok {
			logrus.WithField("source_repo", orgRepo).WithField("private_repo", privateOrgRepoName).Info("Found")
			prStatusBaseURLs[privateOrgRepoName] = value
		}
	}
}

func injectPrivatePlankDefaultDecorationConfigs(defaultDecorationConfigs map[string]*prowapi.DecorationConfig, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	sourceDefaultDecorationConfigs := make(map[string]*prowapi.DecorationConfig)
	for orgRepo, value := range defaultDecorationConfigs {
		if strings.HasPrefix(orgRepo, fmt.Sprintf("%s/", openshiftPrivOrg)) {
			delete(defaultDecorationConfigs, orgRepo)
			continue
		}
		sourceDefaultDecorationConfigs[orgRepo] = value
	}

	for orgRepo, value := range sourceDefaultDecorationConfigs {
		if privateOrgRepoName, ok := orgRepos.privateOrgRepoFull(orgRepo); ok {
			logrus.WithField("source_repo", orgRepo).WithField("private_repo", privateOrgRepoName).Info("Found")
			defaultDecorationConfigs[privateOrgRepoName] = value
		}
	}
}

func injectPrivateJobURLPrefixConfig(jobURLPrefixConfig map[string]string, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	sourceJobURLPrefixConfig := make(map[string]string)
	for orgRepo, value := range jobURLPrefixConfig {
		if strings.HasPrefix(orgRepo, fmt.Sprintf("%s/", openshiftPrivOrg)) {
			delete(jobURLPrefixConfig, orgRepo)
			continue
		}
		sourceJobURLPrefixConfig[orgRepo] = value
	}

	for orgRepo, value := range sourceJobURLPrefixConfig {
		if privateOrgRepoName, ok := orgRepos.privateOrgRepoFull(orgRepo); ok {
			logrus.WithField("source_repo", orgRepo).WithField("private_repo", privateOrgRepoName).Info("Found")
			jobURLPrefixConfig[privateOrgRepoName] = value
		}
	}
}

func injectPrivateApprovePlugin(approves []plugins.Approve, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	for index, approve := range approves {
		repos := sets.New[string]()
		for _, orgRepo := range approve.Repos {
			if strings.HasPrefix(orgRepo, fmt.Sprintf("%s/", openshiftPrivOrg)) {
				continue
			}
			repos.Insert(orgRepo)
		}

		for orgRepo := range repos {
			if privateOrgRepoName, ok := orgRepos.privateOrgRepoFull(orgRepo); ok {
				logrus.WithField("source_repo", orgRepo).WithField("private_repo", privateOrgRepoName).Info("Found")
				repos.Insert(privateOrgRepoName)
			}
		}

		approves[index].Repos = sets.List(repos)
	}
}

func injectPrivateLGTMPlugin(lgtms []plugins.Lgtm, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	for index, lgtm := range lgtms {
		repos := sets.New[string]()
		for _, orgRepo := range lgtm.Repos {
			if strings.HasPrefix(orgRepo, fmt.Sprintf("%s/", openshiftPrivOrg)) {
				continue
			}
			repos.Insert(orgRepo)
		}

		for orgRepo := range repos {
			if privateOrgRepoName, ok := orgRepos.privateOrgRepoFull(orgRepo); ok {
				logrus.WithField("source_repo", orgRepo).WithField("private_repo", privateOrgRepoName).Info("Found")
				repos.Insert(privateOrgRepoName)
			}
		}

		lgtms[index].Repos = sets.List(repos)
	}
}

func injectPrivateTriggers(triggers []plugins.Trigger, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	for index, trigger := range triggers {
		repos := sets.New[string]()
		for _, orgRepo := range trigger.Repos {
			if strings.HasPrefix(orgRepo, fmt.Sprintf("%s/", openshiftPrivOrg)) {
				continue
			}
			repos.Insert(orgRepo)
		}

		for orgRepo := range repos {
			if privateOrgRepoName, ok := orgRepos.privateOrgRepoFull(orgRepo); ok {
				logrus.WithField("source_repo", orgRepo).WithField("private_repo", privateOrgRepoName).Info("Found")
				repos.Insert(privateOrgRepoName)
			}
		}

		triggers[index].Repos = sets.List(repos)
	}
}

func injectPrivateBugzillaPlugin(bugzillaPlugins plugins.Bugzilla, orgRepos orgReposWithOfficialImages) {
	logrus.Info("Processing...")

	privateRepos := make(map[string]plugins.BugzillaRepoOptions)
	for org, orgValue := range bugzillaPlugins.Orgs {
		for repo, value := range orgValue.Repos {
			if privateRepoName, ok := orgRepos.privateRepoName(org, repo); ok {
				logrus.WithField("source_repo", fmt.Sprintf("%s/%s", org, repo)).WithField("private_repo", privateRepoName).Info("Found")
				privateRepos[privateRepoName] = value
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
			privateOrgRepoName, ok := orgRepos.privateRepoName(org, repo)
			if !ok {
				continue
			}
			privateRepoPlugins[fmt.Sprintf("%s/%s", openshiftPrivOrg, privateOrgRepoName)] = sets.List(values)
		}
	}

	commonPlugins := getCommonPlugins(privateRepoPlugins)
	for repo, values := range privateRepoPlugins {
		repoLevelPlugins := sets.New(values...)

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
		valuesSet := sets.New(values...)

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
	prowConfig := configs.Prow
	prowConfig.PresubmitsStatic = nil
	prowConfig.PostsubmitsStatic = nil
	prowConfig.Periodics = nil

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
	flattenedOrgs := sets.New[string](privateorg.DefaultFlattenOrgs...)
	flattenedOrgs.Insert(o.flattenOrgs...)
	orgRepos, err := getOrgReposWithOfficialImages(o.ConfigDir, o.WhitelistOptions.WhitelistConfig.Whitelist, reposInOpenShiftPrivOrg, flattenedOrgs)
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
	injectPrivateTriggers(pluginsConfig.Triggers, orgRepos)

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

	var cleanedTriggers []plugins.Trigger
	for _, trigger := range config.Triggers {
		var repos []string
		for _, repo := range trigger.Repos {
			if !strings.HasPrefix(repo, fmt.Sprintf("%s/", openshiftPrivOrg)) {
				repos = append(repos, repo)
			}
		}
		if len(repos) > 0 {
			trigger.Repos = repos
			cleanedTriggers = append(cleanedTriggers, trigger)
		}
	}
	config.Triggers = cleanedTriggers

	return config
}
