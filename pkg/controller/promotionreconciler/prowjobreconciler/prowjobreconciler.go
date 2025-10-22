package prowjobreconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/prow/pkg/pjutil"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
)

// OrgRepoBranchCommit represents a GitHub commit
type OrgRepoBranchCommit struct {
	Org    string
	Repo   string
	Branch string
	Commit string
}

// Enqueuer allows the caller to Enqueue an OrgRepoBranchCommit
type Enqueuer func(OrgRepoBranchCommit)

const controllerName = "promotion_job_creator"

func AddToManager(mgr controllerruntime.Manager, config config.Getter, dryRun bool) (Enqueuer, error) {
	createdJobsCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: controllerName,
		Name:      "prowjobs_created",
		Help:      "The number of prowjobs the controller created",
	}, []string{"org", "repo", "branch"})
	if err := metrics.Registry.Register(createdJobsCounter); err != nil {
		return nil, fmt.Errorf("failed to register createdJobsCounter metric: %w", err)
	}

	ctrl, err := controller.New(controllerName, mgr, controller.Options{
		MaxConcurrentReconciles: 10,
		Reconciler: &reconciler{
			log:    logrus.WithField("controller", controllerName),
			config: config,
			client: mgr.GetClient(),
			createdProwJobLabels: map[string]string{
				"openshift.io/created-by": controllerName,
			},
			createdJobsCounter: createdJobsCounter,
			dryRun:             dryRun,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to construct controller: %w", err)
	}
	enqueuer, src := newSource()

	if err := ctrl.Watch(src); err != nil {
		return nil, fmt.Errorf("failed to create watch: %w", err)
	}

	return enqueuer, nil
}

func newSource() (Enqueuer, source.Source) {
	channel := make(chan event.TypedGenericEvent[*prowv1.ProwJob])
	enqueuer := func(orbc OrgRepoBranchCommit) {
		channel <- orcbToEvent(orbc)
	}
	src := source.Channel(channel, &handler.TypedEnqueueRequestForObject[*prowv1.ProwJob]{})
	return enqueuer, src
}

func orcbToEvent(orbc OrgRepoBranchCommit) event.TypedGenericEvent[*prowv1.ProwJob] {
	// The object type is irrelvant for us but we need to fulfill the client.Object interface
	return event.TypedGenericEvent[*prowv1.ProwJob]{Object: &prowv1.ProwJob{ObjectMeta: metav1.ObjectMeta{
		Name: fmt.Sprintf("%s|%s|%s|%s", orbc.Org, orbc.Repo, orbc.Branch, orbc.Commit),
	}}}
}

func nameToORBC(name string) (*OrgRepoBranchCommit, error) {
	split := strings.Split(name, "|")
	if n := len(split); n != 4 {
		return nil, fmt.Errorf("splitting string by '|' did not return four but %d results. This is a bug", n)
	}
	return &OrgRepoBranchCommit{
		Org:    split[0],
		Repo:   split[1],
		Branch: split[2],
		Commit: split[3],
	}, nil
}

var _ reconcile.Reconciler = &reconciler{}

type reconciler struct {
	log                  *logrus.Entry
	config               config.Getter
	client               ctrlruntimeclient.Client
	createdProwJobLabels map[string]string
	createdJobsCounter   *prometheus.CounterVec
	dryRun               bool
}

func (r *reconciler) Reconcile(ctx context.Context, request controllerruntime.Request) (controllerruntime.Result, error) {
	log := r.log.WithField("request", request.String())
	err := r.reconcile(ctx, log, request)
	if err != nil {
		log.WithError(err).Error("Reconciliation failed")
	}
	return reconcile.Result{}, err
}

func (r *reconciler) reconcile(ctx context.Context, log *logrus.Entry, request controllerruntime.Request) error {
	orbc, err := nameToORBC(request.Name)
	if err != nil {
		return nonRetriableError{err: fmt.Errorf("failed to decode key: %w", err)}
	}

	pj := r.getPromotionJob(orbc)
	if pj == nil {
		log.Debug("no promotion job found, doing nothing")
		return nil
	}

	isJobAlreadyRunning, err := r.isJobAlreadyRunning(ctx, pj)
	if err != nil {
		return fmt.Errorf("failed to check if job is already running: %w", err)
	}
	// There is no guarantee it succeededs, but we get retriggered periodically anyways
	if isJobAlreadyRunning {
		return nil
	}

	if r.dryRun {
		serialized, _ := json.Marshal(pj)
		log.WithField("job_name", pj.Spec.Job).WithField("job", string(serialized)).Info("Not creating prowjob because dryRun is enabled")
		r.createdJobsCounter.WithLabelValues(orbc.Org, orbc.Repo, orbc.Branch).Inc()
		return nil
	}

	if err := r.client.Create(ctx, pj); err != nil {
		return fmt.Errorf("failed to create prowjob: %w", err)
	}
	r.createdJobsCounter.WithLabelValues(orbc.Org, orbc.Repo, orbc.Branch).Inc()
	log.WithField("name", pj.Name).WithField("job", pj.Spec.Job).Info("Successfully created prowjob")

	// There is some delay until it gets back to our cache, so block until we can retrieve
	// it successfully.
	key := ctrlruntimeclient.ObjectKey{Namespace: pj.Namespace, Name: pj.Name}
	if err := wait.Poll(100*time.Millisecond, 5*time.Second, func() (bool, error) {
		if err := r.client.Get(ctx, key, &prowv1.ProwJob{}); err != nil {
			if kerrors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("getting prowJob failed: %w", err)
		}
		return true, nil
	}); err != nil {
		return fmt.Errorf("failed to wait for created ProwJob to appear in cache: %w", err)
	}

	return nil
}

func (r *reconciler) isJobAlreadyRunning(ctx context.Context, pj *prowv1.ProwJob) (bool, error) {

	if pj.Labels[kube.ProwJobAnnotation] == "" ||
		pj.Labels[kube.OrgLabel] == "" ||
		pj.Labels[kube.RepoLabel] == "" {
		return false, fmt.Errorf("reference job didn't have all of prowJobName(%s), orgname(%s) and repoName(%s) labels set", pj.Labels[kube.ProwJobAnnotation], pj.Labels[kube.OrgLabel], pj.Labels[kube.RepoLabel])
	}

	labelSelector := ctrlruntimeclient.MatchingLabels{
		kube.ProwJobAnnotation: pj.Labels[kube.ProwJobAnnotation],
		kube.OrgLabel:          pj.Labels[kube.OrgLabel],
		kube.RepoLabel:         pj.Labels[kube.RepoLabel],
		kube.ProwJobTypeLabel:  string(prowv1.PostsubmitJob),
	}
	namespaceSelector := ctrlruntimeclient.InNamespace(r.config().ProwJobNamespace)

	prowJobs := &prowv1.ProwJobList{}
	if err := r.client.List(ctx, prowJobs, labelSelector, namespaceSelector); err != nil {
		return false, fmt.Errorf("failed to list prowjobs: %w", err)
	}

	for _, job := range prowJobs.Items {
		if job.Complete() {
			continue
		}
		if job.Spec.Refs != nil && job.Spec.Refs.BaseSHA == pj.Spec.Refs.BaseSHA {
			return true, nil
		}
	}

	return false, nil
}

func (r *reconciler) getPromotionJob(orbc *OrgRepoBranchCommit) *prowv1.ProwJob {
	cfg := r.config()
	for _, postsubmit := range cfg.PostsubmitsStatic[orbc.Org+"/"+orbc.Repo] {
		if !postsubmit.Brancher.ShouldRun(orbc.Branch) {
			continue
		}
		if !cioperatorapi.IsPromotionJob(postsubmit.Labels) {
			continue
		}
		refs := prowv1.Refs{
			Org:     orbc.Org,
			Repo:    orbc.Repo,
			BaseRef: orbc.Branch,
			BaseSHA: orbc.Commit,
		}
		pj := pjutil.NewProwJob(pjutil.PostsubmitSpec(postsubmit, refs), r.createdProwJobLabels, nil, pjutil.RequireScheduling(cfg.Scheduler.Enabled))
		pj.Namespace = cfg.ProwJobNamespace
		return &pj
	}

	return nil
}

type nonRetriableError struct {
	err error
}

func (nre nonRetriableError) Error() string {
	return nre.err.Error()
}
