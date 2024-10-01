package onboard

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
)

func NewOnboard(ctx context.Context, log *logrus.Entry, opts *runtime.Options) (*cobra.Command, error) {
	cmd := cobra.Command{
		Use:   "onboard",
		Short: "Onboard a cluster",
		Long:  "Handle the onboarding procedure",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	configCmd, err := newConfigCmd(ctx, log, opts)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	cmd.AddCommand(configCmd)
	return &cmd, nil
}

func newConfigCmd(ctx context.Context, log *logrus.Entry, opts *runtime.Options) (*cobra.Command, error) {
	cmd := cobra.Command{
		Use:   "config",
		Short: "Handle configurations for a cluster",
		Long:  "Generate and apply configurations for a cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	generateConfigCmd, err := newGenerateConfigCmd(ctx, log, opts)
	if err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}
	cmd.AddCommand(generateConfigCmd)

	updateConfigCmd, err := newUpdateConfigCmd(ctx, log)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	cmd.AddCommand(updateConfigCmd)

	cmd.AddCommand(newApplyConfigCmd(ctx, log, opts))
	return &cmd, nil
}

func newApplyConfigCmd(ctx context.Context, log *logrus.Entry, opts *runtime.Options) *cobra.Command {
	cmd := cobra.Command{
		Use:   "apply",
		Short: "Apply the configuration files on a cluster",
		Long: `Apply the configuration files on a cluster.
This stage assumes that the configurations have been already generated.
It then runs the applyconfig tool to apply them.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			step := onboard.NewApplyConfigStep(log,
				runtime.ClusterInstallGetterFunc(opts.ClusterInstall),
				runtime.BuildCmd, runtime.RunCmd)
			return step.Run(ctx)
		},
	}
	return &cmd
}
