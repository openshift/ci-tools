package onboard

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func NewOnboard(ctx context.Context, log *logrus.Entry) *cobra.Command {
	cmd := cobra.Command{
		Use:   "onboard",
		Short: "Onboard a cluster",
		Long:  "Handle the onboarding procedure by generating the required assets",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newConfigCmd(ctx, log))
	return &cmd
}
