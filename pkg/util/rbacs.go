package util

import (
	"context"
	"fmt"
	"time"

	coreapi "k8s.io/api/core/v1"
	rbacapi "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/results"
)

// CreateRBACs creates the given service account, role, and role bindings. In addition, it waits until the service account will be updated with an image pull secret.
// Because the DockerCfgController controller needs some time to create the corresponding secrets we need to wait until this happens, otherwise, the pod that
// will use this service account will fail to start since there are no credentials to pull any images.
func CreateRBACs(ctx context.Context, sa *coreapi.ServiceAccount, role *rbacapi.Role, roleBindings []rbacapi.RoleBinding, client ctrlruntimeclient.Client, retryDuration, timeout time.Duration) error {
	skipPoll := false

	err := client.Create(ctx, sa)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		return results.ForReason("creating_service_account").WithError(err).Errorf("could not create service account '%s'", sa.Name)
	}

	if kerrors.IsAlreadyExists(err) {
		skipPoll = true
	}

	if err := client.Create(ctx, role); err != nil && !kerrors.IsAlreadyExists(err) {
		return results.ForReason("creating_roles").WithError(err).Errorf("could not create role '%s'", role.Name)
	}
	for _, roleBinding := range roleBindings {
		if err := client.Create(ctx, &roleBinding); err != nil && !kerrors.IsAlreadyExists(err) {
			return results.ForReason("binding_roles").WithError(err).Errorf("could not create role binding '%s'", roleBinding.Name)
		}
	}

	if skipPoll {
		return nil
	}

	if err := wait.PollImmediate(retryDuration, timeout, func() (bool, error) {
		actualSA := &coreapi.ServiceAccount{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{
			Namespace: sa.Namespace,
			Name:      sa.Name,
		}, actualSA); err != nil {
			return false, fmt.Errorf("couldn't get service account %s: %w", sa.Name, err)
		}

		if len(actualSA.ImagePullSecrets) == 0 {
			return false, nil
		}

		return true, nil
	},
	); err != nil {
		return results.ForReason("create_dockercfg_secrets").WithError(err).Errorf("timeout while waiting for dockercfg secret creation for service account '%s'", sa.Name)
	}

	return nil
}
