// determinize-prow-config reads and writes Prow configuration
// to enforce formatting on the files
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"k8s.io/apimachinery/pkg/util/sets"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/plugins"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/prowconfigsharding"
)

type options struct {
	prowConfigDir              string
	shardedProwConfigBaseDir   string
	shardedPluginConfigBaseDir string
}

func (o *options) Validate() error {
	if o.prowConfigDir == "" {
		return errors.New("--prow-config-dir is required")
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.prowConfigDir, "prow-config-dir", "", "Path to the Prow configuration directory.")
	fs.StringVar(&o.shardedProwConfigBaseDir, "sharded-prow-config-base-dir", "", "Basedir for the sharded prow config. If set, org and repo-specific config will get removed from the main prow config and written out in an org/repo tree below the base dir.")
	fs.StringVar(&o.shardedPluginConfigBaseDir, "sharded-plugin-config-base-dir", "", "Basedir for the sharded plugin config. If set, the plugin config will get sharded")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	if err := updateProwConfig(o.prowConfigDir, o.shardedProwConfigBaseDir); err != nil {
		logrus.WithError(err).Fatal("could not update Prow configuration")
	}

	if err := updatePluginConfig(o.prowConfigDir, o.shardedPluginConfigBaseDir); err != nil {
		logrus.WithError(err).Fatal("could not update Prow plugin configuration")
	}
}

func updateProwConfig(configDir, shardingBaseDir string) error {
	configPath := path.Join(configDir, config.ProwConfigFile)
	agent := prowconfig.Agent{}
	var additionalConfigs []string
	if shardingBaseDir != "" {
		additionalConfigs = append(additionalConfigs, shardingBaseDir)
	}
	if err := agent.Start(configPath, "", additionalConfigs, "_prowconfig.yaml"); err != nil {
		return fmt.Errorf("could not load Prow configuration: %w", err)
	}

	config := agent.Config()

	if shardingBaseDir != "" {
		pc, err := shardProwConfig(&config.ProwConfig, afero.NewBasePathFs(afero.NewOsFs(), shardingBaseDir))
		if err != nil {
			return fmt.Errorf("failed to shard the prow config: %w", err)
		}
		config.ProwConfig = *pc
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("could not marshal Prow configuration: %w", err)
	}

	return ioutil.WriteFile(configPath, data, 0644)
}

func updatePluginConfig(configDir, shardingBaseDir string) error {
	configPath := path.Join(configDir, config.PluginConfigFile)
	agent := plugins.ConfigAgent{}
	if err := agent.Load(configPath, []string{filepath.Dir(configPath)}, "_pluginconfig.yaml", false); err != nil {
		return fmt.Errorf("could not load Prow plugin configuration: %w", err)
	}
	cfg := agent.Config()
	if shardingBaseDir != "" {
		pc, err := prowconfigsharding.WriteShardedPluginConfig(cfg, afero.NewBasePathFs(afero.NewOsFs(), shardingBaseDir))
		if err != nil {
			return fmt.Errorf("failed to shard plugin config: %w", err)
		}
		cfg = pc
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("could not marshal Prow plugin configuration: %w", err)
	}

	return ioutil.WriteFile(configPath, data, 0644)
}

// prowConfigWithPointers mimics the upstream prowConfig but has pointer fields only
// in order to avoid serializing empty structs.
type prowConfigWithPointers struct {
	BranchProtection *prowconfig.BranchProtection `json:"branch-protection,omitempty"`
	Tide             *tideConfig                  `json:"tide,omitempty"`
}

type tideConfig struct {
	MergeType map[string]github.PullRequestMergeType `json:"merge_method,omitempty"`
	Queries   prowconfig.TideQueries                 `json:"queries,omitempty"`
}

