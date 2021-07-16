package promotionnamespacereconciler

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load/agents"
)

const (
	ControllerName  = "promotion_namespace_reconciler"
	configIndexName = ControllerName + "_namespace_index"
)

func AddToManager(mgr manager.Manager, configAgent agents.ConfigAgent) error {
	indexChanges, err := configAgent.SubscribeToIndexChanges(configIndexName)
	if err != nil {
		return fmt.Errorf("failed to subscribe to the config index for %s: %w", configIndexName, err)
	}

	log := logrus.WithField("controller", ControllerName)
	c, err := controller.New(ControllerName, mgr, controller.Options{
		Reconciler:              &reconciler{client: mgr.GetClient(), log: log},
		MaxConcurrentReconciles: 100,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}
	if err := c.Watch(&source.Channel{Source: indexChangesToEvents(log, indexChanges)}, &handler.EnqueueRequestForObject{}); err != nil {
		return fmt.Errorf("failed to construct watch: %w", err)
	}
	if err := configAgent.AddIndex(configIndexName, indexPromotionNamespaces()); err != nil {
		return fmt.Errorf("failed to add index for promotion namespaces to config agent: %w", err)
	}
	return nil
}

func indexChangesToEvents(l *logrus.Entry, changes <-chan agents.IndexDelta) <-chan event.GenericEvent {
	result := make(chan event.GenericEvent)
	go func() {
		for change := range changes {
			if len(change.Added) == 0 {
				continue
			}
			for _, config := range change.Added {
				if config.PromotionConfiguration != nil && config.PromotionConfiguration.Namespace != "" {
					l.WithField("name", config.PromotionConfiguration.Namespace).Debug("Enqueueing namespace")
					result <- event.GenericEvent{Object: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: config.PromotionConfiguration.Namespace}}}
				}
			}
		}
	}()

	return result
}

func indexPromotionNamespaces() agents.IndexFn {
	return func(cfg api.ReleaseBuildConfiguration) []string {
		if cfg.PromotionConfiguration == nil || cfg.PromotionConfiguration.Namespace == "" {
			return nil
		}
		return []string{cfg.PromotionConfiguration.Namespace}
	}
}

type reconciler struct {
	log    *logrus.Entry
	client ctrlruntimeclient.Client
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithField("request", req.String())
	err := r.reconcile(ctx, log, req)
	if err != nil && !apierrors.IsConflict(err) {
		log.WithError(err).Error("Reconciliation failed")
	} else {
		log.Info("Finished reconciliation")
	}
	return reconcile.Result{}, err
}

func (r *reconciler) reconcile(ctx context.Context, l *logrus.Entry, req reconcile.Request) error {
	l.Debug("Starting reconciliation")
	var ns corev1.Namespace
	if err := r.client.Get(ctx, req.NamespacedName, &ns); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get namespace %s: %w", req.NamespacedName, err)
		}
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   req.Name,
			Labels: map[string]string{"created-by": ControllerName},
		}}
		if err := r.client.Create(ctx, namespace); err != nil {
			return fmt.Errorf("failed to create namespace %s: %w", req.Name, err)
		}
		l.Info("Created namespace")
	}

	// Nothing to do if namespace already exists
	return nil
}
