package clusterinstall

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

func Load(path string) (*ClusterInstall, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}
	ci := &ClusterInstall{}
	if err := yaml.Unmarshal(b, &ci); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	applyDefaults(ci)
	return ci, nil
}

func LoadFromDir(dir string) (map[string]*ClusterInstall, error) {
	clusterInstalls := make(map[string]*ClusterInstall)
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("read dir %s: %w", path, err)
		}
		if d.IsDir() {
			return nil
		}
		ci, err := Load(path)
		if err != nil {
			return fmt.Errorf("load cluster-install %s: %w", path, err)
		}
		clusterInstalls[ci.ClusterName] = ci
		return nil
	}); err != nil {
		return nil, err
	}
	return clusterInstalls, nil
}

func applyDefaults(ci *ClusterInstall) {
	coalesce(&ci.Onboard.Hosted, false)
	coalesce(&ci.Onboard.Unmanaged, false)
	coalesce(&ci.Onboard.OSD, true)
	coalesce(&ci.Onboard.UseTokenFileInKubeconfig, true)
}

func coalesce[T any](x **T, def T) {
	if *x == nil {
		*x = &def
	}
}
