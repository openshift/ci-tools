package util

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
)

// PendingBuildError fetches scheduling errors from the build pod's events
func PendingBuildError(ctx context.Context, client kubernetes.PodClient, build *buildapi.Build) error {
	msg := fmt.Sprintf("build didn't start running within %s (phase: %s)", api.PodStartTimeout, build.Status.Phase)
	var pod corev1.Pod
	if podName, ok := build.Annotations[buildapi.BuildPodNameAnnotation]; !ok {
		logrus.Debug("build pod annotation missing")
		return errors.New(msg)
	} else if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: build.Namespace, Name: podName}, &pod); err != nil {
		logrus.Warnf("failed to get build pod: %v", err)
		return errors.New(msg)
	}
	return fmt.Errorf("%s: %s\n%s", msg, getReasonsForUnreadyContainers(&pod), getEventsForPod(ctx, &pod, client))
}
