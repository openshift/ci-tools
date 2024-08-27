package provision

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/pkg/clustermgmt"
	"github.com/openshift/ci-tools/pkg/clustermgmt/provision/ocp"
)

func buildCmd(ctx context.Context, program string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func runCmd(cmd *exec.Cmd) error {
	return cmd.Run()
}

func newProvisionOCP(ctx context.Context, log *logrus.Entry) *cobra.Command {
	cmd := cobra.Command{
		Use:   "ocp",
		Short: "Provision an OCP Cluster",
		Long: `Use the OCP installer to provision a cluster.
The procedure consists of three steps:
1. openshift-install create install-config
2. openshift-install create manifests
3. openshift-install create cluster`,
	}
	cmd.AddCommand(newOCPCreate(ctx, log))
	return &cmd
}

func newOCPCreate(ctx context.Context, log *logrus.Entry) *cobra.Command {
	cmd := cobra.Command{
		Use:   "create [install-config|manifests|cluster]",
		Short: "Create OCP assets",
		Long: `Provision an OCP cluster by running these commands in sequence:
1. create install-config: create an install-config.yaml
2. create manifests: create the manifests from an install-config.yaml
3. create cluster: provision a cluster`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var step clustermgmt.Step

			if len(args) == 0 {
				return cmd.Help()
			}

			switch args[0] {
			case "install-config":
				step = ocp.NewCreateInstallConfigStep(log, clusterInstallGetterFunc(opts.clusterInstall), buildCmd, runCmd)
			case "manifests":
				step = ocp.NewCreateManifestsStep(log, clusterInstallGetterFunc(opts.clusterInstall), buildCmd, runCmd)
			case "cluster":
				step = ocp.NewCreateClusterStep(log, clusterInstallGetterFunc(opts.clusterInstall), buildCmd, runCmd)
			default:
				return fmt.Errorf("action %q is not supported", args[0])
			}

			if err := step.Run(ctx); err != nil {
				return fmt.Errorf("%s: %w", step.Name(), err)
			}

			return nil
		},
	}
	return &cmd
}
