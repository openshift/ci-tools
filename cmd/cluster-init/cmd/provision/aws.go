package provision

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	awsruntime "github.com/openshift/ci-tools/cmd/cluster-init/runtime/aws"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/provision/aws"
	_ "github.com/openshift/cloud-credential-operator/pkg/apis"
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
			clusterInstall, err := clusterinstall.Load(opts.ClusterInstall, clusterinstall.FinalizeOption(clusterinstall.FinalizeOptions{
				InstallBase: opts.InstallBase,
			}))
			if err != nil {
				return fmt.Errorf("load cluster-install: %w", err)
			}
			awsProvider := awsruntime.NewProvider(clusterInstall, nil)
			step := aws.NewCreateAWSStacksStep(log, clusterInstall, awsProvider, nil, nil)
			if err := step.Run(ctx); err != nil {
				return fmt.Errorf("%s: %w", step.Name(), err)
			}
			return nil
		},
	}
	return &cmd
}
