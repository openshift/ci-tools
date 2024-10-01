package onboard

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	"github.com/openshift/ci-tools/pkg/clustermgmt/onboard"
)

func NewOnboard(ctx context.Context, log *logrus.Entry, opts *runtime.Options) *cobra.Command {
	cmd := cobra.Command{
		Use:   "onboard",
		Short: "Onboard a cluster",
		Long:  "Handle the onboarding procedure",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newConfigCmd(ctx, log, opts))
	return &cmd
}

func newConfigCmd(ctx context.Context, log *logrus.Entry, opts *runtime.Options) *cobra.Command {
	cmd := cobra.Command{
		Use:   "config",
		Short: "Handle configurations for a cluster",
		Long:  "Generate and apply configurations for a cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newGenerateConfigCmd(ctx, log, opts))
	cmd.AddCommand(newUpdateConfigCmd(ctx, log))
	cmd.AddCommand(newApplyConfigCmd(ctx, log, opts))
	return &cmd
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
