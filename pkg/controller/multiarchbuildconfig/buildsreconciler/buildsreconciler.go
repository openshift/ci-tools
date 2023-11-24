package buildsreconciler

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	buildv1 "github.com/openshift/api/build/v1"

	v1 "github.com/openshift/ci-tools/pkg/api/multiarchbuildconfig/v1"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
)

const (
	controllerName = "builds_reconciler"
)

func AddToManager(mgr manager.Manager) error {
	c, err := controller.New(controllerName, mgr, controller.Options{
		MaxConcurrentReconciles: 1,
		Reconciler: &reconciler{
			logger: logrus.WithField("controller", controllerName),
			client: mgr.GetClient(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	predicateFuncs := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			_, exists := e.Object.GetLabels()[v1.MultiArchBuildConfigNameLabel]
			return exists
		},
		DeleteFunc: func(event.DeleteEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			_, exists := e.ObjectNew.GetLabels()[v1.MultiArchBuildConfigNameLabel]
			return exists
		},
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
	if err := c.Watch(source.Kind(mgr.GetCache(), &buildv1.Build{}), buildsHandler(), predicateFuncs); err != nil {
		return fmt.Errorf("failed to create watch for Builds: %w", err)
	}

	return nil
}

func buildsHandler() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o ctrlruntimeclient.Object) []reconcile.Request {
		build, ok := o.(*buildv1.Build)
		if !ok {
			logrus.WithField("type", fmt.Sprintf("%T", o)).Error("Got object that was not a Build")
			return nil
		}

		return []reconcile.Request{
			{NamespacedName: types.NamespacedName{Namespace: build.Namespace, Name: build.Name}},
		}
	})
}

type reconciler struct {
	logger *logrus.Entry
	client ctrlruntimeclient.Client
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := r.logger.WithField("request", req.String())
	err := r.reconcile(ctx, req, logger)
	if err != nil {
		logger.WithError(err).Error("Reconciliation failed")
	} else {
		logger.Info("Finished reconciliation")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

func (r *reconciler) reconcile(ctx context.Context, req reconcile.Request, logger *logrus.Entry) error {
	logger = logger.WithField("namespace", req.Namespace).WithField("build-name", req.Name)
	logger.Info("Starting reconciliation")

	build := &buildv1.Build{}
	if err := r.client.Get(ctx, req.NamespacedName, build); err != nil {
		return fmt.Errorf("failed to get Build %s: %w", req.NamespacedName, err)
	}

	if err := v1.UpdateMultiArchBuildConfig(ctx, logger, r.client, ctrlruntimeclient.ObjectKey{Namespace: build.Namespace, Name: build.GetLabels()[v1.MultiArchBuildConfigNameLabel]},
		func(mabcToMutate *v1.MultiArchBuildConfig) {
			if mabcToMutate.Status.Builds == nil {
				mabcToMutate.Status.Builds = make(map[string]*buildv1.Build)
			}
			mabcToMutate.Status.Builds[fmt.Sprintf("%s/%s", build.Namespace, build.Name)] = build
		}); err != nil {
		return err
	}

	return nil
}