func shardProwConfig(pc *prowconfig.ProwConfig, target afero.Fs) (*prowconfig.ProwConfig, error) {
	configsByOrgRepo := map[prowconfig.OrgRepo]*prowConfigWithPointers{}
	for org, orgConfig := range pc.BranchProtection.Orgs {
		for repo, repoConfig := range orgConfig.Repos {
			if configsByOrgRepo[prowconfig.OrgRepo{Org: org, Repo: repo}] == nil {
				configsByOrgRepo[prowconfig.OrgRepo{Org: org, Repo: repo}] = &prowConfigWithPointers{}
			}
			configsByOrgRepo[prowconfig.OrgRepo{Org: org, Repo: repo}].BranchProtection = &prowconfig.BranchProtection{
				Orgs: map[string]prowconfig.Org{org: {Repos: map[string]prowconfig.Repo{repo: repoConfig}}},
			}
			delete(pc.BranchProtection.Orgs[org].Repos, repo)
		}

		if isPolicySet(orgConfig.Policy) {
			if configsByOrgRepo[prowconfig.OrgRepo{Org: org}] == nil {
				configsByOrgRepo[prowconfig.OrgRepo{Org: org}] = &prowConfigWithPointers{}
			}
			configsByOrgRepo[prowconfig.OrgRepo{Org: org}].BranchProtection = &prowconfig.BranchProtection{
				Orgs: map[string]prowconfig.Org{org: orgConfig},
			}
		}
		delete(pc.BranchProtection.Orgs, org)
	}

	for orgOrgRepoString, mergeMethod := range pc.Tide.MergeType {
		var orgRepo prowconfig.OrgRepo
		if idx := strings.Index(orgOrgRepoString, "/"); idx != -1 {
			orgRepo.Org = orgOrgRepoString[:idx]
			orgRepo.Repo = orgOrgRepoString[idx+1:]
		} else {
			orgRepo.Org = orgOrgRepoString
		}

		if configsByOrgRepo[orgRepo] == nil {
			configsByOrgRepo[orgRepo] = &prowConfigWithPointers{}
		}
		configsByOrgRepo[orgRepo].Tide = &tideConfig{MergeType: map[string]github.PullRequestMergeType{orgOrgRepoString: mergeMethod}}
		delete(pc.Tide.MergeType, orgOrgRepoString)
	}

	ocpFeatureFreezeRepos := sets.NewString()
	ocpFeatureFreezeExemptRepos := sets.NewString("openshift/cluster-logging-operator",
		"openshift/console",
		"openshift/console-operator",
		"openshift/elasticsearch-operator",
		"openshift/elasticsearch-proxy",
		"openshift/origin-aggregated-logging",
		"openshift/builder",
		"openshift/cluster-samples-operator",
	)

	for _, query := range pc.Tide.Queries {
		for _, org := range query.Orgs {
			if configsByOrgRepo[prowconfig.OrgRepo{Org: org}] == nil {
				configsByOrgRepo[prowconfig.OrgRepo{Org: org}] = &prowConfigWithPointers{}
			}
			if configsByOrgRepo[prowconfig.OrgRepo{Org: org}].Tide == nil {
				configsByOrgRepo[prowconfig.OrgRepo{Org: org}].Tide = &tideConfig{}
			}
			queryCopy, err := deepCopyTideQuery(&query)
			if err != nil {
				return nil, fmt.Errorf("failed to deepcopy tide query %+v: %w", query, err)
			}
			queryCopy.Orgs = []string{org}
			queryCopy.Repos = nil
			configsByOrgRepo[prowconfig.OrgRepo{Org: org}].Tide.Queries = append(configsByOrgRepo[prowconfig.OrgRepo{Org: org}].Tide.Queries, *queryCopy)
		}
		for _, repo := range query.Repos {
			slashSplit := strings.Split(repo, "/")
			if len(slashSplit) != 2 {
				return nil, fmt.Errorf("repo '%s' in query %+v is not a valid repo specification", repo, query)
			}
			orgRepo := prowconfig.OrgRepo{Org: slashSplit[0], Repo: slashSplit[1]}
			if configsByOrgRepo[orgRepo] == nil {
				configsByOrgRepo[orgRepo] = &prowConfigWithPointers{}
			}
			if configsByOrgRepo[orgRepo].Tide == nil {
				configsByOrgRepo[orgRepo].Tide = &tideConfig{}
			}
			queryCopy, err := deepCopyTideQuery(&query)
			if err != nil {
				return nil, fmt.Errorf("failed to deepcopy tide query %+v: %w", query, err)
			}
			queryCopy.Orgs = nil
			queryCopy.Repos = []string{repo}
			configsByOrgRepo[orgRepo].Tide.Queries = append(configsByOrgRepo[orgRepo].Tide.Queries, *queryCopy)
			sort.Strings(queryCopy.IncludedBranches)
			sort.Strings(queryCopy.ExcludedBranches)

			if isOcpFeatureFreezeRepo(queryCopy) && !ocpFeatureFreezeExemptRepos.Has(repo) {
				ocpFeatureFreezeRepos.Insert(repo)
			}
		}
	}
	pc.Tide.Queries = nil

	for repo := range ocpFeatureFreezeExemptRepos {
		slashSplit := strings.Split(repo, "/")
		orgRepo := prowconfig.OrgRepo{Org: slashSplit[0], Repo: slashSplit[1]}
		ensureOcpFeatureFreezeExemptCriteria(&(configsByOrgRepo[orgRepo].Tide.Queries), repo)
	}
	for repo := range ocpFeatureFreezeRepos {
		slashSplit := strings.Split(repo, "/")
		orgRepo := prowconfig.OrgRepo{Org: slashSplit[0], Repo: slashSplit[1]}
		ensureOcpFeatureFreezeCriteria(&(configsByOrgRepo[orgRepo].Tide.Queries), repo)
	}

	for orgOrRepo, cfg := range configsByOrgRepo {
		if err := prowconfigsharding.MkdirAndWrite(target, filepath.Join(orgOrRepo.Org, orgOrRepo.Repo, config.SupplementalProwConfigFileName), cfg); err != nil {
			return nil, err
		}
	}

	return pc, nil
}

