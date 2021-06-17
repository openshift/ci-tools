// determinize-prow-config reads and writes Prow configuration
// to enforce formatting on the files
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"

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
		if err := prowconfigsharding.MkdirAndWrite(target, filepath.Join(orgOrRepo.Org, orgOrRepo.Repo, config.SupplementalProwConfigFileName), cfg); err != nil {
			return nil, err
		}
	}

	return pc, nil
}

func isPolicySet(p prowconfig.Policy) bool {
	return !apiequality.Semantic.DeepEqual(p, prowconfig.Policy{})
}
