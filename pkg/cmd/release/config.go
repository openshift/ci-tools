package release

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/util"
)

type configOptions struct {
	*options
	list, resolve bool
}

func newConfigCommand(o *options) *cobra.Command {
	co := configOptions{options: o}
	ret := &cobra.Command{
		Use:   "config",
		Short: "ci-operator configuration commands",
		Long: `Loads configuration files and writes them to stdout, optionally performing
resolution.`,
		RunE: func(_ *cobra.Command, args []string) error {
			if util.PopCount(co.list, co.resolve) > 1 {
				return fmt.Errorf("`list` and `resolve` are mutually exclusive")
			}
			args = o.argsWithPrefixes(config.CiopConfigInRepoPath, o.ciOperatorConfigPath, args)
			if co.list {
				return cmdConfigList(configPathsFromArgs(co.options, args))
			}
			if co.resolve {
				if err := co.loadRegistry(); err != nil {
					return err
				}
			}
			return cmdConfigPrint(co.resolver, configPathsFromArgs(co.options, args))
		},
	}
	flags := ret.PersistentFlags()
	flags.BoolVarP(&co.list, "list", "l", false, "list file names instead of contents")
	flags.BoolVarP(&co.resolve, "resolve", "r", false, "resolve configurations before printing")
	return ret
}

func configPathsFromArgs(o *options, args []string) []string {
	if len(args) != 0 {
		return args
	}
	if o.ciOperatorConfigPath != "" {
		return []string{o.ciOperatorConfigPath}
	}
	return []string{filepath.Join(o.rootPath, config.CiopConfigInRepoPath)}
}

func cmdConfigList(paths []string) error {
	for _, p := range paths {
		if err := config.OperateOnCIOperatorConfigPaths(p, func(path string, _ *config.Info) error {
			fmt.Println(path)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to list configuration files: %w", err)
		}
	}
	return nil
}

func cmdConfigPrint(resolver registry.Resolver, paths []string) error {
	for _, p := range paths {
		if err := config.OperateOnCIOperatorConfigDir(p, func(
			conf *api.ReleaseBuildConfiguration,
			_ *config.Info,
		) error {
			if resolver != nil {
				return resolveAndPrint(resolver, conf)
			}
			fmt.Println("---")
			return printYAML(conf)
		}); err != nil {
			return fmt.Errorf("failed to process configuration files: %w", err)
		}
	}
	return nil
}

func resolveAndPrint(resolver registry.Resolver, conf *api.ReleaseBuildConfiguration) error {
	c, err := registry.ResolveConfig(resolver, *conf)
	if err != nil {
		return err
	}
	fmt.Println("---")
	return printYAML(&c)
}
