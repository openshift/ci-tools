package config

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
)

func newApplyCmd(ctx context.Context, log *logrus.Entry, opts *runtime.Options) *cobra.Command {
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