func ensureOcpFeatureFreezeCriteria(queries *prowconfig.TideQueries, repo string) {
	for i := range *queries {
		included := sets.NewString((*queries)[i].IncludedBranches...)
		if !(included.Equal(sets.NewString("master")) ||
			included.Equal(sets.NewString("main")) ||
			included.Equal(sets.NewString("master", "main"))) {
			continue
		}
		labels := sets.NewString((*queries)[i].Labels...)
		if labels.Has("bugzilla/valid-bug") {
			return
		}
		(*queries)[i].Labels = append((*queries)[i].Labels, "bugzilla/valid-bug")
		sort.Strings((*queries)[i].Labels)
		return
	}
	logrus.Warnf("Could not ensure OCP Feature Freeze merge criteria on %s", repo)
}

func ensureOcpFeatureFreezeExemptCriteria(queries *prowconfig.TideQueries, repo string) {
	var mainQuery *prowconfig.TideQuery
	var hasBugQuery, hasApprovalQuery bool
	approvalLabels := sets.NewString("px-approved", "docs-approved", "qe-approved")
	for i := range *queries {
		included := sets.NewString((*queries)[i].IncludedBranches...)
		if !(included.Equal(sets.NewString("master")) ||
			included.Equal(sets.NewString("main")) ||
			included.Equal(sets.NewString("master", "main"))) {
			continue
		}
		mainQuery, _ = deepCopyTideQuery(&(*queries)[i])

		labels := sets.NewString((*queries)[i].Labels...)

		if labels.Has("bugzilla/valid-bug") {
			hasBugQuery = true
			continue
		}
		if labels.IsSuperset(approvalLabels) {
			hasApprovalQuery = true
			continue
		}
		// It's neither, so we make it the bug query
		(*queries)[i].Labels = append((*queries)[i].Labels, "bugzilla/valid-bug")
		hasBugQuery = true
		sort.Strings((*queries)[i].Labels)
	}

	if mainQuery == nil {
		logrus.Warnf("Could not ensure OCP Feature Freeze-exempt merge criteria on %s", repo)
		return
	}
	mainQuery.Labels = sets.NewString(mainQuery.Labels...).Difference(approvalLabels.Union(sets.NewString("bugzilla/valid-bug"))).List()
	if !hasBugQuery {
		mainQuery.Labels = append(mainQuery.Labels, "bugzilla/valid-bug")
		sort.Strings(mainQuery.Labels)
		*queries = append(*queries, *mainQuery)
	}
	if !hasApprovalQuery {
		mainQuery.Labels = append(mainQuery.Labels, approvalLabels.List()...)
		sort.Strings(mainQuery.Labels)
		*queries = append(*queries, *mainQuery)
	}
}

func isOcpFeatureFreezeRepo(query *prowconfig.TideQuery) bool {
	if !(reflect.DeepEqual(query.IncludedBranches, []string{"release-4.8"}) ||
		reflect.DeepEqual(query.IncludedBranches, []string{"openshift-4.8"}) ||
		reflect.DeepEqual(query.IncludedBranches, []string{"openshift-4.8", "release-4.8"})) {
		return false
	}

	for _, label := range query.Labels {
		if label == "group-lead-approved" {
			return true
		}
	}

	return false
}

func deepCopyTideQuery(q *prowconfig.TideQuery) (*prowconfig.TideQuery, error) {
	serialized, err := json.Marshal(q)
	if err != nil {
		return nil, fmt.Errorf("failed to marhsal: %w", err)
	}
	var result prowconfig.TideQuery
	if err := json.Unmarshal(serialized, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal: %w", err)
	}

	return &result, nil
}

func isPolicySet(p prowconfig.Policy) bool {
	return !apiequality.Semantic.DeepEqual(p, prowconfig.Policy{})
}
