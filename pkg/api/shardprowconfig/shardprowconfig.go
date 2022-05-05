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
)

type ShardProwConfigFunctors interface {
	ModifyQuery(*prowconfig.TideQuery, string)
	GetDataFromProwConfig(*prowconfig.ProwConfig)
}

// prowConfigWithPointers mimics the upstream prowConfig but has pointer fields only
// in order to avoid serializing empty structs.
type prowConfigWithPointers struct {
	BranchProtection *prowconfig.BranchProtection `json:"branch-protection,omitempty"`
	Tide             *tideConfig                  `json:"tide,omitempty"`
}

type tideConfig struct {
	MergeType map[string]types.PullRequestMergeType `json:"merge_method,omitempty"`
	Queries   prowconfig.TideQueries                `json:"queries,omitempty"`
}

func ShardProwConfig(pc *prowconfig.ProwConfig, target afero.Fs, f ShardProwConfigFunctors) (*prowconfig.ProwConfig, error) {
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
		configsByOrgRepo[orgRepo].Tide = &tideConfig{MergeType: map[string]types.PullRequestMergeType{orgOrgRepoString: mergeMethod}}
		delete(pc.Tide.MergeType, orgOrgRepoString)
	}

	f.GetDataFromProwConfig(pc)

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

			f.ModifyQuery(queryCopy, repo)

			configsByOrgRepo[orgRepo].Tide.Queries = append(configsByOrgRepo[orgRepo].Tide.Queries, *queryCopy)
		}
	}
	pc.Tide.Queries = nil

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
