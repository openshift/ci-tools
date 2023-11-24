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
	"github.com/openshift/ci-tools/pkg/results"
)

// PendingBuildError fetches scheduling errors from the build pod's events
func PendingBuildError(ctx context.Context, client kubernetes.PodClient, build *buildapi.Build) (ret error) {
	msg := fmt.Sprintf("build didn't start running within %s (phase: %s)", client.GetPendingTimeout(), build.Status.Phase)
	var pod corev1.Pod
	if name, ok := build.Annotations[buildapi.BuildPodNameAnnotation]; !ok {
		logrus.Warnf("pod annotation missing for build %q", build.Name)
		ret = errors.New(msg)
	} else if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: build.Namespace, Name: name}, &pod); err != nil {
		logrus.Warnf("failed to get pod for build %q: %v", build.Name, err)
		ret = errors.New(msg)
	} else {
		ret = fmt.Errorf("%s:%s\n%s", msg, getReasonsForUnreadyContainers(&pod), getEventsForPod(ctx, &pod, client))
	}
	ret = results.ForReason(api.ReasonPending).ForError(ret)
	return
}
