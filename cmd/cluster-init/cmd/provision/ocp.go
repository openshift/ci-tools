package provision

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

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
- openshift-install create install-config
- openshift-install create manifests
- openshift-install create cluster`,
	}
	cmd.AddCommand(newOCPCreateInstallConfig(ctx, log))
	cmd.AddCommand(newOCPCreateManifests(ctx, log))
	cmd.AddCommand(newOCPCreateCluster(ctx, log))
	return &cmd
}

func newOCPCreateInstallConfig(ctx context.Context, log *logrus.Entry) *cobra.Command {
	cmd := cobra.Command{
		Use:   "create-install-config",
		Short: "Create the install-config.yaml for openshift-install",
		Long:  "Create the install-config.yaml for openshift-install",
		RunE: func(cmd *cobra.Command, args []string) error {
			step := ocp.NewCreateInstallConfigStep(log, clusterInstallGetterFunc(opts.clusterInstall), buildCmd, runCmd)
			if err := step.Run(ctx); err != nil {
				return fmt.Errorf("%s: %w", step.Name(), err)
			}
			return nil
		},
	}
	return &cmd
}

func newOCPCreateManifests(ctx context.Context, log *logrus.Entry) *cobra.Command {
	cmd := cobra.Command{
		Use:   "create-manifests",
		Short: "Create the install-config manifests",
		Long:  "Create the install-config manifests",
		RunE: func(cmd *cobra.Command, args []string) error {
			step := ocp.NewCreateManifestsStep(log, clusterInstallGetterFunc(opts.clusterInstall), buildCmd, runCmd)
			if err := step.Run(ctx); err != nil {
				return fmt.Errorf("%s: %w", step.Name(), err)
			}
			return nil
		},
	}
	return &cmd
}

func newOCPCreateCluster(ctx context.Context, log *logrus.Entry) *cobra.Command {
	cmd := cobra.Command{
		Use:   "create-cluster",
		Short: "Create an OCP Cluster",
		Long:  "Create an OCP Cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			step := ocp.NewCreateClusterStep(log, clusterInstallGetterFunc(opts.clusterInstall), buildCmd, runCmd)
			if err := step.Run(ctx); err != nil {
				return fmt.Errorf("%s: %w", step.Name(), err)
			}
			return nil
		},
	}
	return &cmd
}
