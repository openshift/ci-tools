package prowjobretrigger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	prowjobsv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/github"
	"sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/controller/promotionreconciler/prowjobreconciler"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/rehearse"
)

type Options struct {
	CIOperatorConfigAgent agents.ConfigAgent
	GitHubClient          github.Client
	Enqueuer              prowjobreconciler.Enqueuer
}

const (
	ControllerName = "promotion_job_retrigger"
)

func AddToManager(mgr controllerruntime.Manager, opts Options) error {
	log := logrus.WithField("controller", ControllerName)
	r := &reconciler{
		log:          log,
		client:       mgr.GetClient(),
		gitHubClient: opts.GitHubClient,
		enqueueJob:   opts.Enqueuer,
	}

	c, err := controller.New(ControllerName, mgr, controller.Options{
		Reconciler:              r,
		MaxConcurrentReconciles: 10,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}
	if err := c.Watch(
		&source.Kind{Type: &prowjobsv1.ProwJob{}},
		handler.Funcs{
			UpdateFunc: func(e event.UpdateEvent, q workqueue.RateLimitingInterface) {
				q.Add(reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: e.ObjectNew.GetNamespace(),
						Name:      e.ObjectNew.GetName(),
					},
				})
			},
		},
		predicate.NewPredicateFuncs(func(object client.Object) bool {
			prowJob, ok := object.(*prowjobsv1.ProwJob)
			if !ok {
				return false
			}
			if prowJob.Spec.Type != prowjobsv1.PostsubmitJob {
				return false
			}
			if !strings.HasSuffix(prowJob.Spec.Job, "-images") {
				return false
			}
			return prowJob.Status.State == prowjobsv1.FailureState || prowJob.Status.State == prowjobsv1.ErrorState
		}),
	); err != nil {
		return fmt.Errorf("failed to create watch for ProwJobs: %w", err)
	}
	r.log.Info("Successfully added reconciler to manager")

	return nil
}

type RefGetter interface {
	GetRef(org, repo, ref string) (string, error)
}

type reconciler struct {
	log          *logrus.Entry
	client       ctrlruntimeclient.Client
	gitHubClient RefGetter
	enqueueJob   prowjobreconciler.Enqueuer
}

func (r *reconciler) Reconcile(ctx context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
	log := r.log.WithField("name", req.Name).WithField("namespace", req.Namespace)
	log.Trace("Starting reconciliation")
	startTime := time.Now()
	defer func() { log.WithField("duration", time.Since(startTime)).Trace("Finished reconciliation") }()

	err := r.reconcile(ctx, req, log)
	if err != nil {
		log.WithError(err).Error("Reconciliation failed")
	}

	return controllerruntime.Result{}, controllerutil.SwallowIfTerminal(err)
}

func (r *reconciler) reconcile(ctx context.Context, req controllerruntime.Request, log *logrus.Entry) error {
	prowJob := &prowjobsv1.ProwJob{}
	if err := r.client.Get(ctx, req.NamespacedName, prowJob); err != nil {
		// Object got deleted while it was in the workqueue
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get object: %w", err)
	}

	if prowJob.Spec.Refs == nil {
		return controllerutil.TerminalError(fmt.Errorf("promotion prowJob has no refs: %v", req.NamespacedName))
	}

	metadata := cioperatorapi.Metadata{
		Org:     prowJob.Spec.Refs.Org,
		Repo:    prowJob.Spec.Refs.Repo,
		Branch:  prowJob.Spec.Refs.BaseRef,
		Variant: rehearse.VariantFromLabels(prowJob.Labels),
	}
	log = log.WithField("org", metadata.Org).WithField("repo", metadata.Repo).WithField("branch", metadata.Branch)

	currentHEAD, found, err := CurrentHEADForBranch(r.gitHubClient, metadata, log)
	if err != nil {
		return fmt.Errorf("failed to get current git head: %w", err)
	}
	if !found {
		return controllerutil.TerminalError(fmt.Errorf("got 404 for %s/%s/%s from github, this likely means the repo or branch got deleted or we are not allowed to access it", metadata.Org, metadata.Repo, metadata.Branch))
	}
	if currentHEAD != prowJob.Spec.Refs.BaseSHA {
		log.Debug("Not re-triggering failed image job as it is not building images for the latest commit.")
		return nil
	}
	log = log.WithField("currentHEAD", currentHEAD)

	log.Info("Requesting prowjob creation")
	r.enqueueJob(prowjobreconciler.OrgRepoBranchCommit{
		Org:    metadata.Org,
		Repo:   metadata.Repo,
		Branch: metadata.Branch,
		Commit: currentHEAD,
	})
	return nil
}

func CurrentHEADForBranch(client RefGetter, metadata cioperatorapi.Metadata, log *logrus.Entry) (string, bool, error) {
	// We attempted for some time to use the gitClient for this, but we do so many reconciliations that
	// it results in a massive performance issues that can easely kill the developers laptop.
	ref, err := client.GetRef(metadata.Org, metadata.Repo, "heads/"+metadata.Branch)
	if err != nil {
		if github.IsNotFound(err) {
			return "", false, nil
		}
		if errors.Is(err, github.GetRefTooManyResultsError{}) {
			log.WithError(err).Debug("got multiple refs back")
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to get sha for ref %s/%s/heads/%s from github: %w", metadata.Org, metadata.Repo, metadata.Branch, err)
	}
	return ref, true, nil
}
