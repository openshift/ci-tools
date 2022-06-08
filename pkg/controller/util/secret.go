package util

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	crcontrollerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/openshift/ci-tools/pkg/api"
)

// EnsureImagePullSecret copy secret PullSecretName from ci namespace to another namespace
func EnsureImagePullSecret(ctx context.Context, namespace string, client ctrlruntimeclient.Client, log *logrus.Entry) error {
	*log = *log.WithField("subcomponent", "ensure-image-pull-secret").WithField("namespace", namespace)
	if namespace == "ci" || namespace == "test-credentials" {
		log.Debug("ignore ensuring image pull secret because it is managed by ci-secret-bootstrapper")
		return nil
	}
	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: api.RegistryPullCredentialsSecretName, Namespace: "ci"}
	if err := client.Get(ctx, key, secret); err != nil {
		return fmt.Errorf("failed to get the source secret %s: %w", key.String(), err)
	}
	s, mutateFn := pullSecret(secret, namespace)
	return upsertObject(ctx, client, s, mutateFn, log)
}

func pullSecret(template *corev1.Secret, namespace string) (*corev1.Secret, crcontrollerutil.MutateFn) {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      template.Name,
		},
	}
	return s, func() error {
		s.ObjectMeta.Annotations = template.ObjectMeta.Annotations
		s.ObjectMeta.Labels = template.ObjectMeta.Labels
		s.Type = template.Type
		s.Data = template.Data
		return nil
	}
}

func upsertObject(ctx context.Context, c ctrlruntimeclient.Client, obj ctrlruntimeclient.Object, mutateFn crcontrollerutil.MutateFn, log *logrus.Entry) error {
	// Create log here in case the operation fails and the obj is nil
	log = log.WithFields(logrus.Fields{"namespace": obj.GetNamespace(), "name": obj.GetName(), "type": fmt.Sprintf("%T", obj)})
	result, err := crcontrollerutil.CreateOrUpdate(ctx, c, obj, mutateFn)
	log = log.WithField("operation", result)
	if err != nil && !apierrors.IsConflict(err) && !apierrors.IsAlreadyExists(err) {
		log.WithError(err).Error("Upsert failed")
	} else if result != crcontrollerutil.OperationResultNone {
		log.Info("Upsert succeeded")
	}
	return err
}
