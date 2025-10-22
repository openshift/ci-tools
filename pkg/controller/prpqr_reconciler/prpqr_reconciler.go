package prpqr_reconciler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/pjutil"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	v1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/controller/prpqr_reconciler/pjstatussyncer"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/prowgen"
)

const (
	controllerName           = "prpqr_reconciler"
	releaseJobNameLabel      = "releaseJobNameHash"
	releaseJobNameAnnotation = "releaseJobName"

	conditionAllJobsTriggered = "AllJobsTriggered"
	conditionWithErrors       = "WithErrors"

	aggregationIDLabel = "release.openshift.io/aggregation-id"

	dependentProwJobsFinalizer = "pullrequestpayloadqualificationruns.ci.openshift.io/dependent-prowjobs"
)

type injectingResolverClient interface {
	ConfigWithTest(base *cioperatorapi.Metadata, testSource *cioperatorapi.MetadataWithTest) (*cioperatorapi.ReleaseBuildConfiguration, error)
}

type prowConfigGetter interface {
	Defaulter() periodicDefaulter
	Config() *prowconfig.Config
}

type wrappedProwConfigAgent struct {
	pc *prowconfig.Agent
}

func (w *wrappedProwConfigAgent) Defaulter() periodicDefaulter {
	return w.pc.Config()
}

func (w *wrappedProwConfigAgent) Config() *prowconfig.Config {
	return w.pc.Config()
}

type periodicDefaulter interface {
	DefaultPeriodic(periodic *prowconfig.Periodic) error
}

type jobClusterCache struct {
	clusterForJob map[string]string
	lastCleared   time.Time
}

func AddToManager(mgr manager.Manager, ns string, rc injectingResolverClient, prowConfigAgent *prowconfig.Agent, dispatcherAddress string, jobTriggerWaitDuration time.Duration, defaultAggregatorJobTimeout time.Duration, defaultMultiRefJobTimeout time.Duration) error {
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
			dispatcherClient:     dispatcher.NewClient(dispatcherAddress),
			jobClusterCache: jobClusterCache{
				clusterForJob: make(map[string]string),
				lastCleared:   time.Now(),
			},
			jobTriggerWaitDuration:      jobTriggerWaitDuration,
			defaultAggregatorJobTimeout: defaultAggregatorJobTimeout,
			defaultMultiRefJobTimeout:   defaultMultiRefJobTimeout,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	predicateFuncs := predicate.TypedFuncs[*v1.PullRequestPayloadQualificationRun]{
		CreateFunc: func(e event.TypedCreateEvent[*v1.PullRequestPayloadQualificationRun]) bool {
			return e.Object.GetNamespace() == ns
		},
		DeleteFunc: func(e event.TypedDeleteEvent[*v1.PullRequestPayloadQualificationRun]) bool { return false },
		UpdateFunc: func(e event.TypedUpdateEvent[*v1.PullRequestPayloadQualificationRun]) bool {
			return e.ObjectNew.GetNamespace() == ns
		},
		GenericFunc: func(e event.TypedGenericEvent[*v1.PullRequestPayloadQualificationRun]) bool {
			return false
		},
	}
	if err := c.Watch(source.Kind(mgr.GetCache(), &v1.PullRequestPayloadQualificationRun{}, prpqrHandler(), predicateFuncs)); err != nil {
		return fmt.Errorf("failed to create watch for PullRequestPayloadQualificationRun: %w", err)
	}

	return nil
}

func prpqrHandler() handler.TypedEventHandler[*v1.PullRequestPayloadQualificationRun, reconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc[*v1.PullRequestPayloadQualificationRun](func(ctx context.Context, prpqr *v1.PullRequestPayloadQualificationRun) []reconcile.Request {
		return []reconcile.Request{
			{NamespacedName: types.NamespacedName{Namespace: prpqr.Namespace, Name: prpqr.Name}},
		}
	})
}

