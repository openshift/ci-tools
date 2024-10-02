package provision

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	"github.com/openshift/ci-tools/pkg/clusterinit"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/provision/ocp"
)

func newProvisionOCP(log *logrus.Entry, opts *runtime.Options) *cobra.Command {
	cmd := cobra.Command{
		Use:   "ocp",
		Short: "Provision an OCP Cluster",
		Long: `Use the OCP installer to provision a cluster.
The procedure consists of three steps:
1. openshift-install create install-config
2. openshift-install create manifests
3. openshift-install create cluster`,
	}
	cmd.AddCommand(newOCPCreate(log, opts))
	return &cmd
}

func newOCPCreate(log *logrus.Entry, opts *runtime.Options) *cobra.Command {
	cmd := cobra.Command{
		Use:   "create [install-config|manifests|cluster]",
		Short: "Create OCP assets",
		Long: `Provision an OCP cluster by running these commands in sequence:
1. create install-config: create an install-config.yaml
2. create manifests: create the manifests from an install-config.yaml
3. create cluster: provision a cluster`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var step clusterinit.Step

			if len(args) == 0 {
				return cmd.Help()
			}

			clusterInstall, err := clusterinstall.Load(opts.ClusterInstall, clusterinstall.FinalizeOption(clusterinstall.FinalizeOptions{
				InstallBase: opts.InstallBase,
			}))
			if err != nil {
				return fmt.Errorf("load cluster-install: %w", err)
			}

			switch args[0] {
			case "install-config":
				step = ocp.NewCreateInstallConfigStep(log, clusterInstall, runtime.BuildCmd, runtime.RunCmd)
			case "manifests":
				step = ocp.NewCreateManifestsStep(log, clusterInstall, runtime.BuildCmd, runtime.RunCmd)
			case "cluster":
				step = ocp.NewCreateClusterStep(log, clusterInstall, runtime.BuildCmd, runtime.RunCmd)
			default:
				return fmt.Errorf("action %q is not supported", args[0])
			}

			if err := step.Run(cmd.Context()); err != nil {
				return fmt.Errorf("%s: %w", step.Name(), err)
			}

			return nil
		},
	}
	return &cmd
}
