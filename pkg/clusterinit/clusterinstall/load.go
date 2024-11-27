package clusterinstall

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

type LoadOptions struct {
	installBase string
	releaseRepo string
}

type LoadOption func(*LoadOptions)

type FinalizeOptions struct {
	InstallBase string
	ReleaseRepo string
}

// FinalizeOption configures the fields that will be set on a cluster config upon reading it.
func FinalizeOption(fo FinalizeOptions) func(*LoadOptions) {
	return func(lo *LoadOptions) {
		lo.installBase = fo.InstallBase
		lo.releaseRepo = fo.ReleaseRepo
	}
}

// Load loads a cluster config from path in accordance with opts.
func Load(path string, opts ...LoadOption) (*ClusterInstall, error) {
	loadOptions := LoadOptions{}
	for _, opt := range opts {
		opt(&loadOptions)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}
	ci := &ClusterInstall{}
	if err := yaml.Unmarshal(b, &ci); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}

	ci.InstallBase = loadOptions.installBase
	ci.Onboard.ReleaseRepo = loadOptions.releaseRepo
	applyDefaults(ci, path)

	compareAndSet(&ci.InstallBase, loadOptions.installBase, "")
	compareAndSet(&ci.Onboard.ReleaseRepo, loadOptions.releaseRepo, "")

	return ci, nil
}

// LoadFromDir loads cluster configs from dir in accordance with opts.
func LoadFromDir(dir string, opts ...LoadOption) (map[string]*ClusterInstall, error) {
	clusterInstalls := make(map[string]*ClusterInstall)
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("read dir %s: %w", path, err)
		}
		if d.IsDir() {
			return nil
		}
		ci, err := Load(path, opts...)
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

func applyDefaults(ci *ClusterInstall, ciPath string) {
	coalesce(&ci.Onboard.Hosted, false)
	coalesce(&ci.Onboard.Unmanaged, false)
	coalesce(&ci.Onboard.OSD, true)
	coalesce(&ci.Onboard.UseTokenFileInKubeconfig, true)
	coalesce(&ci.Onboard.Multiarch, false)
	if ci.InstallBase == "" {
		ci.InstallBase = path.Dir(ciPath)
	}
}

func coalesce[T any](x **T, def T) {
	if *x == nil {
		*x = &def
	}
}

func compareAndSet[T comparable](x *T, val, target T) {
	if *x == target {
		*x = val
	}
}
