package ephemeralcluster

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrlbldr "sigs.k8s.io/controller-runtime/pkg/builder"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/api"
	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
)

const (
	AbortECNotFound = "Ephemeral Cluster not found"
)

type prowJobReconciler struct {
	logger       *logrus.Entry
	masterClient ctrlruntimeclient.Client
	buildClients buildClients

	now func() time.Time
}

func ProwJobFilter(object ctrlruntimeclient.Object) bool {
	_, ok := object.GetLabels()[EphemeralClusterLabel]
	return ok
}

func addPJReconcilerToManager(logger *logrus.Entry, mgr manager.Manager, buildClients buildClients) error {
	r := prowJobReconciler{
		logger:       logger.WithField("controller", "ephemeral_cluster_provisioner_pj"),
		masterClient: mgr.GetClient(),
		buildClients: buildClients,
		now:          time.Now,
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
	log := r.logger.WithField("request", req.String())

	pj := &prowv1.ProwJob{}
	if err := r.masterClient.Get(ctx, req.NamespacedName, pj); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		} else {
			return reconcile.Result{}, err
		}
	}

	ecName, ok := pj.Labels[EphemeralClusterLabel]
	if !ok {
		return reconcile.Result{}, reconcile.TerminalError(fmt.Errorf("%s doesn't have the EC label", pj.Name))
	}

	log = log.WithField("ephemeralcluster", ecName)

	err := r.masterClient.Get(ctx, types.NamespacedName{Name: ecName, Namespace: EphemeralClusterNamespace}, &ephemeralclusterv1.EphemeralCluster{})
	if err == nil {
		return reconcile.Result{}, nil
	}

	if !apierrors.IsNotFound(err) {
		return reconcile.Result{}, err
	}

	switch pj.Status.State {
	case prowv1.AbortedState, prowv1.ErrorState, prowv1.FailureState, prowv1.SuccessState:
		log.Info("ProwJob already in a terminal state, nothing to do")
		return reconcile.Result{}, nil
	}

	return r.gracefullyTerminateClusterProvisioning(ctx, log, pj)
}

// gracefullyTerminateClusterProvisioning terminates the ProwJob. This procedure makes some assumptions about
// the cluster provisioning procedure, in particular: if the ci-operator namespace has been created, it's likely
// that the provisioning procedure has begun already. Therefore let it going but trigger the deprovisioning
// procedure right away. In this way we avoid leaking cloud resources for too long, but we rather:
//  1. Provision the cluster
//  2. Do not use it to test anything at all
//  3. Deprovision the cluster
//
// On the other hand, if ci-operator has not even created a namespace, abort the ProwJob right away since no
// cloud resources has been created so far.
func (r *prowJobReconciler) gracefullyTerminateClusterProvisioning(ctx context.Context, log *logrus.Entry, pj *prowv1.ProwJob) (reconcile.Result, error) {
	buildClient, err := r.buildClients.forCluster(pj.Spec.Cluster)
	if err != nil {
		log.WithField("cluster", pj.Spec.Cluster).WithError(err).Warn("Build client not found")
		return reconcile.Result{}, reconcile.TerminalError(err)
	}

	ns, err := findCIOperatorTestNS(ctx, buildClient, pj)
	if err != nil {
		log.WithError(err).Error("Unable to retrieve ci-operator namespace")
		return reconcile.Result{}, err
	}

	// If the test NS hasn't been created yet we can just abort the PJ, no graceful termination is needed.
	if ns == "" {
		return r.abortProwJob(ctx, pj)
	}

	log = log.WithField("namespace", ns)
	log.Info("ci-operator namespace found")

	log = log.WithField("secret", api.EphemeralClusterTestDoneSignalSecretName)
	created, err := createTestDoneSignalSecret(ctx, buildClient, ns)
	if err != nil {
		log.WithError(err).Warn("Failed to create the secret")
		return reconcile.Result{}, err
	}
	if created {
		log.Info("Secret created")
	}

	return reconcile.Result{}, nil
}

func (r *prowJobReconciler) abortProwJob(ctx context.Context, pj *prowv1.ProwJob) (reconcile.Result, error) {
	if pj.Status.State == prowv1.AbortedState {
		return reconcile.Result{}, nil
	}

	pj.Status.State = prowv1.AbortedState
	pj.Status.Description = AbortECNotFound
	pj.Status.CompletionTime = ptr.To(metav1.NewTime(r.now()))

	if err := r.masterClient.Update(ctx, pj); err != nil {
		return reconcile.Result{}, fmt.Errorf("abort prowjob: %w", err)
	}

	return reconcile.Result{}, nil
}
