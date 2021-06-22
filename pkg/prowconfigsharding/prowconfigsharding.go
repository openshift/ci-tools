package prowconfigsharding

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/afero"

	"k8s.io/test-infra/prow/plugins"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/config"
)

type pluginsConfigWithPointers struct {
	Plugins  *plugins.Plugins   `json:"plugins,omitempty"`
	Bugzilla *plugins.Bugzilla  `json:"bugzilla,omitempty"`
	Approve  []*plugins.Approve `json:"approve,omitempty"`
}

// WriteShardedPluginConfig shards the plugin config and then writes it into
// the provided target.
func WriteShardedPluginConfig(pc *plugins.Configuration, target afero.Fs) (*plugins.Configuration, error) {
	fileList := make(map[string]pluginsConfigWithPointers)
	for orgOrRepo, cfg := range pc.Plugins {
		file := pluginsConfigWithPointers{
			Plugins: &plugins.Plugins{orgOrRepo: cfg},
		}
		fileList[filepath.Join(orgOrRepo, config.SupplementalPluginConfigFileName)] = file
		delete(pc.Plugins, orgOrRepo)
	}
	for org, orgConfig := range pc.Bugzilla.Orgs {
		if orgConfig.Default != nil {
			path := filepath.Join(org, config.SupplementalPluginConfigFileName)
			if _, ok := fileList[path]; !ok {
				fileList[path] = pluginsConfigWithPointers{}
			}
			newOrgConfig := plugins.Bugzilla{
				Orgs: map[string]plugins.BugzillaOrgOptions{
					org: {
						Default: orgConfig.Default,
					},
				},
			}
			updatedConfig := fileList[path]
			updatedConfig.Bugzilla = &newOrgConfig
			fileList[path] = updatedConfig
		}
		for repo, repoConfig := range orgConfig.Repos {
			path := filepath.Join(org, repo, config.SupplementalPluginConfigFileName)
			if _, ok := fileList[path]; !ok {
				fileList[path] = pluginsConfigWithPointers{}
			}
			newRepoConfig := plugins.Bugzilla{
				Orgs: map[string]plugins.BugzillaOrgOptions{
					org: {
						Repos: map[string]plugins.BugzillaRepoOptions{
							repo: repoConfig,
						},
					},
				},
			}
			updatedConfig := fileList[path]
			updatedConfig.Bugzilla = &newRepoConfig
			fileList[path] = updatedConfig
		}
		delete(pc.Bugzilla.Orgs, org)
	}

	for _, approve := range pc.Approve {
		for _, orgOrRepo := range approve.Repos {
			path := filepath.Join(orgOrRepo, config.SupplementalPluginConfigFileName)
			if _, ok := fileList[path]; !ok {
				fileList[path] = pluginsConfigWithPointers{}
			}
			updatedConfig := fileList[path]

			newApproveCfg := approve
			newApproveCfg.Repos = []string{orgOrRepo}

			updatedConfig.Approve = []*plugins.Approve{&newApproveCfg}
			fileList[path] = updatedConfig
		}
	}

	pc.Approve = nil
	pc.Bugzilla.Orgs = nil
	for path, file := range fileList {
		if err := MkdirAndWrite(target, path, file); err != nil {
			return nil, err
		}
	}
	return pc, nil
}

func MkdirAndWrite(fs afero.Fs, path string, content interface{}) error {
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
