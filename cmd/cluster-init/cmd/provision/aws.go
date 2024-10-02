package provision

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/provision/aws"
)

func newProvisionAWS(log *logrus.Entry, opts *runtime.Options) *cobra.Command {
	cmd := cobra.Command{
		Use:   "aws",
		Short: "Provision assets on AWS",
		Long: `Provision the required infrastructure on AWS.
An AWS profile must be properly set for these subcommands to work properly.
How to use a named profile: https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html#cli-configure-files-using-profiles
For more information regarding env. variables: https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-envvars.html#envvars-set`,
	}
	cmd.AddCommand(newAWSCreateStacks(log, opts))
	return &cmd
}

func newAWSCreateStacks(log *logrus.Entry, opts *runtime.Options) *cobra.Command {
	cmd := cobra.Command{
		Use:   "create-stacks",
		Short: "Create cloud formation stacks",
		Long:  `Create cloud formation stacks `,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			clusterInstall, err := clusterinstall.Load(opts.ClusterInstall)
			if err != nil {
				return fmt.Errorf("load cluster-install: %w", err)
			}
			step := aws.NewCreateAWSStacksStep(log,
				clusterInstall,
				func() (aws.CloudFormationClient, error) {
					log.Info("Loading AWS config")
					awsconfig, err := config.LoadDefaultConfig(ctx)
					if err != nil {
						return nil, fmt.Errorf("load aws config: %w", err)
					}
					return cloudformation.NewFromConfig(awsconfig), nil
				}, nil, nil,
			)
			if err := step.Run(ctx); err != nil {
				return fmt.Errorf("%s: %w", step.Name(), err)
			}
			return nil
		},
	}
	return &cmd
}
