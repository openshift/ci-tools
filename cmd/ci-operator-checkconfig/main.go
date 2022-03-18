package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/defaults"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/steps/release"
	"github.com/openshift/ci-tools/pkg/util"
	"github.com/openshift/ci-tools/pkg/validation"
)

type tagSet map[api.ImageStreamTagReference][]*config.Info

type promotedTag struct {
	tag      api.ImageStreamTagReference
	repoInfo *config.Info
}

type options struct {
	config.Options
	maxConcurrency uint

	resolver registry.Resolver
}

func (o *options) parse() error {
	var registryDir string

	fs := flag.NewFlagSet("", flag.ExitOnError)

	fs.StringVar(&registryDir, "registry", "", "Path to the step registry directory")
	fs.UintVar(&o.maxConcurrency, "concurrency", uint(runtime.GOMAXPROCS(0)), "Maximum number of concurrent in-flight goroutines.")

	o.Options.Bind(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}

	if err := o.loadResolver(registryDir); err != nil {
		return fmt.Errorf("failed to load registry: %w", err)
	}
	if err := o.Options.Validate(); err != nil {
		return fmt.Errorf("failed to validate config options: %w", err)
	}
	if err := o.Options.Complete(); err != nil {
		return fmt.Errorf("failed to complete config options: %w", err)
	}
	return nil
}

func (o *options) validate() (ret []error) {
	type workItem struct {
		configuration *api.ReleaseBuildConfiguration
		repoInfo      *config.Info
	}
	inputCh := make(chan workItem)
	produce := func() error {
		defer close(inputCh)
		if err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
			inputCh <- workItem{configuration, repoInfo}
			return nil
		}); err != nil {
			return fmt.Errorf("error reading configuration files: %w", err)
		}
		return nil
	}
	outputCh := make(chan promotedTag)
	errCh := make(chan error)
	map_ := func() error {
		validator := validation.NewValidator()
		for item := range inputCh {
			if err := o.validateConfiguration(&validator, outputCh, item.configuration, item.repoInfo); err != nil {
				errCh <- fmt.Errorf("failed to validate configuration %s: %w", item.repoInfo.Filename, err)
			}
		}
		return nil
	}
	seen := tagSet{}
	reduce := func() error {
		for i := range outputCh {
			seen[i.tag] = append(seen[i.tag], i.repoInfo)
		}
		return nil
	}
	done := func() { close(outputCh) }
	if err := util.ProduceMapReduce(0, produce, map_, reduce, done, errCh); err != nil {
		ret = append(ret, err)
	}
	return append(ret, validateTags(seen)...)
}

func (o *options) loadResolver(path string) error {
	if path == "" {
		return nil
	}
	refs, chains, workflows, _, _, observers, err := load.Registry(path, load.RegistryFlag(0))
	if err != nil {
		return err
	}
	o.resolver = registry.NewResolver(refs, chains, workflows, observers)
	return nil
}

func (o *options) validateConfiguration(
	validator *validation.Validator,
	seenCh chan<- promotedTag,
	configuration *api.ReleaseBuildConfiguration,
	repoInfo *config.Info,
) error {
	if o.resolver != nil {
		if c, err := registry.ResolveConfig(o.resolver, *configuration); err != nil {
			return err
		} else if err := validator.IsValidResolvedConfiguration(&c); err != nil {
			return err
		}
	}
	graphConf := defaults.FromConfigStatic(configuration)
	if err := validation.IsValidGraphConfiguration(graphConf.Steps); err != nil {
		return err
	}
	for _, tag := range release.PromotedTags(configuration) {
		seenCh <- promotedTag{tag, repoInfo}
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
		logrus.WithError(err).Fatal("failed to parse arguments")
	}
	if errs := o.validate(); errs != nil {
		for _, err := range errs {
			logrus.WithError(err).Error()
		}
		logrus.Fatal("error validating configuration files")
	}
}
