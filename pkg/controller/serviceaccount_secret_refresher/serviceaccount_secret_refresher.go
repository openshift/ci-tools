package serviceaccountsecretrefresher

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	ControllerName = "serviceaccount_secret_refresher"

	TTLAnnotationKey = "serviaccount-secret-rotator.openshift.io/delete-after"
)

func AddToManager(clusterName string, mgr manager.Manager, enabledNamespaces, ignoreServiceAccounts sets.Set[string], removeOldSecrets bool) error {
	r := &reconciler{
		client: mgr.GetClient(),
		filter: func(r reconcile.Request) bool {
			return enabledNamespaces.Has(r.Namespace) && !ignoreServiceAccounts.Has(r.String())
		},
		log:              logrus.WithField("controller", ControllerName).WithField("cluster", clusterName),
		second:           time.Second,
		removeOldSecrets: removeOldSecrets,
	}
	c, err := controller.New(fmt.Sprintf("%s_%s", ControllerName, clusterName), mgr, controller.Options{
		Reconciler: r,
		// When > 1, there will be IsConflict errors on updating the same ServiceAccount
		MaxConcurrentReconciles: 20,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(source.Kind(mgr.GetCache(), &corev1.ServiceAccount{}), &handler.EnqueueRequestForObject{}); err != nil {
		return fmt.Errorf("failed to construct watch for ServiceAccounts: %w", err)
	}
	if err := c.Watch(source.Kind(mgr.GetCache(), &corev1.Secret{}), handler.EnqueueRequestsFromMapFunc(secretMapper)); err != nil {
		return fmt.Errorf("failed to construct watch for Secrets: %w", err)
	}

	return nil
}

func secretMapper(ctx context.Context, o ctrlruntimeclient.Object) []reconcile.Request {
	secret, ok := o.(*corev1.Secret)
	if !ok {
		logrus.WithField("type", fmt.Sprintf("%T", o)).Error("Got an object that was not a secret")
		return nil
	}

	sa, ok := secret.Annotations[corev1.ServiceAccountNameKey]
	if !ok {
		return nil
	}

	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: secret.Namespace, Name: sa}}}
}

type reconciler struct {
	client ctrlruntimeclient.Client
	filter func(reconcile.Request) bool
	log    *logrus.Entry
	// Allow speeding up time for tests
	second           time.Duration
	removeOldSecrets bool
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	l := r.log.WithField("request", req.String())
	res, err := r.reconcile(ctx, l, req)
	// Ignore the logging for IsConflict errors because they are results of concurrent reconciling
	if err != nil && !apierrors.IsConflict(err) && !apierrors.IsAlreadyExists(err) {
		l.WithError(err).Error("Reconciliation failed")
	} else {
		l.Info("Finished reconciliation")
	}
	if res == nil {
		res = &reconcile.Result{}
	}
	return *res, err
}

const thirtyDays = 30 * 24 * time.Hour

