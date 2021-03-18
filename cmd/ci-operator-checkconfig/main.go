package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/steps/release"
)

type tagSet map[api.ImageStreamTagReference][]*config.Info

func main() {
	var configDir, registryDir string
	flag.StringVar(&configDir, "config-dir", "", "The directory containing configuration files.")
	flag.StringVar(&registryDir, "registry", "", "Path to the step registry directory")
	flag.Parse()

	if configDir == "" {
		fmt.Fprintln(os.Stderr, "The --config-dir flag is required but was not provided")
		os.Exit(1)
	}
	resolver, err := loadResolver(registryDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load registry: %v\n", err)
		os.Exit(1)
	}
	seen := tagSet{}
	if err := config.OperateOnCIOperatorConfigDir(configDir, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
		// basic validation of the configuration is implicit in the iteration
		if resolver != nil {
			if _, err := registry.ResolveConfig(resolver, *configuration); err != nil {
				return err
			}
		}
		for _, tag := range release.PromotedTags(configuration) {
			seen[tag] = append(seen[tag], repoInfo)
		}
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error validating configuration files: %v\n", err)
		os.Exit(1)
	}
	if dupes := validateTags(seen); len(dupes) > 0 {
		fmt.Fprintln(os.Stderr, "non-unique image publication found: ")
		for _, dupe := range dupes {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", dupe)
		}
		os.Exit(1)
	}
}

func loadResolver(path string) (registry.Resolver, error) {
	if path == "" {
		return nil, nil
	}
	refs, chains, workflows, _, _, observers, err := load.Registry(path, false)
	if err != nil {
		return nil, err
	}
	return registry.NewResolver(refs, chains, workflows, observers), nil
}

func validateTags(seen tagSet) []error {
	var dupes []error
	for tag, infos := range seen {
		if len(infos) <= 1 {
			continue
		}
		formatted := []string{}
		for _, info := range infos {
			identifier := fmt.Sprintf("%s/%s@%s", info.Org, info.Repo, info.Branch)
			if info.Variant != "" {
				identifier = fmt.Sprintf("%s [%s]", identifier, info.Variant)
			}
			formatted = append(formatted, identifier)
		}
		dupes = append(dupes, fmt.Errorf("output tag %s is promoted from more than one place: %v", tag.ISTagName(), strings.Join(formatted, ", ")))
	}
	return dupes
}
