package release

import (
	"fmt"
	"path/filepath"

	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
)

// joinPath is similar to `filepath.Join` but preserves absolute paths
func joinPath(x, y string) string {
	if len(y) != 0 && y[0] == '/' {
		return y
	}
	return filepath.Join(x, y)
}

// argsWithPrefixes joins each argument with the various base paths
func (o options) argsWithPrefixes(def, path string, args []string) []string {
	if path == "" {
		path = def
	}
	path = joinPath(o.rootPath, path)
	if len(args) == 0 {
		args = append(args, path)
	} else if path != "" {
		for i, x := range args {
			args[i] = filepath.Join(path, x)
		}
	}
	return args
}

func (o *options) loadRegistry() error {
	path := o.argsWithPrefixes(config.RegistryPath, o.registryPath, nil)[0]
	var err error
	o.refs, o.chains, o.workflows, _, _, _, _, err = load.Registry(path, load.RegistryFlag(0))
	if err != nil {
		return fmt.Errorf("failed to load registry: %w", err)
	}
	o.resolver = registry.NewResolver(o.refs, o.chains, o.workflows, nil)
	return nil
}

func printYAML(i any) error {
	b, err := yaml.Marshal(i)
	if err != nil {
		return fmt.Errorf("failed to generate YAML: %w", err)
	}
	fmt.Print(string(b))
	return nil
}
