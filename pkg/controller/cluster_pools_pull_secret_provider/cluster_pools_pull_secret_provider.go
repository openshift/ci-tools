package cluster_pools_pull_secret_provider

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/util"
)

const ControllerName = "cluster_pools_pull_secret_provider"

func AddToManager(manager manager.Manager,
	sourcePullSecretNamespace, sourcePullSecretName string) error {
	log := logrus.WithField("controller", ControllerName)

	client := manager.GetClient()
	r := &reconciler{
		log:                       log,
		client:                    client,
		sourcePullSecretNamespace: sourcePullSecretNamespace,
		sourcePullSecretName:      sourcePullSecretName,
	}
	c, err := controller.New(ControllerName, manager, controller.Options{
		Reconciler: r,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(source.Kind(manager.GetCache(),
		&hivev1.ClusterPool{},
		clusterPoolHandler(sourcePullSecretNamespace))); err != nil {
		return fmt.Errorf("failed to create watch for clusterpools: %w", err)
	}

	if err := c.Watch(source.Kind(manager.GetCache(),
		&corev1.Secret{},
		secretHandler(sourcePullSecretNamespace, sourcePullSecretName, client))); err != nil {
		return fmt.Errorf("failed to create watch for secrets: %w", err)
	}

	r.log.Info("Successfully added reconciler to manager")
	return nil

}

func clusterPoolHandler(sourcePullSecretNamespace string) handler.TypedEventHandler[*hivev1.ClusterPool] {
	return handler.TypedEnqueueRequestsFromMapFunc[*hivev1.ClusterPool](func(ctx context.Context, clusterPool *hivev1.ClusterPool) []reconcile.Request {
		if clusterPool.Namespace == sourcePullSecretNamespace {
			return nil
		}
		return []reconcile.Request{{NamespacedName: types.NamespacedName{
			Namespace: clusterPool.Namespace,
			Name:      clusterPool.Name,
		}}}
	})
}

func requestsFactoryForSecretEvent(namespace string, client ctrlruntimeclient.Client) []reconcile.Request {
	clusterPools := &hivev1.ClusterPoolList{}
	if err := client.List(context.TODO(), clusterPools, ctrlruntimeclient.InNamespace(namespace)); err != nil {
		logrus.WithField("namespace", namespace).WithError(err).Error("Failed to list the cluster pools")
		return nil
	}
	var requests []reconcile.Request
	for _, pool := range clusterPools.Items {
		logrus.WithField("namespace", pool.Namespace).WithField("name", pool.Name).Info("Found cluster pool")
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: pool.Namespace,
			Name:      pool.Name,
		}})
	}
	return requests
}

func secretHandler(
	sourcePullSecretNamespace string,
	sourcePullSecretName string,
	client ctrlruntimeclient.Client,
) handler.TypedEventHandler[*corev1.Secret] {
	return handler.TypedEnqueueRequestsFromMapFunc[*corev1.Secret](func(ctx context.Context, secret *corev1.Secret) []reconcile.Request {
		if secret.Name != sourcePullSecretName {
			return nil
		}
		var namespace string
		if secret.Namespace != sourcePullSecretNamespace {
			logrus.Info("The pull secret in the target namespace is changed")
			namespace = secret.Namespace
		} else {
			logrus.Info("The source pull secret is changed")
		}
		return requestsFactoryForSecretEvent(namespace, client)
	})
}

type reconciler struct {
	log                       *logrus.Entry
	client                    ctrlruntimeclient.Client
	sourcePullSecretNamespace string
	sourcePullSecretName      string
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithField("request", req.String())
	err := r.reconcile(ctx, req, log)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		log.WithError(err).Error("Reconciliation failed")
	} else {
		log.Info("Finished reconciliation")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

func (r *reconciler) reconcile(ctx context.Context, req reconcile.Request, log *logrus.Entry) error {

	*log = *log.WithField("namespace", req.Namespace).WithField("name", req.Name)
	log.Info("Starting reconciliation")

	clusterPool := &hivev1.ClusterPool{}
	if err := r.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: req.Namespace, Name: req.Name}, clusterPool); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("The cluster pool is deleted")
			return nil
		}
		return fmt.Errorf("failed to get the cluster pool %s in namespace %s: %w", req.Name, req.Namespace, err)
	}

	if clusterPool.Spec.PullSecretRef == nil {
		log.Info("The cluster pool does not refer a secret")
		return nil
	}

	if clusterPool.Spec.PullSecretRef.Name != r.sourcePullSecretName {
		log.WithField("clusterPool.Spec.PullSecretRef.Name", clusterPool.Spec.PullSecretRef.Name).
			Infof("The cluster pool does not refer secret %s", r.sourcePullSecretName)
		return nil
	}

	secret := &corev1.Secret{}
	if err := r.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: r.sourcePullSecretNamespace, Name: r.sourcePullSecretName}, secret); err != nil {
		return fmt.Errorf("failed to get the secret %s in namespace %s: %w", r.sourcePullSecretName, r.sourcePullSecretNamespace, err)
	}

	secret.ObjectMeta = metav1.ObjectMeta{
		Name:        r.sourcePullSecretName,
		Namespace:   req.Namespace,
		Labels:      secret.Labels,
		Annotations: secret.Annotations,
	}

	if secret.Labels == nil {
		secret.Labels = make(map[string]string)
	}
	secret.Labels[api.DPTPRequesterLabel] = ControllerName

	logrus.Info("Upserting the pull secret")
	created, err := util.UpsertImmutableSecret(ctx, r.client, secret)
	if err != nil {
		return fmt.Errorf("failed to upsert the secret %s in namespace %s: %w", secret.Name, secret.Namespace, err)
	}
	log.WithField("created", created).Info("The pull secret is upserted successfully")
	return nil
}
