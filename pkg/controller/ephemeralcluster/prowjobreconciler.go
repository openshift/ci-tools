package ephemeralcluster

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrlbldr "sigs.k8s.io/controller-runtime/pkg/builder"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
)

const (
	EphemeralClusterProwJobLabel = "ci.openshift.io/ephemeral-cluster"
	AbortECNotFound              = "Ephemeral Cluster not found"
)

type prowJobReconciler struct {
	logger *logrus.Entry
	client ctrlclient.Client

	now func() time.Time
}

func ProwJobFilter(object ctrlclient.Object) bool {
	_, ok := object.GetLabels()[EphemeralClusterProwJobLabel]
	return ok
}

func addPJReconcilerToManager(logger *logrus.Entry, mgr manager.Manager) error {
	r := prowJobReconciler{
		logger: logger,
		client: mgr.GetClient(),
	}

	if err := ctrlbldr.ControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		WithEventFilter(predicate.NewPredicateFuncs(ProwJobFilter)).
		For(&prowv1.ProwJob{}).
		Complete(&r); err != nil {
		return fmt.Errorf("build controller: %w", err)
	}

	return nil
}

func (r *prowJobReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	pj := &prowv1.ProwJob{}
	if err := r.client.Get(ctx, req.NamespacedName, pj); err != nil {
		if kerrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		} else {
			return reconcile.Result{}, err
		}
	}

	ecName, ok := pj.Labels[EphemeralClusterNameLabel]
	if !ok {
		return reconcile.Result{}, reconcile.TerminalError(fmt.Errorf("%s doesn't have the EC label", pj.Name))
	}

	pj = pj.DeepCopy()

	ec := ephemeralclusterv1.EphemeralCluster{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: ecName, Namespace: EphemeralClusterNamespace}, &ec); err != nil {
		if kerrors.IsNotFound(err) {
			return r.abortProwJob(ctx, pj)
		} else {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *prowJobReconciler) abortProwJob(ctx context.Context, pj *prowv1.ProwJob) (reconcile.Result, error) {
	if pj.Status.State == prowv1.AbortedState {
		return reconcile.Result{}, nil
	}

	pj.Status.State = prowv1.AbortedState
	pj.Status.Description = AbortECNotFound
	pj.Status.CompletionTime = ptr.To(v1.NewTime(r.now()))

	if err := r.client.Update(ctx, pj); err != nil {
		return reconcile.Result{}, fmt.Errorf("abort prowjob: %w", err)
	}

	return reconcile.Result{}, nil
}
