package release

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/registry"
)

type options struct {
	// Input.
	rootPath             string
	ciOperatorConfigPath string
	jobConfigPath        string
	registryPath         string
	// Dynamic, optional state.
	refs      registry.ReferenceByName
	chains    registry.ChainByName
	workflows registry.WorkflowByName
	resolver  registry.Resolver
}

func NewCommand() *cobra.Command {
	var o options
	ret := cobra.Command{
		Use: "release",
		Long: `release is a command-line program that can be used to interact with the
openshift/release repository.

Subcommands exist that parse ci-operator configuration files and step registry
definitions in the same manner as the other CI components.  These can be used
directly or as the base for higher-level programs and scripts.`,
	}
	flags := ret.PersistentFlags()
	flags.StringVarP(&o.rootPath, "root-dir", "C", ".", "path to the root directory")
	flags.StringVar(&o.ciOperatorConfigPath, "config-dir", "", fmt.Sprintf(`path to the ci-operator configuration directory (default: %q under the root directory)`, config.CiopConfigInRepoPath))
	flags.StringVar(&o.jobConfigPath, "job-config", "", fmt.Sprintf(`path to the Prow job configuration directory (default: %q under the root directory)`, config.JobConfigInRepoPath))
	flags.StringVar(&o.registryPath, "registry", "", fmt.Sprintf(`path to the registry directory (default: %q under the root directory)`, config.RegistryPath))
	ret.AddCommand(newConfigCommand(&o))
	ret.AddCommand(newJobCommand(&o))
	ret.AddCommand(newRegistryCommand(&o))
	ret.AddCommand(newProfileCommand())
	return &ret
}
