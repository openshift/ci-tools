package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/steps/release"
	"github.com/openshift/ci-tools/pkg/validation"
)

type tagSet map[api.ImageStreamTagReference][]*config.Info

type options struct {
	configDir string

	resolver registry.Resolver
}

func (o *options) parse() error {
	var registryDir string
	flag.StringVar(&o.configDir, "config-dir", "", "The directory containing configuration files.")
	flag.StringVar(&registryDir, "registry", "", "Path to the step registry directory")
	flag.Parse()
	if o.configDir == "" {
		return errors.New("The --config-dir flag is required but was not provided")
	}
	if err := o.loadResolver(registryDir); err != nil {
		return fmt.Errorf("failed to load registry: %w", err)
	}
	return nil
}

func (o *options) validate() (ret []error) {
	seen := tagSet{}
	if err := config.OperateOnCIOperatorConfigDir(o.configDir, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
		// basic validation of the configuration is implicit in the iteration
		if err := o.validateConfiguration(seen, configuration, repoInfo); err != nil {
			ret = append(ret, fmt.Errorf("error validating configuration %s: %w", repoInfo.Filename, err))
		}
		return nil
	}); err != nil {
		ret = append(ret, fmt.Errorf("error reading configuration files: %w", err))
	}
	ret = append(ret, validateTags(seen)...)
	return
}

func (o *options) loadResolver(path string) error {
	if path == "" {
		return nil
	}
	refs, chains, workflows, _, _, observers, err := load.Registry(path, false)
	if err != nil {
		return err
	}
	o.resolver = registry.NewResolver(refs, chains, workflows, observers)
	return nil
}

func (o *options) validateConfiguration(seen tagSet, configuration *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
	if o.resolver != nil {
		if c, err := registry.ResolveConfig(o.resolver, *configuration); err != nil {
			return err
		} else if err := validation.IsValidResolvedConfiguration(&c); err != nil {
			return err
		}
	}
	for _, tag := range release.PromotedTags(configuration) {
		seen[tag] = append(seen[tag], repoInfo)
	}
	if configuration.PromotionConfiguration != nil && configuration.PromotionConfiguration.RegistryOverride != "" {
		return errors.New("setting promotion.registry_override is not allowed")
	}
	return nil
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

func main() {
	o := options{}
	if err := o.parse(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if errs := o.validate(); errs != nil {
		fmt.Fprintln(os.Stderr, "error validating configuration files:")
		for _, err := range errs {
			fmt.Fprintln(os.Stderr, err)
		}
	}
}
