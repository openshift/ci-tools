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
	Plugins plugins.Plugins `json:"plugins,omitempty"`
}

// WriteShardedPluginConfig shards the plugin config and then writes it into
// the provided target.
func WriteShardedPluginConfig(pc *plugins.Configuration, target afero.Fs) (*plugins.Configuration, error) {
	for orgOrRepo, cfg := range pc.Plugins {
		file := pluginsConfigWithPointers{
			Plugins: plugins.Plugins{orgOrRepo: cfg},
		}
		if err := MkdirAndWrite(target, filepath.Join(orgOrRepo, config.SupplementalPluginConfigFileName), file); err != nil {
			return nil, err
		}
		delete(pc.Plugins, orgOrRepo)
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
