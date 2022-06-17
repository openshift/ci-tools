package util

import (
	"context"
	"fmt"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/kubernetes"
)

// PendingBuildError produces an error for a build pending timeout
func PendingBuildError(ctx context.Context, client kubernetes.PodClient, build *buildapi.Build) error {
	return fmt.Errorf("build didn't start running within %s (phase: %s)", client.GetPendingTimeout(), build.Status.Phase)
}
