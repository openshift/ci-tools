package provision

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
)

func NewProvision(log *logrus.Entry, opts *runtime.Options) (*cobra.Command, error) {
	cmd := cobra.Command{
		Use:   "provision",
		Short: "Commands to provision the infrastructure on a cloud provider",
	}
	cmd.AddCommand(newProvisionAWS(log, opts))
	cmd.AddCommand(newProvisionOCP(log, opts))
	return &cmd, nil
}
