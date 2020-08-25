package secretsyncer

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	crcontrollerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/controller/secretsyncer/config"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
)

const (
	ControllerName       = "secret_syncer"
	referenceClusterName = "api.ci"
)

func AddToManager(mgr manager.Manager,
	referenceCluster manager.Manager,
	otherBuildClusters map[string]manager.Manager,
	config config.Getter,
	secretbootstrapConfig secretbootstrap.Config,
) error {

	r := &reconciler{
		ctx:                    context.Background(),
		log:                    logrus.WithField("controller", ControllerName),
		config:                 config,
		referenceClusterClient: referenceCluster.GetClient(),
		clients:                map[string]ctrlruntimeclient.Client{referenceClusterName: referenceCluster.GetClient()},
		targetFilter:           filterFromConfig(secretbootstrapConfig),
	}
	c, err := controller.New(ControllerName, mgr, controller.Options{
		Reconciler:              r,
		MaxConcurrentReconciles: 20,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	// Like the original implementation we don't handle the case of "someone changed target secret, find
	// source secret" at all, hence we don't need watches in the target cluster.
	// The api.ci manager happens to have a 24h resync interval, hence we are still  eventually consistent
	// for ppl with patience.
	allTargetClusters := sets.NewString(referenceClusterName)
	for targetCluster, targetClusterManager := range otherBuildClusters {
		r.clients[targetCluster] = targetClusterManager.GetClient()
		allTargetClusters.Insert(targetCluster)
	}

	if err := c.Watch(
		source.NewKindWithCache(&corev1.Secret{}, referenceCluster.GetCache()),
		&handler.EnqueueRequestsFromMapFunc{ToRequests: handler.ToRequestsFunc(
			func(mo handler.MapObject) []reconcile.Request {
				// The reference cluster enq all clusters
				var requests []reconcile.Request
				for _, targetCluster := range allTargetClusters.List() {
					requests = append(requests, requestForCluster(targetCluster, mo.Meta.GetNamespace(), mo.Meta.GetName()))
				}
				return requests
			},
		)},
	); err != nil {
		return fmt.Errorf("failed to create watch on reference cluster %s: %w", referenceClusterName, err)
	}

	return nil
}

type filter func(cluster string, target types.NamespacedName) bool

type reconciler struct {
	ctx                    context.Context
	log                    *logrus.Entry
	config                 config.Getter
	referenceClusterClient ctrlruntimeclient.Client
	clients                map[string]ctrlruntimeclient.Client
	targetFilter           filter
}

func (r *reconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithField("request", request.String())
	err := r.reconcile(log, request)
	if err != nil {
		log.WithError(err).Error("Reconciliation failed")
	} else {
		log.Trace("Finished reconciliation successfully")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

func (r *reconciler) reconcile(log *logrus.Entry, encodedRequest reconcile.Request) error {
	cluster, sourceSecretName, err := decodeRequest(encodedRequest)
	if err != nil {
		return fmt.Errorf("failed to decode request %s: %w", encodedRequest, err)
	}
	*log = *log.WithField("cluster", cluster)
	client, ok := r.clients[cluster]
	if !ok {
		return controllerutil.TerminalError(fmt.Errorf("no client for cluster %s available", cluster))
	}
	log.WithField("request", sourceSecretName.String()).Trace("Reconciling")

	source := &corev1.Secret{}
	if err := r.referenceClusterClient.Get(r.ctx, sourceSecretName, source); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get secret %s: %w", sourceSecretName.String(), err)
	}
	if source.DeletionTimestamp != nil {
		log.Debug("not doing work for secret because it is being deleted")
		return nil
	}

	if len(source.Data) == 0 {
		log.Trace("not updating target secret as source has no data")
		return nil
	}

	var mirrorErrors []error
	for _, mirrorConfig := range r.config().Secrets {
		if mirrorConfig.From.Namespace != sourceSecretName.Namespace || mirrorConfig.From.Name != sourceSecretName.Name {
			continue
		}

		// Verify this doesn't target anything managed by ci-secret-boostrap
		if allowed := r.targetFilter(cluster, types.NamespacedName{Namespace: mirrorConfig.To.Namespace, Name: mirrorConfig.To.Name}); !allowed {
			// Log a big fat error, but don't return it as that would result in retrying.
			log.WithField("target", mirrorConfig.String()).Error("Ignoring secret, its target is managed by ci-secret-boostrap")
			continue
		}
		// TODO: Ideally we would would create one reconcile.Request per target. This here means
		// that if a single target is broken, it will send all targets into exponential backoff.
		mirrorErrors = append(mirrorErrors, r.mirrorSecret(log, client, source, mirrorConfig.To))
	}

	return utilerrors.NewAggregate(mirrorErrors)
}

func (r *reconciler) mirrorSecret(log *logrus.Entry, c ctrlruntimeclient.Client, source *corev1.Secret, to config.SecretLocation) error {
	*log = *log.WithFields(logrus.Fields{"target-namespace": to.Namespace, "target-secret": to.Name})

	secret, mutateFn := secret(
		types.NamespacedName{Namespace: to.Namespace, Name: to.Name},
		source.Data,
		source.Type,
	)
	result, err := crcontrollerutil.CreateOrUpdate(r.ctx, c, secret, mutateFn)
	if err != nil {
		// Happens if someone changes the type, just re-create it
		if strings.Contains(err.Error(), "field is immutable") {
			if err := recreateSecret(r.ctx, c, secret); err != nil {
				return fmt.Errorf("failed to recreate secret %s/%s: %w", secret.Namespace, secret.Name, err)
			}
		}
		return fmt.Errorf("failed to upsert secret: %w", err)
	}
	if result != crcontrollerutil.OperationResultNone {
		log.WithField("operation", result).Info("Upsert succeeded")
	}
	return nil
}

func recreateSecret(ctx context.Context, c ctrlruntimeclient.Client, s *corev1.Secret) error {
	if err := c.Delete(ctx, s.DeepCopy()); err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}
	s.ObjectMeta = metav1.ObjectMeta{
		Namespace:   s.Namespace,
		Name:        s.Name,
		Labels:      s.Labels,
		Annotations: s.Annotations,
	}
	if err := c.Create(ctx, s); err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}
	return nil
}

func secret(nn types.NamespacedName, data map[string][]byte, tp corev1.SecretType) (*corev1.Secret, crcontrollerutil.MutateFn) {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: nn.Namespace,
			Name:      nn.Name,
		},
	}
	return s, func() error {
		if s.Labels == nil {
			s.Labels = map[string]string{}
		}
		s.Labels["ci.openshift.org/secret-syncer-controller-managed"] = "true"
		if s.Data == nil {
			s.Data = map[string][]byte{}
		}
		for k, v := range data {
			s.Data[k] = v
		}
		s.Type = tp
		return nil
	}
}

func requestForCluster(clusterName, namespace, name string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: clusterName + "_" + namespace,
			Name:      name,
		},
	}
}

func decodeRequest(req reconcile.Request) (string, types.NamespacedName, error) {
	clusterAndNamespace := strings.Split(req.Namespace, "_")
	if n := len(clusterAndNamespace); n != 2 {
		return "", types.NamespacedName{}, fmt.Errorf("didn't get two but %d segments when trying to extract cluster and namespace", n)
	}
	return clusterAndNamespace[0], types.NamespacedName{Namespace: clusterAndNamespace[1], Name: req.Name}, nil
}

func filterFromConfig(cfg secretbootstrap.Config) filter {
	forbidden := sets.String{}
	for _, cfg := range cfg {
		for _, target := range cfg.To {
			forbidden.Insert(fmt.Sprintf("%s/%s/%s", target.Cluster, target.Namespace, target.Name))
		}
	}
	return func(cluster string, secretName types.NamespacedName) bool {
		return !forbidden.Has(fmt.Sprintf("%s/%s/%s", cluster, secretName.Namespace, secretName.Name))
	}
}
