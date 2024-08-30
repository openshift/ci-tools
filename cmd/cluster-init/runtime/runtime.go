package runtime

import (
	"context"
	"os"
	"os/exec"

	"github.com/openshift/ci-tools/pkg/clustermgmt"
)

var (
	clusterInstallCache *clustermgmt.ClusterInstall
)

type Options struct {
	ClusterInstall string
}

// clusterInstallGetterFunc reads and caches a ClusterInstall instance. This function exists
// solely because there are several steps that would otherwise read from the filesystem and unmarshal
// a cluster-install.yaml over and over.
// This function is NOT thread safe.
func ClusterInstallGetterFunc(path string) clustermgmt.ClusterInstallGetter {
	return func() (*clustermgmt.ClusterInstall, error) {
		if clusterInstallCache != nil {
			return clusterInstallCache, nil
		}
		ci, err := clustermgmt.LoadClusterInstall(path)
		clusterInstallCache = ci
		return clusterInstallCache, err
	}
}

func BuildCmd(ctx context.Context, program string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func RunCmd(cmd *exec.Cmd) error {
	return cmd.Run()
}
