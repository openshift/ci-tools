package provision

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
)

func NewProvision(ctx context.Context, log *logrus.Entry, opts *runtime.Options) (*cobra.Command, error) {
	cmd := cobra.Command{
		Use:   "provision",
		Short: "Commands to provision the infrastructure on a cloud provider",
	}
	cmd.AddCommand(newProvisionAWS(ctx, log, opts))
	cmd.AddCommand(newProvisionOCP(ctx, log, opts))
	return &cmd, nil
}
