package testimagestreamimportcleaner

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	testimagestreamtagimportv1 "github.com/openshift/ci-tools/pkg/api/testimagestreamtagimport/v1"
)

const ControllerName = "testimagestreamimportcleaner"

func AddToManager(
	mgr manager.Manager,
	allManagers map[string]manager.Manager,
) error {
	for clusterName, clusterManager := range allManagers {
		c, err := controller.New(ControllerName+"_"+clusterName, mgr, controller.Options{
			Reconciler:              &reconciler{client: clusterManager.GetClient(), now: time.Now},
			MaxConcurrentReconciles: 10,
		})
		if err != nil {
			return fmt.Errorf("failed to construct controller for cluster %s: %w", clusterName, err)
		}
		if err := c.Watch(source.NewKindWithCache(&testimagestreamtagimportv1.TestImageStreamTagImport{}, clusterManager.GetCache()), &handler.EnqueueRequestForObject{}); err != nil {
			return fmt.Errorf("failed to watch testimagestreamtagimports in cluster %s: %w", clusterName, err)
		}
	}

	return nil
}

type reconciler struct {
	client ctrlruntimeclient.Client
	now    func() time.Time
}

const sevenDays = 7 * 24 * time.Hour

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var tagImport testimagestreamtagimportv1.TestImageStreamTagImport
	if err := r.client.Get(ctx, req.NamespacedName, &tagImport); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("failed to get %s: %w", req, err)
	}

	if age := r.now().Sub(tagImport.CreationTimestamp.Time); age < sevenDays {
		return reconcile.Result{RequeueAfter: sevenDays - age}, nil
	}

	return reconcile.Result{}, r.client.Delete(ctx, &tagImport)
}
