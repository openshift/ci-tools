package serviceaccountsecretrefresher

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

const ControllerName = "serviceaccount_secret_refresher"

func AddToManager(clusterName string, mgr manager.Manager, enabledNamespaces sets.String, removeOldSecrets bool) error {
	r := &reconciler{
		client:           mgr.GetClient(),
		filter:           func(r reconcile.Request) bool { return enabledNamespaces.Has(r.Namespace) },
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

	if err := c.Watch(&source.Kind{Type: &corev1.ServiceAccount{}}, &handler.EnqueueRequestForObject{}); err != nil {
		return fmt.Errorf("failed to construct watch for ServiceAccounts: %w", err)
	}
	if err := c.Watch(&source.Kind{Type: &corev1.Secret{}}, handler.EnqueueRequestsFromMapFunc(secretMapper)); err != nil {
		return fmt.Errorf("failed to construct watch for Secrets: %w", err)
	}

	return nil
}

func secretMapper(o ctrlruntimeclient.Object) []reconcile.Request {
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
		creationTimestamp, err := r.creationTimestampSecret(ctx, req.Namespace, pullSecretRef.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to check image pull secret creationTimestamp: %w", err)
		}
		if isObjectCurrent(*creationTimestamp) {
			imagePullSecretsToKeep = append(imagePullSecretsToKeep, pullSecretRef)
			if newRequeueAfter := -time.Since(creationTimestamp.Time.Add(thirtyDays)); requeueAfter == 0 || newRequeueAfter < requeueAfter {
				requeueAfter = newRequeueAfter
			}
			continue
		}
		l.WithField("imagepullsecretname", pullSecretRef.Name).Info("Not keeping image pull secret")
	}

	var tokenSecretsToKeep []corev1.ObjectReference
	for _, tokenSecretRef := range sa.Secrets {
		creationTimestamp, err := r.creationTimestampSecret(ctx, req.Namespace, tokenSecretRef.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to check if secret should be kept: %w", err)
		}
		if isObjectCurrent(*creationTimestamp) {
			tokenSecretsToKeep = append(tokenSecretsToKeep, tokenSecretRef)
			if newRequeueAfter := -time.Since(creationTimestamp.Time.Add(thirtyDays)); requeueAfter == 0 || newRequeueAfter < requeueAfter {
				requeueAfter = newRequeueAfter
			}
			continue
		}
		l.WithField("tokensecretname", tokenSecretRef.Name).Info("Not keeping token secret")
	}

	dockerSecretDiffCount, tokenSecretDiffCount := len(imagePullSecretsToKeep)-len(sa.ImagePullSecrets), len(tokenSecretsToKeep)-len(sa.Secrets)

	if dockerSecretDiffCount != 0 || tokenSecretDiffCount != 0 {
		l.WithField("docker_secret_diff_count", dockerSecretDiffCount).WithField("token_secret_diff_count", tokenSecretDiffCount).Info("Updating ServiceAccount")
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
		// Secrets includes the pull secret, hence the >= 2 instead of >=1
		return len(sa.ImagePullSecrets) >= 1 && len(sa.Secrets) >= 2, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to wait for ServiceAccount to have a pull and a tokenSecret: %w", err)
	}

	if !r.removeOldSecrets {
		return &reconcile.Result{RequeueAfter: requeueAfter}, nil
	}

	var allSecrets corev1.SecretList
	if err := r.client.List(ctx, &allSecrets, ctrlruntimeclient.InNamespace(req.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list secrets in %s namespace: %w", req.Namespace, err)
	}

	for _, secret := range allSecrets.Items {
		if secret.Annotations[corev1.ServiceAccountUIDKey] != string(sa.UID) || isObjectCurrent(secret.CreationTimestamp) {
			continue
		}

		l.WithField("name", secret.Name).WithField("age", time.Since(secret.CreationTimestamp.Time).String()).Info("Deleting secret that is older than 30 days")
		if err := r.client.Delete(ctx, &secret); err != nil {
			return nil, fmt.Errorf("failed to delete secret %s/%s: %w", secret.Namespace, secret.Name, err)
		}
	}

	return &reconcile.Result{RequeueAfter: requeueAfter}, nil
}

func (r *reconciler) creationTimestampSecret(ctx context.Context, namespace, name string) (*metav1.Time, error) {
	secret := &corev1.Secret{}
	if err := r.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return &metav1.Time{}, nil
		}

		return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, name, err)
	}

	return &secret.CreationTimestamp, nil
}

func isObjectCurrent(t metav1.Time) bool {
	return t.Time.After(time.Now().Add(-thirtyDays))
}
