package provision

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/pkg/clustermgmt"
)

type options struct {
	clusterInstall string
}

var (
	opts                = options{}
	clusterInstallCache *clustermgmt.ClusterInstall
)

// clusterInstallGetterFunc reads and caches a ClusterInstall instance. This function exists
// solely because there are several steps that would otherwise read from the filesystem and unmarshal
// a cluster-install.yaml over and over.
// This function is NOT thread safe.
func clusterInstallGetterFunc(path string) clustermgmt.ClusterInstallGetter {
	return func() (*clustermgmt.ClusterInstall, error) {
		if clusterInstallCache != nil {
			return clusterInstallCache, nil
		}
		ci, err := clustermgmt.LoadClusterInstall(path)
		clusterInstallCache = ci
		return clusterInstallCache, err
	}
}

func NewProvision(ctx context.Context, log *logrus.Entry) (*cobra.Command, error) {
	cmd := cobra.Command{
		Use:   "provision",
		Short: "Commands to provision the infrastructure on a cloud provider",
	}
	cmd.PersistentFlags().StringVar(&opts.clusterInstall, "cluster-install", "", "Path to cluster-install.yaml")
	if err := cmd.MarkPersistentFlagRequired("cluster-install"); err != nil {
		return nil, err
	}
	cmd.AddCommand(newProvisionAWS(ctx, log))
	cmd.AddCommand(newProvisionOCP(ctx, log))
	return &cmd, nil
}
