package util

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	rbacapi "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
)

// CreateRBACs creates the given service account, role, and role bindings. In addition, it waits until the service account will be updated with an image pull secret.
// Because the DockerCfgController controller needs some time to create the corresponding secrets we need to wait until this happens, otherwise, the pod that
// will use this service account will fail to start since there are no credentials to pull any images.
func CreateRBACs(ctx context.Context, sa *coreapi.ServiceAccount, role *rbacapi.Role, roleBindings []rbacapi.RoleBinding, client ctrlruntimeclient.Client, retryDuration, timeout time.Duration) error {
	skipPoll := false

	err := client.Create(ctx, sa)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		return results.ForReason("creating_service_account").WithError(err).Errorf("could not create service account %q: %v", sa.Name, err)
	}

	if kerrors.IsAlreadyExists(err) {
		skipPoll = true
	}

	if err := client.Create(ctx, role); err != nil && !kerrors.IsAlreadyExists(err) {
		return results.ForReason("creating_roles").WithError(err).Errorf("could not create role %q: %v", role.Name, err)
	}
	for _, roleBinding := range roleBindings {
		if err := client.Create(ctx, &roleBinding); err != nil && !kerrors.IsAlreadyExists(err) {
			return results.ForReason("binding_roles").WithError(err).Errorf("could not create role binding %q: %v", roleBinding.Name, err)
		}
	}

	if skipPoll {
		return nil
	}

	hasDockerCfgImagePullSecretSet := func(imagePullSecrets []coreapi.LocalObjectReference) bool {
		for _, secret := range imagePullSecrets {
			if api.RegistryPullCredentialsSecret != secret.Name {
				return true
			}
		}
		return false
	}

	if err := wait.PollImmediate(retryDuration, timeout, func() (bool, error) {
		actualSA := &coreapi.ServiceAccount{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{
			Namespace: sa.Namespace,
			Name:      sa.Name,
		}, actualSA); err != nil {
			return false, fmt.Errorf("couldn't get service account %s: %w", sa.Name, err)
		}

		if !hasDockerCfgImagePullSecretSet(actualSA.ImagePullSecrets) {
			return false, nil
		}

		return true, nil
	}); err != nil {
		_ = results.ForReason("create_dockercfg_secrets").WithError(err).Errorf("timeout while waiting for dockercfg secret creation for service account %q: %v", sa.Name, err)
		logrus.WithError(err).Debugf("timeout while waiting for dockercfg secret creation for service account %q", sa.Name)
	}
	return nil
}
