package onboard

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/config"
	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
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
	configCmd, err := config.NewCmd(ctx, log, opts)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	cmd.AddCommand(configCmd)
	return &cmd, nil
}
