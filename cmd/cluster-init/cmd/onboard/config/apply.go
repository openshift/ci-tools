package config

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
)

type applyConfigOptions struct {
	releaseRepo string
}

func newApplyCmd(log *logrus.Entry, parentOpts *runtime.Options) (*cobra.Command, error) {
	opts := applyConfigOptions{}
	cmd := cobra.Command{
		Use:   "apply",
		Short: "Apply the configuration files on a cluster",
		Long: `Apply the configuration files on a cluster.
This stage assumes that the configurations have been already generated.
It then runs the applyconfig tool to apply them.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterInstall, err := clusterinstall.Load(parentOpts.ClusterInstall, clusterinstall.FinalizeOption(clusterinstall.FinalizeOptions{
				ReleaseRepo: opts.releaseRepo,
			}))
			if err != nil {
				return fmt.Errorf("load cluster-install: %w", err)
			}
			step := onboard.NewApplyConfigStep(log, clusterInstall, runtime.BuildCmd, runtime.RunCmd)
			return step.Run(cmd.Context())
		},
	}
	pf := cmd.PersistentFlags()
	pf.StringVar(&opts.releaseRepo, "release-repo", "", "Path to openshift/release.")
	if err := cmd.MarkPersistentFlagRequired("release-repo"); err != nil {
		return nil, err
	}

	return &cmd, nil
}
