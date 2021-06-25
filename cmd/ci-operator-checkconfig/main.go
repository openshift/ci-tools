package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/steps/release"
	"github.com/openshift/ci-tools/pkg/validation"
)

type tagSet map[api.ImageStreamTagReference][]*config.Info

type options struct {
	configDir      string
	maxConcurrency int

	resolver registry.Resolver
}

func (o *options) parse() error {
	var registryDir string
	flag.StringVar(&o.configDir, "config-dir", "", "The directory containing configuration files.")
	flag.StringVar(&registryDir, "registry", "", "Path to the step registry directory")
	flag.IntVar(&o.maxConcurrency, "concurrency", 0, "Maximum number of concurrent in-flight goroutines.")
	flag.Parse()
	if o.configDir == "" {
		return errors.New("The --config-dir flag is required but was not provided")
	}
	if o.maxConcurrency == 0 {
		o.maxConcurrency = runtime.GOMAXPROCS(0)
	}
	if err := o.loadResolver(registryDir); err != nil {
		return fmt.Errorf("failed to load registry: %w", err)
	}
	return nil
}

func (o *options) validate() (ret []error) {
	type workItem struct {
		configuration *api.ReleaseBuildConfiguration
		repoInfo      *config.Info
	}
	ch, errCh := make(chan workItem), make(chan error)
	seen := make([]tagSet, o.maxConcurrency)
	wg := sync.WaitGroup{}
	for i := 0; i < o.maxConcurrency; i++ {
		wg.Add(1)
		seenI := tagSet{}
		seen[i] = seenI
		go func() {
			defer wg.Done()
			for item := range ch {
				if err := o.validateConfiguration(seenI, item.configuration, item.repoInfo); err != nil {
					errCh <- err
				}
			}
		}()
	}
	go func() {
		for err := range errCh {
			ret = append(ret, err)
		}
	}()
	if err := config.OperateOnCIOperatorConfigDir(o.configDir, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
		ch <- workItem{configuration, repoInfo}
		return nil
	}); err != nil {
		ret = append(ret, fmt.Errorf("error reading configuration files: %w", err))
	}
	close(ch)
	wg.Wait()
	close(errCh)
	mergePromotedTags(seen)
	ret = append(ret, validateTags(seen[0])...)
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

func mergePromotedTags(s []tagSet) {
	dst := s[0]
	for _, src := range s[1:] {
		for k, v := range src {
			if l := dst[k]; l != nil {
				dst[k] = append(l, v...)
			} else {
				dst[k] = v
			}
		}
	}
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
