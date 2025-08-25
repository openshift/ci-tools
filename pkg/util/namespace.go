package util

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func WaitUntilNamespaceIsPrivileged(ctx context.Context, nsName string, client ctrlruntimeclient.Client, retryDuration, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, retryDuration, timeout, true, func(ctx context.Context) (done bool, err error) {
		testNS := corev1.Namespace{}
		if err := client.Get(ctx, types.NamespacedName{Name: nsName}, &testNS, &ctrlruntimeclient.GetOptions{}); err != nil {
			return false, fmt.Errorf("get namespace %s: %w", nsName, err)
		}

		if testNS.Annotations == nil {
			return false, nil
		}

		return testNS.Annotations["security.openshift.io/MinimallySufficientPodSecurityStandard"] == "privileged", nil
	})
}
