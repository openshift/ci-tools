package shardprowconfig

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/git/types"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/prowconfigsharding"
	"github.com/openshift/ci-tools/pkg/prowconfigutils"
)

type ShardProwConfigFunctors interface {
	ModifyQuery(*prowconfig.TideQuery, string)
	GetDataFromProwConfig(*prowconfig.ProwConfig)
}

// prowConfigWithPointers mimics the upstream prowConfig but has pointer fields only
// in order to avoid serializing empty structs.
type ProwConfigWithPointers struct {
	BranchProtection     *prowconfig.BranchProtection     `json:"branch-protection,omitempty"`
	Tide                 *TideConfig                      `json:"tide,omitempty"`
	SlackReporterConfigs *prowconfig.SlackReporterConfigs `json:"slack_reporter_configs,omitempty"`
}

type TideConfig struct {
	MergeType map[string]types.PullRequestMergeType `json:"merge_method,omitempty"`
	Queries   prowconfig.TideQueries                `json:"queries,omitempty"`
}

func ShardProwConfig(pc *prowconfig.ProwConfig, target afero.Fs, f ShardProwConfigFunctors) (*prowconfig.ProwConfig, error) {
	configsByOrgRepo := map[prowconfig.OrgRepo]*ProwConfigWithPointers{}
	for org, orgConfig := range pc.BranchProtection.Orgs {
		for repo, repoConfig := range orgConfig.Repos {
			if configsByOrgRepo[prowconfig.OrgRepo{Org: org, Repo: repo}] == nil {
				configsByOrgRepo[prowconfig.OrgRepo{Org: org, Repo: repo}] = &ProwConfigWithPointers{}
			}
			configsByOrgRepo[prowconfig.OrgRepo{Org: org, Repo: repo}].BranchProtection = &prowconfig.BranchProtection{
				Orgs: map[string]prowconfig.Org{org: {Repos: map[string]prowconfig.Repo{repo: repoConfig}}},
			}
			delete(pc.BranchProtection.Orgs[org].Repos, repo)
		}

		if isPolicySet(orgConfig.Policy) {
			if configsByOrgRepo[prowconfig.OrgRepo{Org: org}] == nil {
				configsByOrgRepo[prowconfig.OrgRepo{Org: org}] = &ProwConfigWithPointers{}
			}
			configsByOrgRepo[prowconfig.OrgRepo{Org: org}].BranchProtection = &prowconfig.BranchProtection{
				Orgs: map[string]prowconfig.Org{org: orgConfig},
			}
		}
		delete(pc.BranchProtection.Orgs, org)
	}

	setOrgRepoOnConfigs := func(orgRepo prowconfig.OrgRepo, mergeType types.PullRequestMergeType) {
		if configsByOrgRepo[orgRepo] == nil {
			configsByOrgRepo[orgRepo] = &ProwConfigWithPointers{}
		}
		orgRepoString := orgRepo.Org
		if orgRepo.Repo != "" {
			orgRepoString = fmt.Sprintf("%s/%s", orgRepo.Org, orgRepo.Repo)
		}
		configsByOrgRepo[orgRepo].Tide = &TideConfig{MergeType: map[string]types.PullRequestMergeType{orgRepoString: mergeType}}
	}

	for orgRepoBranch, orgMergeConfig := range pc.Tide.MergeType {
		org, repo, branch := prowconfigutils.ExtractOrgRepoBranch(orgRepoBranch)
		orgRepo := prowconfig.OrgRepo{Org: org, Repo: repo}
		switch {
		// org/repo
		case org != "" && repo != "" && branch == "":
			setOrgRepoOnConfigs(orgRepo, orgMergeConfig.MergeType)
		// org
		case org != "" && repo == "" && branch == "":
			if orgMergeConfig.MergeType != "" {
				setOrgRepoOnConfigs(orgRepo, orgMergeConfig.MergeType)
			} else {
				for r, repoMergeConfig := range orgMergeConfig.Repos {
					if r != prowconfigutils.TideRepoMergeTypeWildcard &&
						repoMergeConfig.MergeType != "" {
						orgRepo.Repo = r
						setOrgRepoOnConfigs(orgRepo, repoMergeConfig.MergeType)
					}
				}
			}
		}
		delete(pc.Tide.MergeType, orgRepoBranch)
	}

	f.GetDataFromProwConfig(pc)

	for _, query := range pc.Tide.Queries {
		for _, org := range query.Orgs {
			if configsByOrgRepo[prowconfig.OrgRepo{Org: org}] == nil {
				configsByOrgRepo[prowconfig.OrgRepo{Org: org}] = &ProwConfigWithPointers{}
			}
			if configsByOrgRepo[prowconfig.OrgRepo{Org: org}].Tide == nil {
				configsByOrgRepo[prowconfig.OrgRepo{Org: org}].Tide = &TideConfig{}
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
				configsByOrgRepo[orgRepo] = &ProwConfigWithPointers{}
			}
			if configsByOrgRepo[orgRepo].Tide == nil {
				configsByOrgRepo[orgRepo].Tide = &TideConfig{}
			}
			queryCopy, err := deepCopyTideQuery(&query)
			if err != nil {
				return nil, fmt.Errorf("failed to deepcopy tide query %+v: %w", query, err)
			}

			queryCopy.Orgs = nil
			queryCopy.Repos = []string{repo}

			f.ModifyQuery(queryCopy, repo)

			configsByOrgRepo[orgRepo].Tide.Queries = append(configsByOrgRepo[orgRepo].Tide.Queries, *queryCopy)
		}
	}
	pc.Tide.Queries = nil

	for orgOrRepo, slackReporter := range pc.SlackReporterConfigs {
		if orgOrRepo == "*" {
			// Value of "*" is for applying global configurations, so no need to shard it
			continue
		}
		orgRepo := prowconfig.NewOrgRepo(orgOrRepo)
		if configsByOrgRepo[*orgRepo] == nil {
			configsByOrgRepo[*orgRepo] = &ProwConfigWithPointers{}
		}

		configsByOrgRepo[*orgRepo].SlackReporterConfigs = &prowconfig.SlackReporterConfigs{
			orgOrRepo: slackReporter,
		}

		delete(pc.SlackReporterConfigs, orgOrRepo)
	}

	for orgOrRepo, cfg := range configsByOrgRepo {
		if err := prowconfigsharding.MkdirAndWrite(target, filepath.Join(orgOrRepo.Org, orgOrRepo.Repo, config.SupplementalProwConfigFileName), cfg); err != nil {
			return nil, err
		}
	}

	return pc, nil
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