func (r *reconciler) reconcile(ctx context.Context, l *logrus.Entry, req reconcile.Request) (*reconcile.Result, error) {
	if !r.filter(req) {
		return nil, nil
	}

	sa := &corev1.ServiceAccount{}
	if err := r.client.Get(ctx, req.NamespacedName, sa); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get serviaccount %s: %w", req.String(), err)
	}

	var imagePullSecretsToKeep []corev1.LocalObjectReference
	var requeueAfter time.Duration
	for _, pullSecretRef := range sa.ImagePullSecrets {
		l := l.WithField("imagepullsecretname", pullSecretRef.Name)
		secret, err := r.creationTimestampSecret(ctx, req.Namespace, pullSecretRef.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to check image pull secret creationTimestamp: %w", err)
		}
		if deleteObjectIn := objectExpiredIn(l, secret, thirtyDays); deleteObjectIn != 0 {
			l.WithField("secret", secret.Name).Infof("DeleteObjectIn: %s", deleteObjectIn.String())
			imagePullSecretsToKeep = append(imagePullSecretsToKeep, pullSecretRef)
			if requeueAfter == 0 || deleteObjectIn < requeueAfter {
				requeueAfter = deleteObjectIn
			}
			continue
		}
		l.Info("Not keeping image pull secret")
	}

	var tokenSecretsToKeep []corev1.ObjectReference
	for _, tokenSecretRef := range sa.Secrets {
		l := l.WithField("tokensecretname", tokenSecretRef.Name)
		secret, err := r.creationTimestampSecret(ctx, req.Namespace, tokenSecretRef.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to check if secret should be kept: %w", err)
		}
		if secret.Type == corev1.SecretTypeServiceAccountToken {
			// This controller does not rotate token secrets
			tokenSecretsToKeep = append(tokenSecretsToKeep, tokenSecretRef)
			continue
		}
		if deleteObjectIn := objectExpiredIn(l, secret, thirtyDays); deleteObjectIn != 0 {
			tokenSecretsToKeep = append(tokenSecretsToKeep, tokenSecretRef)
			if requeueAfter == 0 || deleteObjectIn < requeueAfter {
				requeueAfter = deleteObjectIn
			}
			continue
		}
		l.Info("Not keeping token secret")
	}

	dockerSecretDiffCount := len(imagePullSecretsToKeep) - len(sa.ImagePullSecrets)

	if dockerSecretDiffCount != 0 {
		l.WithField("docker_secret_diff_count", dockerSecretDiffCount).Info("Updating ServiceAccount")
		sa.ImagePullSecrets = imagePullSecretsToKeep
		sa.Secrets = tokenSecretsToKeep
		if err := r.client.Update(ctx, sa); err != nil {
			return nil, fmt.Errorf("failed to update ServiceAccount: %w", err)
		}
	}

	if err := wait.Poll(r.second, 30*r.second, func() (bool, error) {
		if err := r.client.Get(ctx, req.NamespacedName, sa); err != nil {
			return false, err
		}
		// Secrets include the pull secrets
		return len(sa.ImagePullSecrets) >= 1 && len(sa.Secrets) >= 1, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to wait for ServiceAccount to have a pull secret: %w", err)
	}

	if !r.removeOldSecrets {
		return &reconcile.Result{RequeueAfter: requeueAfter}, nil
	}

	var allSecrets corev1.SecretList
	if err := r.client.List(ctx, &allSecrets, ctrlruntimeclient.InNamespace(req.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list secrets in %s namespace: %w", req.Namespace, err)
	}

	for _, secret := range allSecrets.Items {
		if secret.Annotations[corev1.ServiceAccountUIDKey] != string(sa.UID) {
			continue
		}
		if deleteObjectIn := objectExpiredIn(l, &secret, 2*thirtyDays); deleteObjectIn != 0 {
			if requeueAfter == 0 || deleteObjectIn < requeueAfter {
				requeueAfter = deleteObjectIn
			}
			continue
		}

		l.WithField("name", secret.Name).WithField("age", time.Since(secret.CreationTimestamp.Time).String()).Info("Deleting secret that is older than 30 days")
		// ignore ErrNotExist as there could be a race condition where something else has already deleted the secret.
		if err := r.client.Delete(ctx, &secret); err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to delete secret %s/%s: %w", secret.Namespace, secret.Name, err)
		}
	}

	return &reconcile.Result{RequeueAfter: requeueAfter}, nil
}

func (r *reconciler) creationTimestampSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := r.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return &corev1.Secret{}, nil
		}

		return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, name, err)
	}

	return secret, nil
}

func objectExpiredIn(log *logrus.Entry, o ctrlruntimeclient.Object, ttl time.Duration) (deleteAfter time.Duration) {
	if val, exists := o.GetAnnotations()[TTLAnnotationKey]; exists {
		deleteAddAnnotationTimestamp, err := time.Parse(time.RFC3339, val)
		if err != nil {
			// No point in returning this as retrying won't help. If someone changes the value, that
			// will trigger us
			log.WithError(err).Errorf("Failed to parse %s annotation value", TTLAnnotationKey)
		} else {
			deleteAfter = time.Until(deleteAddAnnotationTimestamp)
		}
	}

	if expirationDuration := time.Until(o.GetCreationTimestamp().Time.Add(ttl)); deleteAfter == 0 || expirationDuration < deleteAfter {
		deleteAfter = expirationDuration
	}

	// We can't travel back in time to delete it at the right time so simplify the API by always returning 0 for "Expired"
	if deleteAfter < 0 {
		deleteAfter = 0
	}

	return deleteAfter
}
