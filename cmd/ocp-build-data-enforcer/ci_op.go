package main

import (
	"fmt"
	"strings"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load/agents"
)

// We have org, repo, branch and Dockerfile path and need to get a pull spec in order to do substution of the
// member field.
// We do this by:
// * Indexing all pomoting ci operator configs by org/repo/branch/dockerfile
// * Fetching the matching config for the request
// * Iterate through the .images field, find the config by matchin on the dockerfile and use that tag
// * Returning a pull spec pointing to api for the imagestreamtag
func pullSpecForOrgRepoBranchDockerfileFactory(agent agents.ConfigAgent) pullSpecForOrgRepoBranchDockerfileGetter {
	return func(org, repo, branch, dockerfile string) (string, error) {
		cfgs, err := agent.GetFromIndex(
			orgRepoBranchDockerfileConfigIndexName,
			indexKeyForOrgRepoBranchDockerfile(org, repo, branch, dockerfile),
		)
		if err != nil {
			return "", fmt.Errorf("failed to get config from %s index: %w", orgRepoBranchDockerfileConfigIndexName, err)
		}
		if n := len(cfgs); n != 1 {
			return "", fmt.Errorf("expected to get exactly one config from %s index, got %d", orgRepoBranchDockerfileConfigIndexName, n)
		}

		// TODO: There is code for this somewhere
		cfg := cfgs[0]
		var imageBuildConfig *api.ProjectDirectoryImageBuildStepConfiguration
		for _, imageBuild := range cfg.Images {
			if imageBuild.DockerfilePath == dockerfile {
				imageBuildConfig = &imageBuild
				break
			}
		}

		if imageBuildConfig == nil {
			return "", fmt.Errorf("no imageBuildConfig found for Dockerfile %s", dockerfile)
		}

		return fmt.Sprintf("registry.svc.ci.openshift.org/%s/%s:%s", cfg.PromotionConfiguration.Namespace, cfg.PromotionConfiguration.Name, imageBuildConfig.To), nil
	}

}

type pullSpecForOrgRepoBranchDockerfileGetter func(org, repo, branch, dockerfile string) (string, error)

const orgRepoBranchDockerfileConfigIndexName = "config-by-org-repo-branch-dockerfile"

func indexKeyForOrgRepoBranchDockerfile(org, repo, branch, dockerfile string) string {
	return strings.Join([]string{org, repo, branch, dockerfile}, "|")
}

var _ agents.IndexFn = indexPromotingConfigsByOrgRepoBranchDockerfile

func indexPromotingConfigsByOrgRepoBranchDockerfile(cfg api.ReleaseBuildConfiguration) []string {
	if cfg.PromotionConfiguration == nil {
		return nil
	}

	var indexKeys []string
	for _, imageBuildConfig := range cfg.Images {
		dockerFilePath := "Dockerfile"
		if imageBuildConfig.DockerfilePath != "" {
			dockerFilePath = imageBuildConfig.DockerfilePath
		}

		indexKeys = append(indexKeys, indexKeyForOrgRepoBranchDockerfile(
			cfg.Metadata.Org,
			cfg.Metadata.Repo,
			cfg.Metadata.Branch,
			dockerFilePath,
		))
	}

	return indexKeys
}
