package prpqr_reconciler

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"reflect"
	"time"

	"github.com/sirupsen/logrus"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pjutil"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/ci-tools/pkg/api"
	v1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/controller/prpqr_reconciler/pjstatussyncer"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/prowgen"
)

const (
	controllerName           = "prpqr_reconciler"
	releaseJobNameLabel      = "releaseJobNameHash"
	releaseJobNameAnnotation = "releaseJobName"

	conditionAllJobsTriggered = "AllJobsTriggered"
	conditionWithErrors       = "WithErrors"
)

type injectingResolverClient interface {
	ConfigWithTest(base *api.Metadata, testSource *api.MetadataWithTest) (*api.ReleaseBuildConfiguration, error)
}

type prowConfigGetter interface {
	Config() periodicDefaulter
}

type wrappedProwConfigAgent struct {
	pc *prowconfig.Agent
}

func (w *wrappedProwConfigAgent) Config() periodicDefaulter {
	return w.pc.Config()
}

type periodicDefaulter interface {
	DefaultPeriodic(periodic *prowconfig.Periodic) error
}

func AddToManager(mgr manager.Manager, ns string, rc injectingResolverClient, prowConfigAgent *prowconfig.Agent) error {
	if err := pjstatussyncer.AddToManager(mgr, ns); err != nil {
		return fmt.Errorf("failed to construct pjstatussyncer: %w", err)
	}

	c, err := controller.New(controllerName, mgr, controller.Options{
		MaxConcurrentReconciles: 1,
		Reconciler: &reconciler{
			logger:               logrus.WithField("controller", controllerName),
			client:               mgr.GetClient(),
			configResolverClient: rc,
			prowConfigGetter:     &wrappedProwConfigAgent{pc: prowConfigAgent},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	// Watch only on Create events
	predicateFuncs := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return e.Object.GetNamespace() == ns
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		UpdateFunc:  func(event.UpdateEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
	if err := c.Watch(source.NewKindWithCache(&v1.PullRequestPayloadQualificationRun{}, mgr.GetCache()), prpqrHandler(), predicateFuncs); err != nil {
		return fmt.Errorf("failed to create watch for PullRequestPayloadQualificationRun: %w", err)
	}

	return nil
}

func prpqrHandler() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(o ctrlruntimeclient.Object) []reconcile.Request {
		prpqr, ok := o.(*v1.PullRequestPayloadQualificationRun)
		if !ok {
			logrus.WithField("type", fmt.Sprintf("%T", o)).Error("Got object that was not a PullRequestPayloadQualificationRun")
			return nil
		}

		return []reconcile.Request{
			{NamespacedName: types.NamespacedName{Namespace: prpqr.Namespace, Name: prpqr.Name}},
		}
	})
}

type reconciler struct {
	logger *logrus.Entry
	client ctrlruntimeclient.Client

	configResolverClient injectingResolverClient
	prowConfigGetter     prowConfigGetter
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
	logger = logger.WithField("namespace", req.Namespace).WithField("prpqr_name", req.Name)
	logger.Info("Starting reconciliation")

	statuses := make(map[string]*v1.PullRequestPayloadJobStatus)

	prpqr := &v1.PullRequestPayloadQualificationRun{}
	if err := r.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: req.Namespace, Name: req.Name}, prpqr); err != nil {
		return fmt.Errorf("failed to get the PullRequestPayloadQualificationRun: %s in namespace %s: %w", req.Name, req.Namespace, err)
	}

	existingProwjobs := &prowv1.ProwJobList{}
	if err := r.client.List(ctx, existingProwjobs, ctrlruntimeclient.MatchingLabels{v1.PullRequestPayloadQualificationRunLabel: prpqr.Name}); err != nil {
		return fmt.Errorf("failed to get ProwJobs for this PullRequestPayloadQualifiactionRun: %w", err)
	}
	existingProwjobsByNameHash := map[string]*prowv1.ProwJob{}
	for i, pj := range existingProwjobs.Items {
		existingProwjobsByNameHash[pj.Labels[releaseJobNameLabel]] = &existingProwjobs.Items[i]
	}

	statusByJobName := map[string]*v1.PullRequestPayloadJobStatus{}
	for i := range prpqr.Status.Jobs {
		jobName := prpqr.Status.Jobs[i].ReleaseJobName
		statusByJobName[jobName] = &prpqr.Status.Jobs[i]
	}

	baseMetadata := metadataFromPullRequestUnderTest(prpqr.Spec.PullRequest)
	for _, jobSpec := range prpqr.Spec.Jobs.Jobs {
		mimickedJob := jobSpec.JobName(jobconfig.PeriodicPrefix)
		logger = logger.WithFields(logrus.Fields{"want-job": mimickedJob})

		if status, exists := statusByJobName[mimickedJob]; exists {
			logger.WithField("prowjob", status.ProwJob).Debug("Job already present in status")
			statuses[mimickedJob] = status
			continue
		}

		if job, exists := existingProwjobsByNameHash[jobNameHash(mimickedJob)]; exists {
			logger.WithField("prowjob", job.Name).Debug("Prowjob already exists")
			statuses[mimickedJob] = &v1.PullRequestPayloadJobStatus{
				ReleaseJobName: mimickedJob,
				ProwJob:        job.Name,
				Status:         job.Status,
			}
			continue
		}

		ciopConfig, err := resolveCiopConfig(r.configResolverClient, baseMetadata, jobSpec)
		if err != nil {
			logger.WithError(err).Error("Failed to resolve the ci-operator configuration")
			statuses[mimickedJob] = &v1.PullRequestPayloadJobStatus{
				ReleaseJobName: mimickedJob,
				Status: prowv1.ProwJobStatus{
					State:       prowv1.ErrorState,
					Description: err.Error(),
				},
			}
			continue
		}

		prowjob, err := generateProwjob(ciopConfig, r.prowConfigGetter.Config(), baseMetadata, req.Name, req.Namespace, &jobSpec, &prpqr.Spec.PullRequest)
		if err != nil {
			logger.WithError(err).Error("Failed to create a payload prowjob")
			statuses[mimickedJob] = &v1.PullRequestPayloadJobStatus{
				ReleaseJobName: mimickedJob,
				Status: prowv1.ProwJobStatus{
					State:       prowv1.ErrorState,
					Description: err.Error(),
				},
			}
			continue
		}

		logger.Info("Creating prowjob...")
		if err := r.client.Create(ctx, prowjob); err != nil {
			statuses[mimickedJob] = &v1.PullRequestPayloadJobStatus{
				ReleaseJobName: mimickedJob,
				Status: prowv1.ProwJobStatus{
					State:       prowv1.ErrorState,
					Description: fmt.Errorf("failed to create prowjob: %w", err).Error(),
				},
			}
			continue
		}

		// There is some delay until it gets back to our cache, so block until we can retrieve
		// it successfully.
		key := ctrlruntimeclient.ObjectKey{Namespace: prowjob.Namespace, Name: prowjob.Name}
		if err := wait.Poll(100*time.Millisecond, 5*time.Second, func() (bool, error) {
			if err := r.client.Get(ctx, key, &prowv1.ProwJob{}); err != nil {
				if kerrors.IsNotFound(err) {
					return false, nil
				}
				return false, fmt.Errorf("getting prowJob failed: %w", err)
			}
			return true, nil
		}); err != nil {
			statuses[mimickedJob] = &v1.PullRequestPayloadJobStatus{
				ReleaseJobName: mimickedJob,
				Status: prowv1.ProwJobStatus{
					State:       prowv1.ErrorState,
					Description: fmt.Errorf("created job never appeared in cache: %w", err).Error(),
				},
			}
			continue
		}

		statuses[mimickedJob] = &v1.PullRequestPayloadJobStatus{
			ReleaseJobName: mimickedJob,
			ProwJob:        prowjob.Name,
			Status:         prowjob.Status,
		}
	}

	allJobsTriggeredCondition := constructCondition(statuses)

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		prpqr := &v1.PullRequestPayloadQualificationRun{}
		if err := r.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: req.Namespace, Name: req.Name}, prpqr); err != nil {
			return fmt.Errorf("failed to get the PullRequestPayloadQualificationRun: %w", err)
		}

		oldStatus := prpqr.Status.DeepCopy()
		reconcileStatus(prpqr, statuses, allJobsTriggeredCondition)
		if reflect.DeepEqual(*oldStatus, prpqr.Status) {
			logger.Info("PullRequestPayloadQualificationRun status is up to date, no updates necessary")
			return nil
		}

		logger.Info("Updating PullRequestPayloadQualificationRun...")
		if err := r.client.Update(ctx, prpqr); err != nil {
			return fmt.Errorf("failed to update PullRequestPayloadQualificationRun %s: %w", prpqr.Name, err)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func reconcileStatus(theirs *v1.PullRequestPayloadQualificationRun, ourStatuses map[string]*v1.PullRequestPayloadJobStatus, ourCondition metav1.Condition) {
	var foundCondition bool
	for i := range theirs.Status.Conditions {
		if theirs.Status.Conditions[i].Type == ourCondition.Type {
			if theirs.Status.Conditions[i].Status != ourCondition.Status {
				theirs.Status.Conditions[i] = ourCondition
			}
			foundCondition = true
			break
		}
	}
	if !foundCondition {
		theirs.Status.Conditions = append(theirs.Status.Conditions, ourCondition)
	}

	statusByJobName := map[string]*v1.PullRequestPayloadJobStatus{}
	for i := range theirs.Status.Jobs {
		jobName := theirs.Status.Jobs[i].ReleaseJobName
		statusByJobName[jobName] = &theirs.Status.Jobs[i]
	}

	theirs.Status.Jobs = []v1.PullRequestPayloadJobStatus{}
	for _, spec := range theirs.Spec.Jobs.Jobs {
		jobName := spec.JobName(jobconfig.PeriodicPrefix)
		our := ourStatuses[jobName]
		their := statusByJobName[jobName]
		theirs.Status.Jobs = append(theirs.Status.Jobs, reconcileJobStatus(jobName, their, our))
	}
}

func reconcileJobStatus(name string, their, our *v1.PullRequestPayloadJobStatus) v1.PullRequestPayloadJobStatus {
	if their == nil && our == nil {
		return v1.PullRequestPayloadJobStatus{
			ReleaseJobName: name,
			Status: prowv1.ProwJobStatus{
				State:       prowv1.ErrorState,
				Description: fmt.Sprintf("BUG: job '%s' not present in old nor new status", name),
			},
		}
	}

	if their == nil {
		return *our
	}
	if our == nil {
		return *their
	}

	if their.ProwJob != our.ProwJob {
		// TODO(muller): Weird state which we should probably log as a bug
		return *our
	}
	if their.Status.State == prowv1.ErrorState {
		return *our
	}
	return *their
}

func constructCondition(statuses map[string]*v1.PullRequestPayloadJobStatus) metav1.Condition {
	message := "All jobs triggered successfully"
	reason := conditionAllJobsTriggered
	status := metav1.ConditionTrue

	for _, jobStatus := range statuses {
		if jobStatus.Status.State == prowv1.ErrorState {
			message = "Jobs triggered with errors"
			status = metav1.ConditionFalse
			reason = conditionWithErrors
		}
	}

	return metav1.Condition{
		Type:               conditionAllJobsTriggered,
		Status:             status,
		LastTransitionTime: metav1.Time{Time: time.Now()},
		Reason:             reason,
		Message:            message,
	}
}

func jobNameHash(name string) string {
	hasher := md5.New()
	// MD5 Write never returns error
	_, _ = hasher.Write([]byte(name))
	return hex.EncodeToString(hasher.Sum(nil))
}

func resolveCiopConfig(rc injectingResolverClient, baseCiop *api.Metadata, spec v1.ReleaseJobSpec) (*api.ReleaseBuildConfiguration, error) {
	inject := &api.MetadataWithTest{
		Metadata: api.Metadata{
			Org:     spec.CIOperatorConfig.Org,
			Repo:    spec.CIOperatorConfig.Repo,
			Branch:  spec.CIOperatorConfig.Branch,
			Variant: spec.CIOperatorConfig.Variant,
		},
		Test: spec.Test,
	}

	ciopConfig, err := rc.ConfigWithTest(baseCiop, inject)
	if err != nil {
		return nil, fmt.Errorf("failed to get config from resolver: %w", err)
	}

	return ciopConfig, nil
}

func generateProwjob(ciopConfig *api.ReleaseBuildConfiguration, defaulter periodicDefaulter, baseCiop *api.Metadata, prpqrName, prpqrNamespace string, spec *v1.ReleaseJobSpec, pr *v1.PullRequestUnderTest) (*prowv1.ProwJob, error) {
	fakeProwgenInfo := &prowgen.ProwgenInfo{Metadata: *baseCiop}

	mimickedJob := spec.JobName(jobconfig.PeriodicPrefix)
	labels := map[string]string{
		v1.PullRequestPayloadQualificationRunLabel: prpqrName,
		releaseJobNameLabel:                        jobNameHash(mimickedJob),
	}
	annotations := map[string]string{
		releaseJobNameAnnotation: mimickedJob,
	}

	inject := &api.MetadataWithTest{
		Metadata: api.Metadata{
			Org:     spec.CIOperatorConfig.Org,
			Repo:    spec.CIOperatorConfig.Repo,
			Branch:  spec.CIOperatorConfig.Branch,
			Variant: spec.CIOperatorConfig.Variant,
		},
		Test: spec.Test,
	}

	var periodic *prowconfig.Periodic
	for i := range ciopConfig.Tests {
		if ciopConfig.Tests[i].As != inject.Test {
			continue
		}
		jobBaseGen := prowgen.NewProwJobBaseBuilderForTest(ciopConfig, fakeProwgenInfo, prowgen.NewCiOperatorPodSpecGenerator(), ciopConfig.Tests[i])
		jobBaseGen.PodSpec.Add(prowgen.InjectTestFrom(inject))

		// Avoid sharing when we run the same job multiple times.
		// PRPQR name should be safe to use as a discriminating input, because
		// there should never be more than one execution of a specific job per
		// PRPQR (until aggregated jobs, but for them we'll have a sequence index)
		jobBaseGen.PodSpec.Add(prowgen.CustomHashInput(prpqrName))

		// TODO(muller): Solve cluster assignment
		jobBaseGen.Cluster("build01")
		periodic = prowgen.GeneratePeriodicForTest(jobBaseGen, fakeProwgenInfo, "@yearly", "", false, ciopConfig.CanonicalGoRepository)
		var variant string
		if inject.Variant != "" {
			variant = fmt.Sprintf("-%s", inject.Variant)
		}
		periodic.Name = fmt.Sprintf("%s-%s-%d%s-%s", baseCiop.Org, baseCiop.Repo, pr.PullRequest.Number, variant, inject.Test)
		break
	}
	// We did not find the injected test: this is a bug
	if periodic == nil {
		return nil, fmt.Errorf("BUG: test '%s' not found in injected config", inject.Test)
	}

	extraRefs := prowv1.Refs{
		Org:  baseCiop.Org,
		Repo: baseCiop.Repo,
		// TODO(muller): All these commented-out fields need to be propagated via the PRPQR spec
		// We do not need them now but we should eventually wire them through
		// RepoLink:  pr.Base.Repo.HTMLURL,
		BaseRef: pr.BaseRef,
		BaseSHA: pr.BaseSHA,
		// BaseLink:  fmt.Sprintf("%s/commit/%s", pr.Base.Repo.HTMLURL, pr.BaseSHA),
		PathAlias: periodic.ExtraRefs[0].PathAlias,
		Pulls: []prowv1.Pull{
			{
				Number: pr.PullRequest.Number,
				Author: pr.PullRequest.Author,
				SHA:    pr.PullRequest.SHA,
				Title:  pr.PullRequest.Title,
				// Link:       pr.HTMLURL,
				// AuthorLink: pr.User.HTMLURL,
				// CommitLink: fmt.Sprintf("%s/pull/%d/commits/%s", pr.Base.Repo.HTMLURL, pr.Number, pr.Head.SHA),
			},
		},
	}
	periodic.ExtraRefs = []prowv1.Refs{extraRefs}

	if err := defaulter.DefaultPeriodic(periodic); err != nil {
		return nil, fmt.Errorf("failed to default the ProwJob: %w", err)
	}

	pj := pjutil.NewProwJob(pjutil.PeriodicSpec(*periodic), labels, annotations)
	pj.Namespace = prpqrNamespace

	return &pj, nil
}

func metadataFromPullRequestUnderTest(pr v1.PullRequestUnderTest) *api.Metadata {
	return &api.Metadata{Org: pr.Org, Repo: pr.Repo, Branch: pr.BaseRef}
}
