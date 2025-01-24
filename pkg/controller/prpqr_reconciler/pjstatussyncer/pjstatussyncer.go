package pjstatussyncer

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	v1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
)

const (
	controllerName = "prowjob_status_syncer"

	conditionAllJobsFinished = "AllJobsFinished"
)

func AddToManager(mgr controllerruntime.Manager, ns string) error {
	ctrl, err := controller.New(controllerName, mgr, controller.Options{
		MaxConcurrentReconciles: 1,
		Reconciler: &reconciler{
			logger: logrus.WithField("controller", controllerName),

			client: mgr.GetClient(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	// Watch only on updates
	predicateFuncs := predicate.TypedFuncs[*prowv1.ProwJob]{
		CreateFunc: func(event.TypedCreateEvent[*prowv1.ProwJob]) bool { return false },
		DeleteFunc: func(event.TypedDeleteEvent[*prowv1.ProwJob]) bool { return false },
		UpdateFunc: func(e event.TypedUpdateEvent[*prowv1.ProwJob]) bool {
			if _, ok := e.ObjectNew.GetLabels()[v1.PullRequestPayloadQualificationRunLabel]; !ok {
				return false
			}

			if e.ObjectNew.GetNamespace() != ns {
				return false
			}

			return true
		},
		GenericFunc: func(tge event.TypedGenericEvent[*prowv1.ProwJob]) bool { return false },
	}

	if err := ctrl.Watch(source.Kind(mgr.GetCache(), &prowv1.ProwJob{}, pjHandler(), predicateFuncs)); err != nil {
		return fmt.Errorf("failed to create watch: %w", err)
	}

	return nil
}

func pjHandler() handler.TypedEventHandler[*prowv1.ProwJob, reconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc[*prowv1.ProwJob](func(ctx context.Context, pj *prowv1.ProwJob) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: pj.Namespace, Name: pj.Name}}}
	})
}

var _ reconcile.Reconciler = &reconciler{}

type reconciler struct {
	logger *logrus.Entry
	client ctrlruntimeclient.Client
}

func (r *reconciler) Reconcile(ctx context.Context, request controllerruntime.Request) (controllerruntime.Result, error) {
	log := r.logger.WithField("request", request.String())
	err := r.reconcile(ctx, log, request)
	if err != nil {
		log.WithError(err).Error("Reconciliation failed")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

func (r *reconciler) reconcile(ctx context.Context, log *logrus.Entry, req controllerruntime.Request) error {
	logger := log.WithField("namespace", req.Namespace).WithField("name", req.Name)
	logger.Info("Starting reconciliation")

	var prpqrMutations []func(prpqr *v1.PullRequestPayloadQualificationRun)

	pj := &prowv1.ProwJob{}
	if err := r.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: req.Namespace, Name: req.Name}, pj); err != nil {
		return fmt.Errorf("failed to get the ProwJob: %s in namespace %s: %w", req.Name, req.Namespace, err)
	}

	prpqrName := pj.Labels[v1.PullRequestPayloadQualificationRunLabel]
	prpqr := &v1.PullRequestPayloadQualificationRun{}
	if err := r.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: req.Namespace, Name: prpqrName}, prpqr); err != nil {
		return fmt.Errorf("failed to get the PullRequestPayloadQualificationRun: %s in namespace %s: %w", prpqrName, req.Namespace, err)
	}
	prpqrMutations = append(prpqrMutations, func(prpqr *v1.PullRequestPayloadQualificationRun) {
		for i, job := range prpqr.Status.Jobs {
			if job.ProwJob == pj.Name && !reflect.DeepEqual(pj.Status, job.Status) {
				prpqr.Status.Jobs[i].Status = pj.Status

			}
		}
	})

	prpqrMutations = append(prpqrMutations, func(prpqr *v1.PullRequestPayloadQualificationRun) {
		conditionFound := false
		condition := constructCondition(prpqr.Status.Jobs)
		for i, conditions := range prpqr.Status.Conditions {
			if conditions.Type == conditionAllJobsFinished {
				prpqr.Status.Conditions[i] = condition
				conditionFound = true
				break
			}
		}
		if !conditionFound {
			prpqr.Status.Conditions = append(prpqr.Status.Conditions, condition)
		}
	})

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		prpqr := &v1.PullRequestPayloadQualificationRun{}
		if err := r.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: req.Namespace, Name: prpqrName}, prpqr); err != nil {
			return fmt.Errorf("failed to get the PullRequestPayloadQualificationRun: %s in namespace %s: %w", prpqrName, req.Namespace, err)
		}

		for _, mutate := range prpqrMutations {
			mutate(prpqr)
		}

		logger.WithField("to_state", pj.Status.State).Info("Updating PullRequestPayloadQualificationRun...")
		if err := r.client.Update(ctx, prpqr); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update PullRequestPayloadQualificationRun %s: %w", prpqr.Name, err)
	}

	return nil
}

func constructCondition(jobs []v1.PullRequestPayloadJobStatus) metav1.Condition {
	status := metav1.ConditionTrue
	message := "All jobs have finished."

	if !hasAllJobsFinished(jobs) {
		status = metav1.ConditionFalse

		runningJobs := getRunningJobs(jobs)
		message = fmt.Sprintf("jobs [%s] still running", strings.Join(runningJobs, ","))
	}

	return metav1.Condition{
		Type:               conditionAllJobsFinished,
		Status:             status,
		LastTransitionTime: metav1.Time{Time: time.Now()},
		Reason:             conditionAllJobsFinished,
		Message:            message,
	}
}

func getRunningJobs(jobs []v1.PullRequestPayloadJobStatus) []string {
	var ret []string
	for _, job := range jobs {
		if IsActiveState(job.Status.State) {
			ret = append(ret, job.ReleaseJobName)
		}
	}
	return ret
}

func hasAllJobsFinished(jobs []v1.PullRequestPayloadJobStatus) bool {
	for _, job := range jobs {
		if IsActiveState(job.Status.State) {
			return false
		}
	}
	return true
}

func IsActiveState(state prowv1.ProwJobState) bool {
	return state == prowv1.PendingState || state == prowv1.TriggeredState || state == prowv1.SchedulingState
}
