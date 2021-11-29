package prpqr_reconciler

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
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

	var prpqrMutations []func(prpqr *v1.PullRequestPayloadQualificationRun)
	createdJobs := make(map[string]v1.PullRequestPayloadJobStatus)

	prpqr := &v1.PullRequestPayloadQualificationRun{}
	if err := r.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: req.Namespace, Name: req.Name}, prpqr); err != nil {
		return fmt.Errorf("failed to get the PullRequestPayloadQualificationRun: %s in namespace %s: %w", req.Name, req.Namespace, err)
	}

	baseMetadata := metadataFromPullRequestUnderTest(prpqr.Spec.PullRequest)
	prowjobs, errs := generateProwjobs(r.configResolverClient, r.prowConfigGetter.Config(), baseMetadata, req.Name, req.Namespace, prpqr.Spec.Jobs.Jobs, &prpqr.Spec.PullRequest)
	for job, err := range errs {
		createdJobs[job] = v1.PullRequestPayloadJobStatus{ReleaseJobName: job, Status: prowv1.ProwJobStatus{
			State:       prowv1.ErrorState,
			Description: err.Error(),
		}}
	}
	for releaseJobName, pj := range prowjobs {
		logger = logger.WithFields(logrus.Fields{"name": pj.Name, "namespace": req.Namespace})

		pjList := &prowv1.ProwJobList{}
		if err := r.client.List(ctx, pjList, ctrlruntimeclient.MatchingLabels{v1.PullRequestPayloadQualificationRunLabel: prpqr.Name, releaseJobNameLabel: jobNameHash(releaseJobName)}); err != nil {
			logger.WithError(err).Error("failed to get list of Prowjobs")
			createdJobs[releaseJobName] = v1.PullRequestPayloadJobStatus{ReleaseJobName: releaseJobName, Status: prowv1.ProwJobStatus{
				State:       prowv1.ErrorState,
				Description: fmt.Errorf("failed to list prowjobs: %w", err).Error(),
			}}
			continue
		}

		if len(pjList.Items) > 0 {
			logger.Info("Prowjob already exists...")
			continue
		}

		logger.Info("Creating prowjob...")
		if err := r.client.Create(ctx, &pj); err != nil {
			createdJobs[releaseJobName] = v1.PullRequestPayloadJobStatus{
				ReleaseJobName: releaseJobName,
				Status: prowv1.ProwJobStatus{
					State:       prowv1.ErrorState,
					Description: fmt.Errorf("failed to create prowjob: %w", err).Error(),
				},
			}
			continue
		}

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

		createdJobs[releaseJobName] = v1.PullRequestPayloadJobStatus{ReleaseJobName: releaseJobName, ProwJob: pj.Name, Status: pj.Status}
	}

	prpqrMutations = append(prpqrMutations, func(prpqr *v1.PullRequestPayloadQualificationRun) {
		for _, status := range createdJobs {
			prpqr.Status.Jobs = append(prpqr.Status.Jobs, status)
		}
		prpqr.Status.Conditions = append(prpqr.Status.Conditions, constructCondition(createdJobs))
	})

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		prpqr := &v1.PullRequestPayloadQualificationRun{}
		if err := r.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: req.Namespace, Name: req.Name}, prpqr); err != nil {
			return fmt.Errorf("failed to get the PullRequestPayloadQualificationRun: %w", err)
		}

		for _, mutate := range prpqrMutations {
			mutate(prpqr)
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

func constructCondition(createdJobs map[string]v1.PullRequestPayloadJobStatus) metav1.Condition {
	message := "All jobs triggered successfully"
	reason := conditionAllJobsTriggered
	status := metav1.ConditionTrue

	for _, jobStatus := range createdJobs {
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

func generateProwjobs(rc injectingResolverClient, defaulter periodicDefaulter, baseCiop *api.Metadata, prpqrName, prpqrNamespace string, releaseJobSpec []v1.ReleaseJobSpec, pr *v1.PullRequestUnderTest) (map[string]prowv1.ProwJob, map[string]error) {
	fakeProwgenInfo := &prowgen.ProwgenInfo{Metadata: *baseCiop}

	prowjobs := map[string]prowv1.ProwJob{}
	errs := map[string]error{}

	for _, spec := range releaseJobSpec {
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

		ciopConfig, err := rc.ConfigWithTest(baseCiop, inject)
		if err != nil {
			errs[mimickedJob] = fmt.Errorf("failed to get config from resolver: %w", err)
			continue
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
			errs[mimickedJob] = fmt.Errorf("BUG: test '%s' not found in injected config", inject.Test)
			continue
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

		if err = defaulter.DefaultPeriodic(periodic); err != nil {
			errs[mimickedJob] = fmt.Errorf("failed to default the ProwJob: %w", err)
			continue
		}

		pj := pjutil.NewProwJob(pjutil.PeriodicSpec(*periodic), labels, annotations)
		pj.Namespace = prpqrNamespace

		prowjobs[mimickedJob] = pj
	}
	return prowjobs, errs
}

func metadataFromPullRequestUnderTest(pr v1.PullRequestUnderTest) *api.Metadata {
	return &api.Metadata{Org: pr.Org, Repo: pr.Repo, Branch: pr.BaseRef}
}
