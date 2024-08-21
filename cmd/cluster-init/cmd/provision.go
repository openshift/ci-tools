package cmd

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type options struct {
	clusterInstall string
}

var (
	opts = options{}
)

func NewProvision(ctx context.Context, log *logrus.Entry) *cobra.Command {
	cmd := cobra.Command{
		Use:   "provision",
		Short: "Commands to provision the infrastructure on a cloud provider",
	}
	cmd.PersistentFlags().StringVar(&opts.clusterInstall, "cluster-install", "", "Path to cluster-install.yaml")
	cmd.MarkPersistentFlagRequired("cluster-install")
	cmd.AddCommand(newProvisionAWS(ctx, log))
	return &cmd
}

func newProvisionAWS(ctx context.Context, log *logrus.Entry) *cobra.Command {
	cmd := cobra.Command{
		Use:   "aws",
		Short: "Provision assets on AWS",
	}
	cmd.AddCommand(newAWSCreateStacks(ctx, log))
	return &cmd
}

func newAWSCreateStacks(ctx context.Context, log *logrus.Entry) *cobra.Command {
	cmd := cobra.Command{
		Use:   "create-stacks",
		Short: "Create cloud formation stacks",
		RunE: func(cmd *cobra.Command, args []string) error {
			step := newCreateAWSStacksStep(log, opts.clusterInstall)
			if err := step.Run(ctx); err != nil {
				return fmt.Errorf("%s: %v", step.Name(), err)
			}
			return nil
		},
	}
	return &cmd
}

type createAWSStacksStep struct {
	log            *logrus.Entry
	clusterInstall string
}

func (s *createAWSStacksStep) Run(ctx context.Context) error {
	s.log.Info("create AWS stacks")
	return nil
}

func (s *createAWSStacksStep) Name() string {
	return "create-aws-stacks"
}

func newCreateAWSStacksStep(log *logrus.Entry, clusterInstall string) *createAWSStacksStep {
	return &createAWSStacksStep{log: log, clusterInstall: clusterInstall}
}
