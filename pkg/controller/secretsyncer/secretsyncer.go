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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/controller/secretsyncer/config"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
)

const (
	ControllerName = "secret_syncer"
)

// Enqueuer allows the caller to Enqueue a config change event
type Enqueuer func(allSecretsInConfig []config.MirrorConfig)

func AddToManager(mgr manager.Manager,
	referenceClusterName string,
	referenceCluster manager.Manager,
	otherBuildClusters map[string]manager.Manager,
	config config.Getter,
	secretbootstrapConfig secretbootstrap.Config,
) (Enqueuer, error) {

	r := &reconciler{
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
		return nil, fmt.Errorf("failed to construct controller: %w", err)
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
		handler.EnqueueRequestsFromMapFunc(func(o ctrlruntimeclient.Object) []reconcile.Request {
			// The reference cluster enq all clusters
			var requests []reconcile.Request
			for _, targetCluster := range allTargetClusters.List() {
				requests = append(requests, requestForCluster(targetCluster, o.GetNamespace(), o.GetName()))
			}
			return requests
		}),
	); err != nil {
		return nil, fmt.Errorf("failed to create watch on reference cluster %s: %w", referenceClusterName, err)
	}

	enqueuer, src := newConfigChangeSource()

	if err := c.Watch(src, handler.EnqueueRequestsFromMapFunc(func(o ctrlruntimeclient.Object) []reconcile.Request {
		// The reference cluster enq all clusters
		var requests []reconcile.Request
		for _, targetCluster := range allTargetClusters.List() {
			requests = append(requests, requestForCluster(targetCluster, o.GetNamespace(), o.GetName()))
		}
		return requests
	})); err != nil {
		return nil, fmt.Errorf("failed to create watch on reference cluster %s: %w", referenceClusterName, err)
	}

	return enqueuer, nil
}

func newConfigChangeSource() (Enqueuer, source.Source) {
	channel := make(chan event.GenericEvent)
	src := &source.Channel{
		Source: channel,
	}
	enqueuer := func(allSecretsInConfig []config.MirrorConfig) {
		for _, secret := range allSecretsInConfig {
			channel <- event.GenericEvent{Object: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: secret.From.Namespace, Name: secret.From.Name}}}
		}
	}
	return enqueuer, src
}

type filter func(target *boostrapSecretConfigTarget) bool

type reconciler struct {
	log                    *logrus.Entry
	config                 config.Getter
	referenceClusterClient ctrlruntimeclient.Client
	clients                map[string]ctrlruntimeclient.Client
	targetFilter           filter
}

func (r *reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithField("request", request.String())
	err := r.reconcile(ctx, log, request)
	if err != nil {
		if apierrors.IsConflict(err) {
			log.WithError(err).Trace("Reconciliation failed because of conflicts only")
		} else {
			log.WithError(err).Error("Reconciliation failed")
		}
	} else {
		log.Trace("Finished reconciliation successfully")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

func (r *reconciler) reconcile(ctx context.Context, log *logrus.Entry, encodedRequest reconcile.Request) error {
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
	if err := r.referenceClusterClient.Get(ctx, sourceSecretName, source); err != nil {
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
	var conflictErrors []error
	for _, mirrorConfig := range r.config().Secrets {
		if mirrorConfig.From.Namespace != sourceSecretName.Namespace || mirrorConfig.From.Name != sourceSecretName.Name || (mirrorConfig.To.Cluster != nil && *mirrorConfig.To.Cluster != cluster) {
			continue
		}

		// TODO: Ideally we would would create one reconcile.Request per target. This here means
		// that if a single target is broken, it will send all targets into exponential backoff.
		if err := r.mirrorSecret(ctx, log, cluster, client, source, mirrorConfig.To.SecretLocation); err != nil {
			if apierrors.IsConflict(err) {
				conflictErrors = append(conflictErrors, err)
			} else {
				mirrorErrors = append(mirrorErrors, err)
			}
		}
	}

	// If we *only* hit conflict errors return a "representative" sample of them to pass a IsConflict check
	if len(conflictErrors) > 0 && len(mirrorErrors) == 0 {
		return conflictErrors[0]
	}
	return utilerrors.NewAggregate(append(mirrorErrors, conflictErrors...))
}

func (r *reconciler) mirrorSecret(ctx context.Context, log *logrus.Entry, cluster string, c ctrlruntimeclient.Client, source *corev1.Secret, to config.SecretLocation) error {
	*log = *log.WithFields(logrus.Fields{"target-namespace": to.Namespace, "target-secret": to.Name})

	data := map[string][]byte{}
	for k := range source.Data {
		target := &boostrapSecretConfigTarget{cluster: cluster, namespace: to.Namespace, name: to.Name, key: k}
		if !r.targetFilter(target) {
			// Log a big fat error, but don't return it as that would result in retrying.
			log.WithField("target", target.String()).Error("Ignoring secret key, its target is managed by ci-secret-boostrap")
			continue
		}
		data[k] = source.Data[k]
	}
	secret, mutateFn := secret(
		types.NamespacedName{Namespace: to.Namespace, Name: to.Name},
		data,
		source.Type,
	)
	result, err := crcontrollerutil.CreateOrUpdate(ctx, c, secret, mutateFn)
	if err != nil {
		// Happens if someone changes the type, just re-create it
		if strings.Contains(err.Error(), "field is immutable") {
			if err := recreateSecret(ctx, c, secret); err != nil {
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
		s.Labels[api.DPTPRequesterLabel] = ControllerName
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

type boostrapSecretConfigTarget struct {
	cluster   string
	namespace string
	name      string
	key       string
}

func (s boostrapSecretConfigTarget) String() string {
	return s.cluster + "/" + s.namespace + "/" + s.name + "/" + s.key
}

func filterFromConfig(cfg secretbootstrap.Config) filter {
	forbidden := sets.String{}
	for _, cfg := range cfg.Secrets {
		for _, target := range cfg.To {
			for from := range cfg.From {
				forbidden.Insert(boostrapSecretConfigTarget{cluster: target.Cluster, namespace: target.Namespace, name: target.Name, key: from}.String())
			}
		}
	}
	return func(s *boostrapSecretConfigTarget) bool {
		return !forbidden.Has(s.String())
	}
}