type reconciler struct {
	logger *logrus.Entry
	client ctrlruntimeclient.Client

	dispatcherClient dispatcher.Client
	jobClusterCache

	configResolverClient injectingResolverClient
	prowConfigGetter     prowConfigGetter

	jobTriggerWaitDuration time.Duration

	defaultAggregatorJobTimeout time.Duration
	defaultMultiRefJobTimeout   time.Duration
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := r.logger.WithField("request", req.String())
	err := r.reconcile(ctx, req, logger, time.Second)
	if err != nil {
		logger.WithError(err).Error("Reconciliation failed")
	} else {
		logger.Info("Finished reconciliation")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

func (r *reconciler) reconcile(ctx context.Context, req reconcile.Request, logger *logrus.Entry, second time.Duration) error {
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

	if !prpqr.GetDeletionTimestamp().IsZero() {
		r.abortJobs(ctx, logger, prpqr, existingProwjobs, statuses)
	} else {
		r.triggerJobs(ctx, logger, req, prpqr, existingProwjobs, statuses, second)
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

func (r *reconciler) triggerJobs(ctx context.Context,
	logger *logrus.Entry,
	req reconcile.Request,
	prpqr *v1.PullRequestPayloadQualificationRun,
	existingProwjobs *prowv1.ProwJobList,
	statuses map[string]*v1.PullRequestPayloadJobStatus,
	second time.Duration,
) {
	existingProwjobsByNameHash := map[string]*prowv1.ProwJob{}
	for i, pj := range existingProwjobs.Items {
		existingProwjobsByNameHash[pj.Labels[releaseJobNameLabel]] = &existingProwjobs.Items[i]
	}

	statusByJobName := map[string]*v1.PullRequestPayloadJobStatus{}
	for i := range prpqr.Status.Jobs {
		jobName := prpqr.Status.Jobs[i].ReleaseJobName
		statusByJobName[jobName] = &prpqr.Status.Jobs[i]
	}

	pullRequests := prpqr.Spec.PullRequests
	baseMetadata := metadataFromPullRequestsUnderTest(pullRequests)

	for _, jobSpec := range prpqr.Spec.Jobs.Jobs {
		var prowjobsToCreate []*prowv1.ProwJob
		mimickedJob := jobSpec.JobName(jobconfig.PeriodicPrefix)
		if jobSpec.AggregatedCount > 0 {
			// We treat the aggregator job as the mimicked job, and we assume if this job exists then
			// all the aggregated jobs exist too.
			mimickedJob = fmt.Sprintf("aggregator-%s", jobSpec.JobName(jobconfig.PeriodicPrefix))
		}
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

		inject := &cioperatorapi.MetadataWithTest{
			Metadata: cioperatorapi.Metadata{
				Org:     jobSpec.CIOperatorConfig.Org,
				Repo:    jobSpec.CIOperatorConfig.Repo,
				Branch:  jobSpec.CIOperatorConfig.Branch,
				Variant: jobSpec.CIOperatorConfig.Variant,
			},
			Test: jobSpec.Test,
		}

		// If we are not testing against any pull requests, we can set the base metadata to that of the injected test
		if len(pullRequests) == 0 {
			baseMetadata.Org = inject.Metadata.Org
			baseMetadata.Repo = inject.Metadata.Repo
			baseMetadata.Branch = inject.Metadata.Branch
			baseMetadata.Variant = inject.Metadata.Variant
		}
		ciopConfig, err := resolveCiopConfig(r.configResolverClient, baseMetadata, inject)
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

		if jobSpec.AggregatedCount > 0 {
			uid := jobNameHash(req.Name + mimickedJob)
			aggregatedProwjobs, err := r.generateAggregatedProwjobs(uid, ciopConfig, baseMetadata, req.Name, req.Namespace, &jobSpec, pullRequests, inject, jobSpec.ShardCount, jobSpec.ShardIndex)
			if err != nil {
				logger.WithError(err).Error("Failed to generate the aggregated prowjobs")
				statuses[mimickedJob] = &v1.PullRequestPayloadJobStatus{
					ReleaseJobName: mimickedJob,
					Status: prowv1.ProwJobStatus{
						State:       prowv1.ErrorState,
						Description: fmt.Errorf("failed to generate the aggregated prowjobs: %w", err).Error(),
					},
				}
				continue
			}
			prowjobsToCreate = append(prowjobsToCreate, aggregatedProwjobs...)

			submitted := generateJobNameToSubmit(inject, pullRequests, jobSpec.ShardCount, jobSpec.ShardIndex)
			aggregatorJob, err := generateAggregatorJob(baseMetadata, uid, mimickedJob, jobSpec.JobName(jobconfig.PeriodicPrefix), req.Name, req.Namespace, r.prowConfigGetter, time.Now(), submitted, r.defaultAggregatorJobTimeout)
			if err != nil {
				logger.WithError(err).Error("Failed to generate an aggregator prowjob")
				statuses[mimickedJob] = &v1.PullRequestPayloadJobStatus{
					ReleaseJobName: mimickedJob,
					Status: prowv1.ProwJobStatus{
						State:       prowv1.ErrorState,
						Description: fmt.Errorf("failed to create an aggregator prowjob: %w", err).Error(),
					},
				}
				continue
			}
			statuses[mimickedJob] = &v1.PullRequestPayloadJobStatus{
				ReleaseJobName: mimickedJob,
				ProwJob:        aggregatorJob.Name,
				Status:         aggregatorJob.Status,
			}
			prowjobsToCreate = append(prowjobsToCreate, aggregatorJob)

		} else {
			initialPullSpecOverride := prpqr.Spec.InitialPayloadBase
			// "base" is always treated as "latest" as that is what we are layering changes on top of, additional logic will apply if this changes in the future
			basePullSpecOverride := prpqr.Spec.PayloadOverrides.BasePullSpec
			prowjob, err := r.generateProwjob(ciopConfig, baseMetadata, req.Name, req.Namespace, pullRequests, mimickedJob, inject, nil, initialPullSpecOverride, basePullSpecOverride, prpqr.Spec.PayloadOverrides.ImageTagOverrides, jobSpec.ShardCount, jobSpec.ShardIndex)
			if err != nil {
				logger.WithError(err).Error("Failed to generate prowjob")
				statuses[mimickedJob] = &v1.PullRequestPayloadJobStatus{
					ReleaseJobName: mimickedJob,
					Status: prowv1.ProwJobStatus{
						State:       prowv1.ErrorState,
						Description: fmt.Errorf("failed to generate prowjob: %w", err).Error(),
					},
				}
				continue
			}
			prowjobsToCreate = append(prowjobsToCreate, prowjob)
		}

		for _, prowjob := range prowjobsToCreate {
			logger.WithField("job", prowjob.Spec.Job).Info("Creating prowjob...")
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
			retrievedJob := prowv1.ProwJob{}
			if err := wait.Poll(second/10, r.jobTriggerWaitDuration, func() (bool, error) {
				if err := r.client.Get(ctx, key, &retrievedJob); err != nil {
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
				ProwJob:        retrievedJob.Name,
				Status:         retrievedJob.Status,
			}
		}
	}
}

func (r *reconciler) abortJobs(ctx context.Context,
	logger *logrus.Entry,
	prpqr *v1.PullRequestPayloadQualificationRun,
	existingProwjobs *prowv1.ProwJobList,
	statuses map[string]*v1.PullRequestPayloadJobStatus,
) {
	if !hasDependantProwJobsFinalizer(&prpqr.ObjectMeta) {
		return
	}

	isAggregator := func(job *prowv1.ProwJob) (string, bool) {
		labels := job.Labels
		if labels == nil {
			return "", false
		}
		label, exists := labels[aggregationIDLabel]
		return label, exists
	}

	abort := func(ctx context.Context, logger *logrus.Entry, job *prowv1.ProwJob) {
		if job.Complete() || !pjstatussyncer.IsActiveState(job.Status.State) {
			return
		}

		logger.Info("Aborting prowjob...")
		job.Status.State = prowv1.AbortedState
		if err := r.client.Update(ctx, job); err != nil {
			logger.WithError(err).Error("Failed to abort")
		}
	}

	prowJobsByName := make(map[string]*prowv1.ProwJob)
	for i := range existingProwjobs.Items {
		pj := &existingProwjobs.Items[i]
		prowJobsByName[pj.Name] = pj
	}

	for i := range prpqr.Status.Jobs {
		jobStatus := &prpqr.Status.Jobs[i]
		// Fill statuses so that reconcilition can proberly be executed
		statuses[jobStatus.ReleaseJobName] = jobStatus

		logger = logger.WithField("prowjob", jobStatus.ProwJob)
		job, exists := prowJobsByName[jobStatus.ProwJob]
		if !exists {
			logger.Warn("Job not found")
			continue
		}

		job = job.DeepCopy()
		logger = logger.WithField("job", job.Spec.Job)
		abort(ctx, logger, job)

		if aggregationId, ok := isAggregator(job); ok {
			logger.Info("Aborting aggregated prowjobs...")

			var aggregatedProwjobs prowv1.ProwJobList
			if err := r.client.List(ctx, &aggregatedProwjobs, ctrlruntimeclient.MatchingLabels{aggregationIDLabel: aggregationId}); err != nil {
				logger.WithError(err).Error("Failed to list aggregated jobs")
				continue
			}

			for j := range aggregatedProwjobs.Items {
				job = (&aggregatedProwjobs.Items[j]).DeepCopy()
				logger = logger.WithFields(logrus.Fields{"aggregated-prowjob": job.Name, "aggregated-job": job.Spec.Job})
				abort(ctx, logger, job)
			}
		}
	}
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

	var atLeastOneActive bool
	theirs.Status.Jobs = []v1.PullRequestPayloadJobStatus{}
	for _, spec := range theirs.Spec.Jobs.Jobs {
		jobName := spec.JobName(jobconfig.PeriodicPrefix)
		if spec.AggregatedCount > 0 {
			jobName = fmt.Sprintf("aggregator-%s", spec.JobName(jobconfig.PeriodicPrefix))
		}

		our := ourStatuses[jobName]
		their := statusByJobName[jobName]
		reconciled := reconcileJobStatus(jobName, their, our)
		theirs.Status.Jobs = append(theirs.Status.Jobs, reconciled)
		if !atLeastOneActive {
			atLeastOneActive = pjstatussyncer.IsActiveState(reconciled.Status.State)
		}
	}

	manageDependentProwJobsFinalizer(atLeastOneActive, &theirs.ObjectMeta)
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
	// sha224 for k8s 63 characters name limitaton
	hasher := sha256.New224()
	// sha256 Write never returns error
	_, _ = hasher.Write([]byte(name))
	return hex.EncodeToString(hasher.Sum(nil))
}

func resolveCiopConfig(rc injectingResolverClient, baseCiop *cioperatorapi.Metadata, inject *cioperatorapi.MetadataWithTest) (*cioperatorapi.ReleaseBuildConfiguration, error) {
	ciopConfig, err := rc.ConfigWithTest(baseCiop, inject)
	if err != nil {
		return nil, fmt.Errorf("failed to get config from resolver: %w", err)
	}

	return ciopConfig, nil
}

type aggregatedOptions struct {
	labels          map[string]string
	aggregatedIndex int
	releaseJobName  string
}

func (r *reconciler) generateProwjob(ciopConfig *cioperatorapi.ReleaseBuildConfiguration,
	baseCiop *cioperatorapi.Metadata,
	prpqrName, prpqrNamespace string,
	prs []v1.PullRequestUnderTest,
	mimickedJob string,
	inject *cioperatorapi.MetadataWithTest,
	aggregatedOptions *aggregatedOptions,
	initialPayloadPullspec, latestPayloadPullspec string,
	imageTagOverrides []v1.ImageTagOverride,
	shardCount, shardIndex int,
) (*prowv1.ProwJob, error) {
	fakeProwgenInfo := &prowgen.ProwgenInfo{Metadata: *baseCiop}

	annotations := map[string]string{
		releaseJobNameAnnotation: mimickedJob,
	}
	labels := map[string]string{
		releaseJobNameLabel: jobNameHash(mimickedJob),
	}

	var aggregateIndex *int
	if aggregatedOptions != nil {
		if aggregatedOptions.labels != nil {
			for k, v := range aggregatedOptions.labels {
				labels[k] = v
			}
		}
		annotations = map[string]string{
			releaseJobNameAnnotation: aggregatedOptions.releaseJobName,
		}
		aggregateIndex = &aggregatedOptions.aggregatedIndex
	} else {
		labels[v1.PullRequestPayloadQualificationRunLabel] = prpqrName
	}

	hashInput := prowgen.CustomHashInput(prpqrName)
	var periodic *prowconfig.Periodic
	for i := range ciopConfig.Tests {
		test := ciopConfig.Tests[i]
		if test.As != inject.Test {
			continue
		}
		if len(prs) > 1 {
			if test.Timeout == nil {
				test.Timeout = &prowv1.Duration{Duration: r.defaultMultiRefJobTimeout}
			} else if test.Timeout.Duration < r.defaultMultiRefJobTimeout {
				test.Timeout.Duration = r.defaultMultiRefJobTimeout
			}
		}
		jobBaseGen := prowgen.NewProwJobBaseBuilderForTest(ciopConfig, fakeProwgenInfo, prowgen.NewCiOperatorPodSpecGenerator(), test)
		jobBaseGen.PodSpec.Add(prowgen.InjectTestFrom(inject))
		if latestPayloadPullspec != "" {
			jobBaseGen.PodSpec.Add(prowgen.ReleaseLatest(latestPayloadPullspec))
		}
		if initialPayloadPullspec != "" {
			jobBaseGen.PodSpec.Add(prowgen.ReleaseInitial(initialPayloadPullspec))
		}
		for _, ito := range imageTagOverrides {
			jobBaseGen.PodSpec.Add(prowgen.OverrideImage(ito.Name, ito.Image))
		}
		if aggregateIndex != nil {
			jobBaseGen.PodSpec.Add(prowgen.TargetAdditionalSuffix(strconv.Itoa(*aggregateIndex)))
		}
		if shardCount > 1 {
			jobBaseGen.PodSpec.Add(prowgen.ShardArgs(shardCount, shardIndex))
		}

		// Avoid sharing when we run the same job multiple times.
		// PRPQR name should be safe to use as a discriminating input, because
		// there should never be more than one execution of a specific job per
		// PRPQR (until aggregated jobs, but for them we'll have a sequence index)
		jobBaseGen.PodSpec.Add(hashInput)

		baseTestName := inject.JobName(jobconfig.PeriodicPrefix)
		if shardCount > 1 {
			baseTestName = fmt.Sprintf("%s-%dof%d", baseTestName, shardIndex, shardCount)
		}
		cluster, err := r.clusterForJob(baseTestName)
		if err != nil {
			return nil, fmt.Errorf("failed to get cluster for job %s: %w", baseTestName, err)
		}
		jobBaseGen.Cluster(cioperatorapi.Cluster(cluster))

		periodic = prowgen.GeneratePeriodicForTest(jobBaseGen, fakeProwgenInfo, prowgen.FromConfigSpec(ciopConfig), func(options *prowgen.GeneratePeriodicOptions) {
			options.Cron = "@yearly"
		})
		periodic.Name = generateJobNameToSubmit(inject, prs, shardCount, shardIndex)

		if periodic.DecorationConfig == nil {
			periodic.DecorationConfig = &prowv1.DecorationConfig{}
		}
		periodic.DecorationConfig.Timeout = &prowv1.Duration{Duration: r.defaultAggregatorJobTimeout}
		break
	}
	// We did not find the injected test: this is a bug
	if periodic == nil {
		return nil, fmt.Errorf("BUG: test '%s' not found in injected config", inject.Test)
	}

	prsByRepo := make(map[string][]v1.PullRequestUnderTest)
	for _, pr := range prs {
		orgRepo := fmt.Sprintf("%s/%s", pr.Org, pr.Repo)
		prsByRepo[orgRepo] = append(prsByRepo[orgRepo], pr)
	}
	// We need to iterate through the prsByRepo map in a deterministic order for testing purposes
	var orgRepos []string
	for orgRepo := range prsByRepo {
		orgRepos = append(orgRepos, orgRepo)
	}
	sort.Slice(orgRepos, func(i, j int) bool {
		return orgRepos[i] < orgRepos[j]
	})
	var refs []prowv1.Refs
	for _, orgRepo := range orgRepos {
		prsForRepo := prsByRepo[orgRepo]
		primaryPR := prsForRepo[0] // Common info can be obtained from the first pr in the list
		ref := prowv1.Refs{
			Org:  primaryPR.Org,
			Repo: primaryPR.Repo,
			// TODO(muller): All these commented-out fields need to be propagated via the PRPQR spec
			// We do not need them now but we should eventually wire them through
			// RepoLink:  pr.Base.Repo.HTMLURL,
			BaseRef: primaryPR.BaseRef,
			BaseSHA: primaryPR.BaseSHA,
			// BaseLink:  fmt.Sprintf("%s/commit/%s", pr.Base.Repo.HTMLURL, pr.BaseSHA),
			PathAlias: ciopConfig.DeterminePathAlias(primaryPR.Org, primaryPR.Repo),
		}

		var pulls []prowv1.Pull
		for _, pr := range prsForRepo {
			if pr.PullRequest != nil {
				pulls = append(pulls, prowv1.Pull{
					Number: pr.PullRequest.Number,
					Author: pr.PullRequest.Author,
					SHA:    pr.PullRequest.SHA,
					Title:  pr.PullRequest.Title,
					// Link:       pr.HTMLURL,
					// AuthorLink: pr.User.HTMLURL,
					// CommitLink: fmt.Sprintf("%s/pull/%d/commits/%s", pr.Base.Repo.HTMLURL, pr.Number, pr.Head.SHA),
				})
			}
		}
		ref.Pulls = pulls

		refs = append(refs, ref)
	}

	// If there are no refs, we are not testing against PR content, and can determine them from the injected test
	if len(refs) == 0 {
		refs = append(refs, prowv1.Refs{
			Org:     baseCiop.Org,
			Repo:    baseCiop.Repo,
			BaseRef: baseCiop.Branch,
		})
	}
	periodic.ExtraRefs = refs

	if err := r.prowConfigGetter.Defaulter().DefaultPeriodic(periodic); err != nil {
		return nil, fmt.Errorf("failed to default the ProwJob: %w", err)
	}

	pj := pjutil.NewProwJob(pjutil.PeriodicSpec(*periodic), labels, annotations)
	pj.Namespace = prpqrNamespace

	return &pj, nil
}

func (r *reconciler) clusterForJob(jobName string) (string, error) {
	if time.Now().Add(time.Minute * -15).After(r.jobClusterCache.lastCleared) {
		r.jobClusterCache.lastCleared = time.Now()
		r.jobClusterCache.clusterForJob = make(map[string]string)
	}
	if cluster, ok := r.jobClusterCache.clusterForJob[jobName]; ok {
		return cluster, nil
	}

	cluster, err := r.dispatcherClient.ClusterForJob(jobName)
	if err != nil {
		return "", fmt.Errorf("could not determine cluster for job %s: %w", jobName, err)
	}
	r.jobClusterCache.clusterForJob[jobName] = cluster

	return cluster, nil
}

func metadataFromPullRequestsUnderTest(prs []v1.PullRequestUnderTest) *cioperatorapi.Metadata {
	var orgs, repos, branches []string
	for _, pr := range prs {
		orgs = append(orgs, pr.Org)
		repos = append(repos, pr.Repo)
		branches = append(branches, pr.BaseRef)
	}
	return &cioperatorapi.Metadata{
		Org:    strings.Join(orgs, ","),
		Repo:   strings.Join(repos, ","),
		Branch: strings.Join(branches, ","),
	}
}

func (r *reconciler) generateAggregatedProwjobs(uid string, ciopConfig *cioperatorapi.ReleaseBuildConfiguration, baseCiop *cioperatorapi.Metadata, prpqrName, prpqrNamespace string, spec *v1.ReleaseJobSpec, prs []v1.PullRequestUnderTest, inject *cioperatorapi.MetadataWithTest, shardCount, shardIndex int) ([]*prowv1.ProwJob, error) {
	var ret []*prowv1.ProwJob

	for i := 0; i < spec.AggregatedCount; i++ {
		opts := &aggregatedOptions{
			labels:          map[string]string{aggregationIDLabel: uid},
			aggregatedIndex: i,
			releaseJobName:  spec.JobName(jobconfig.PeriodicPrefix),
		}
		jobName := fmt.Sprintf("%s-%d", spec.JobName(jobconfig.PeriodicPrefix), i)

		pj, err := r.generateProwjob(ciopConfig, baseCiop, prpqrName, prpqrNamespace, prs, jobName, inject, opts, "", "", nil, shardCount, shardIndex)
		if err != nil {
			return nil, fmt.Errorf("failed to create prowjob: %w", err)
		}

		ret = append(ret, pj)
	}

	return ret, nil
}

func generateAggregatorJob(baseCiop *cioperatorapi.Metadata, uid, aggregatorJobName, jobName, prpqrName, prpqrNamespace string, getCfg prowConfigGetter, startTime time.Time, submitted string, aggregatedJobTimeout time.Duration) (*prowv1.ProwJob, error) {
	ciopConfig := &cioperatorapi.ReleaseBuildConfiguration{
		Metadata: *baseCiop,
		Tests: []cioperatorapi.TestStepConfiguration{
			{
				As: "release-analysis-prpqr-aggregator",
				MultiStageTestConfiguration: &cioperatorapi.MultiStageTestConfiguration{
					Environment: map[string]string{
						"GOOGLE_SA_CREDENTIAL_FILE": "/var/run/secrets/google-serviceaccount-credentials.json",
						"VERIFICATION_JOB_NAME":     jobName,
						"JOB_START_TIME":            startTime.Format(time.RFC3339),
						"AGGREGATION_ID":            uid,
						"WORKING_DIR":               "$(ARTIFACT_DIR)/release-analysis-aggregator",
						"EXPLICIT_GCS_PREFIX":       fmt.Sprintf("logs/%s", submitted),
					},
					Test: []cioperatorapi.TestStep{
						{
							Reference: &[]string{"openshift-release-analysis-prpqr-aggregator"}[0],
						},
					},
				},
			},
		},
		Resources: map[string]cioperatorapi.ResourceRequirements{
			"*": {
				Requests: map[string]string{"cpu": "100m", "memory": "200Mi"},
				Limits:   map[string]string{"memory": "6Gi"},
			},
		},
	}

	unresolvedConfigRaw, err := yaml.Marshal(ciopConfig)
	if err != nil {
		return nil, fmt.Errorf("couldn't marshal ci-operator config")
	}

	jobBaseGen := prowgen.NewProwJobBaseBuilderForTest(ciopConfig, &prowgen.ProwgenInfo{}, prowgen.NewCiOperatorPodSpecGenerator(), ciopConfig.Tests[0])

	periodic := prowgen.GeneratePeriodicForTest(jobBaseGen, &prowgen.ProwgenInfo{}, prowgen.FromConfigSpec(ciopConfig), func(options *prowgen.GeneratePeriodicOptions) {
		options.Cron = "@yearly"
	})
	periodic.Name = aggregatorJobName

	// Aggregator jobs need more time to finish than the jobs they are aggregating. The default job timeout in CI is set to 4h
	periodic.DecorationConfig.Timeout = &prowv1.Duration{Duration: aggregatedJobTimeout}

	// The aggregator job doesn't need to clone any repository.
	periodic.ExtraRefs = nil

	periodic.Spec.Containers[0].Env = append(periodic.Spec.Containers[0].Env, corev1.EnvVar{Name: "UNRESOLVED_CONFIG", Value: string(unresolvedConfigRaw)})

	if err := getCfg.Defaulter().DefaultPeriodic(periodic); err != nil {
		return nil, fmt.Errorf("failed to default the ProwJob: %w", err)
	}

	labels := map[string]string{aggregationIDLabel: uid, v1.PullRequestPayloadQualificationRunLabel: prpqrName}
	annotations := map[string]string{releaseJobNameAnnotation: jobNameHash(aggregatorJobName)}

	cfg := getCfg.Config()
	pj := pjutil.NewProwJob(pjutil.PeriodicSpec(*periodic), labels, annotations, pjutil.RequireScheduling(cfg.Scheduler.Enabled))
	pj.Namespace = prpqrNamespace

	return &pj, nil
}

func generateJobNameToSubmit(inject *cioperatorapi.MetadataWithTest, prs []v1.PullRequestUnderTest, shardCount, shardIndex int) string {
	var refs string
	for i, pr := range prs {
		if i > 0 {
			refs += "-"
		}
		refs += fmt.Sprintf("%s-%s", pr.Org, pr.Repo)
		if pr.PullRequest != nil {
			refs += fmt.Sprintf("-%d", pr.PullRequest.Number)
		}
	}

	//If the refs are empty, we are not testing against any PRs
	if refs == "" {
		refs = "no-included-prs"
	}

	var variant string
	if inject.Variant != "" {
		variant = fmt.Sprintf("-%s", inject.Variant)
	}

	var shardSuffix string
	if shardCount > 1 {
		shardSuffix = fmt.Sprintf("-%dof%d", shardIndex, shardCount)
	}

	return fmt.Sprintf("%s%s-%s%s", refs, variant, inject.Test, shardSuffix)
}

// manageDependentProwJobsFinalizer adds a finalizer if the prpqr has at least one running job,
// remove otherwise.
func manageDependentProwJobsFinalizer(atLeastOneJobRunning bool, objMeta *metav1.ObjectMeta) {
	hasFinalizer := hasDependantProwJobsFinalizer(objMeta)
	if !atLeastOneJobRunning && hasFinalizer {
		newFinalizers := make([]string, len(objMeta.Finalizers)-1)
		for _, f := range objMeta.Finalizers {
			if f != dependentProwJobsFinalizer {
				newFinalizers = append(newFinalizers, f)
			}
		}
		objMeta.Finalizers = newFinalizers
	} else if atLeastOneJobRunning && !hasFinalizer {
		objMeta.Finalizers = append(objMeta.Finalizers, dependentProwJobsFinalizer)
	}
}

func hasDependantProwJobsFinalizer(objMeta *metav1.ObjectMeta) bool {
	for _, f := range objMeta.Finalizers {
		if f == dependentProwJobsFinalizer {
			return true
		}
	}
	return false
}
