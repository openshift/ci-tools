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
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/plugins"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/config"
)

type options struct {
	prowConfigDir            string
	shardedProwConfigBaseDir string
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

	if err := updatePluginConfig(o.prowConfigDir); err != nil {
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
	var err error
	config.Tide.Queries, err = deduplicateTideQueries(config.Tide.Queries)
	if err != nil {
		return fmt.Errorf("failed to deduplicate Tide queries: %w", err)
	}

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

type tideQueryConfig struct {
	Author                 string
	ExcludedBranches       []string
	IncludedBranches       []string
	Labels                 []string
	MissingLabels          []string
	Milestone              string
	ReviewApprovedRequired bool
}

type tideQueryTarget struct {
	Orgs          []string
	Repos         []string
	ExcludedRepos []string
}

// tideQueryMap is a map[tideQueryConfig]*tideQueryTarget. Because slices are not comparable, they
// or structs containing them are not allowed as map keys. We sidestep this by using a json serialization
// of the object as key instead. This is horribly inefficient, but will never be able to beat the
// inefficiency of our Python validation scripts.
type tideQueryMap map[string]*tideQueryTarget

func (tm tideQueryMap) queries() (prowconfig.TideQueries, error) {
	var result prowconfig.TideQueries
	for k, v := range tm {
		var queryConfig tideQueryConfig
		if err := json.Unmarshal([]byte(k), &queryConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal %q: %w", k, err)
		}
		result = append(result, prowconfig.TideQuery{
			Orgs:                   v.Orgs,
			Repos:                  v.Repos,
			ExcludedRepos:          v.ExcludedRepos,
			Author:                 queryConfig.Author,
			ExcludedBranches:       queryConfig.ExcludedBranches,
			IncludedBranches:       queryConfig.IncludedBranches,
			Labels:                 queryConfig.Labels,
			MissingLabels:          queryConfig.MissingLabels,
			Milestone:              queryConfig.Milestone,
			ReviewApprovedRequired: queryConfig.ReviewApprovedRequired,
		})

	}
	var errs []error
	sort.SliceStable(result, func(i, j int) bool {
		iSerialized, err := json.Marshal(result[i])
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to marshal %+v: %w", result[i], err))
		}
		jSerialized, err := json.Marshal(result[j])
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to marshal %+v: %w", result[j], err))
		}
		return string(iSerialized) < string(jSerialized)
	})

	return result, utilerrors.NewAggregate(errs)
}

func deduplicateTideQueries(queries prowconfig.TideQueries) (prowconfig.TideQueries, error) {
	m := tideQueryMap{}
	for _, query := range queries {
		key := tideQueryConfig{
			Author:                 query.Author,
			ExcludedBranches:       query.ExcludedBranches,
			IncludedBranches:       query.IncludedBranches,
			Labels:                 query.Labels,
			MissingLabels:          query.MissingLabels,
			Milestone:              query.Milestone,
			ReviewApprovedRequired: query.ReviewApprovedRequired,
		}
		keyRaw, err := json.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal %+v: %w", key, err)
		}
		val, ok := m[string(keyRaw)]
		if !ok {
			val = &tideQueryTarget{}
			m[string(keyRaw)] = val
		}
		val.Orgs = append(val.Orgs, query.Orgs...)
		val.Repos = append(val.Repos, query.Repos...)
		val.ExcludedRepos = append(val.ExcludedRepos, query.ExcludedRepos...)
	}

	return m.queries()
}

func updatePluginConfig(configDir string) error {
	configPath := path.Join(configDir, config.PluginConfigFile)
	agent := plugins.ConfigAgent{}
	if err := agent.Load(configPath, false); err != nil {
		return fmt.Errorf("could not load Prow plugin configuration: %w", err)
	}
	data, err := yaml.Marshal(agent.Config())
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

	for orgOrRepo, cfg := range configsByOrgRepo {
		if err := mkdirAndWrite(target, filepath.Join(orgOrRepo.Org, orgOrRepo.Repo, config.SupplementalProwConfigFileName), cfg); err != nil {
			return nil, err
		}
	}

	return pc, nil
}

func mkdirAndWrite(fs afero.Fs, path string, content interface{}) error {
	dir := filepath.Dir(path)
	if err := fs.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to make dir %s: %w", dir, err)
	}
	serialized, err := yaml.Marshal(content)
	if err != nil {
		return fmt.Errorf("failed to serialize: %w", err)
	}
	if err := afero.WriteFile(fs, path, serialized, 0644); err != nil {
		return fmt.Errorf("failed to write to %s: %w", path, err)
	}
	return nil
}

func isPolicySet(p prowconfig.Policy) bool {
	return !apiequality.Semantic.DeepEqual(p, prowconfig.Policy{})
}
