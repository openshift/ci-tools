package hypershift_namespace_reconciler

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
)

const ControllerName = "hypershift_namespace_reconciler"

func AddToManager(manager manager.Manager) error {
	log := logrus.WithField("controller", ControllerName)

	client := manager.GetClient()
	r := &reconciler{
		log:    log,
		client: client,
	}
	c, err := controller.New(ControllerName, manager, controller.Options{
		Reconciler: r,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	predicates := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return hypershiftNamespace(e.Object.GetLabels()) },
		DeleteFunc: func(event.DeleteEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return hypershiftNamespace(e.ObjectNew.GetLabels())
		},
		GenericFunc: func(e event.GenericEvent) bool { return hypershiftNamespace(e.Object.GetLabels()) },
	}

	if err := c.Watch(
		source.Kind(manager.GetCache(), &corev1.Namespace{}),
		namespaceHandler(),
		predicates,
	); err != nil {
		return fmt.Errorf("failed to create watch for namespaces: %w", err)
	}

	r.log.Info("Successfully added reconciler to manager")
	return nil
}

func hypershiftNamespace(labels map[string]string) bool {
	if labels == nil {
		return false
	}
	value, ok := labels["hypershift.openshift.io/hosted-control-plane"]
	if !ok || (value != "" && value != "true") {
		return false
	}
	return true
}

func namespaceHandler() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o ctrlruntimeclient.Object) []reconcile.Request {
		ns, ok := o.(*corev1.Namespace)
		if !ok {
			logrus.WithField("type", fmt.Sprintf("%T", o)).Error("Got object that was not a namespace")
			return nil
		}
		return []reconcile.Request{{NamespacedName: types.NamespacedName{
			Name: ns.Name,
		}}}
	})
}

type reconciler struct {
	log    *logrus.Entry
	client ctrlruntimeclient.Client
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

	*log = *log.WithField("name", req.Name)
	log.Info("Starting reconciliation")

	if err := controllerutil.EnsureNamespaceNotMonitored(ctx, req.Name, r.client, log); err != nil {
		return fmt.Errorf("failed ot ensure namespace %s not monitored: %w", req.Name, err)
	}
	return nil
}
