package imagestreamtagreconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	git "k8s.io/test-infra/prow/git/v2"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/pjutil"
	"sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/prowgen"
	"github.com/openshift/ci-tools/pkg/steps/release"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagwrapper"
)

type Options struct {
	DryRun                     bool
	CIOperatorConfigAgent      agents.ConfigAgent
	ProwJobNamespace           string
	GitClientFactory           git.ClientFactory
	IgnoredGitHubOrganizations []string
}

const controllerName = "imageStreamTagReconciler"

func AddToManager(mgr controllerruntime.Manager, opts Options) error {
	if err := imagev1.AddToScheme(mgr.GetScheme()); err != nil {
		return fmt.Errorf("failed to add imagev1 to scheme: %w", err)
	}
	if err := prowv1.AddToScheme(mgr.GetScheme()); err != nil {
		return fmt.Errorf("failed to add prowv1 to scheme: %w", err)
	}

	if err := opts.CIOperatorConfigAgent.AddIndex(configIndexName, configIndexFn); err != nil {
		return fmt.Errorf("failed to add indexer to config-agent: %w", err)
	}

	createdJobsCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: controllerName,
		Name:      "prowjobs_created",
		Help:      "The number of prowjobs the controller created",
	}, []string{"org", "repo", "branch", "success"})
	if err := metrics.Registry.Register(createdJobsCounter); err != nil {
		return fmt.Errorf("failed to register createdJobsCounter: %w", err)
	}

	log := logrus.WithField("controller", controllerName)
	r := &reconciler{
		ctx:                 context.Background(),
		log:                 log,
		client:              imagestreamtagwrapper.New(mgr.GetClient()),
		releaseBuildConfigs: opts.CIOperatorConfigAgent,
		dryRun:              opts.DryRun,
		prowJobNamespace:    opts.ProwJobNamespace,
		gitClientFactory:    opts.GitClientFactory,
		createdProwJobLabels: map[string]string{
			"openshift.io/created-by": controllerName,
		},
		ignoredGitHubOrganizations: sets.NewString(opts.IgnoredGitHubOrganizations...),
		createdJobsCounter:         createdJobsCounter,
	}
	c, err := controller.New(controllerName, mgr, controller.Options{
		// Since we watch ImageStreams and not ImageStreamTags as the latter do not support
		// watch, we create a lot more events the needed. In order to decrease load, we coalesce
		// and delay all requests after a successful reconciliation for up to an hour.
		Reconciler: controllerutil.NewReconcileRequestCoalescer(r, time.Hour),
		// We currently have 50k ImageStreamTags in the OCP namespace and need to periodically reconcile all of them,
		// so don't be stingy with the workers
		MaxConcurrentReconciles: 10,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(
		&source.Kind{Type: &imagev1.ImageStream{}},
		&handler.EnqueueRequestsFromMapFunc{ToRequests: handler.ToRequestsFunc(
			func(mo handler.MapObject) []reconcile.Request {
				imageStream, ok := mo.Object.(*imagev1.ImageStream)
				if !ok {
					logrus.Errorf("got object that was not an imageStream but a %T", mo.Object)
					return nil
				}
				var requests []reconcile.Request
				for _, imageStreamTag := range imageStream.Spec.Tags {
					requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
						Namespace: mo.Meta.GetNamespace(),
						Name:      fmt.Sprintf("%s:%s", mo.Meta.GetName(), imageStreamTag.Name),
					}})
				}
				return requests
			},
		)}); err != nil {
		return fmt.Errorf("failed to create watch for ImageStreams: %w", err)
	}
	r.log.Info("Successfully added reconciler to manager")

	return nil
}

type reconciler struct {
	ctx                        context.Context
	log                        *logrus.Entry
	client                     ctrlruntimeclient.Client
	releaseBuildConfigs        agents.ConfigAgent
	dryRun                     bool
	prowJobNamespace           string
	gitClientFactory           git.ClientFactory
	createdProwJobLabels       map[string]string
	ignoredGitHubOrganizations sets.String
	createdJobsCounter         *prometheus.CounterVec
}

func (r *reconciler) Reconcile(req controllerruntime.Request) (controllerruntime.Result, error) {
	log := r.log.WithField("name", req.Name).WithField("namespace", req.Namespace)
	//	log.Debug("Starting reconciliation")
	//	startTime := time.Now()
	//	defer func() { log.WithField("duration", time.Since(startTime)).Debug("Finished reconciliation") }()

	err := r.reconcile(req, log)
	if err != nil {
		log.WithError(err).Error("Reconciliation failed")
	}

	// Swallow non-retriable errors to avoid putting the item back into the workqueue
	if errors.Is(err, nonRetriableError{}) {
		err = nil
	}
	return controllerruntime.Result{}, err
}

func (r *reconciler) reconcile(req controllerruntime.Request, log *logrus.Entry) error {
	ist := &imagev1.ImageStreamTag{}
	if err := r.client.Get(r.ctx, req.NamespacedName, ist); err != nil {
		// Object got deleted while it was in the workqueue
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get object: %w", err)
	}

	ciOPConfig, err := r.promotionConfig(ist)
	if err != nil {
		return fmt.Errorf("failed to get promotionConfig: %w", err)
	}
	if ciOPConfig == nil {
		// We don't know how to build this
		//log.Debug("No promotionConfig found")
		return nil
	}

	istRef, err := refForIST(ist)
	if err != nil {
		return fmt.Errorf("failed to get ref for imageStreamTag: %w", err)
	}
	log = log.WithField("org", istRef.org).WithField("repo", istRef.repo).WithField("branch", istRef.branch)
	if r.ignoredGitHubOrganizations.Has(istRef.org) {
		log.WithField("github-organization", istRef.org).Debug("Ignoring ImageStreamTag because its source organization is configured to be ignored")
		return nil
	}

	currentHEAD, err := r.currentHEADForBranch(istRef)
	if err != nil {
		return fmt.Errorf("failed to get current git head for imageStreamTag: %w", err)
	}
	// ImageStreamTag is current, nothing to do
	if currentHEAD == istRef.commit {
		return nil
	}

	buildJob, err := r.getPublishJob(istRef, ciOPConfig)
	if err != nil {
		return fmt.Errorf("failed to get buildJob for imageStreamTag: %w", err)
	}

	istHasBuildRunning, err := r.isJobRunningForCommit(buildJob, currentHEAD, istRef)
	if err != nil {
		return fmt.Errorf("failed to check if there is a running build for ImageStramTag: %w", err)
	}
	if istHasBuildRunning {
		// Nothing to do right now. We wont get an event if the build fails but we periodically reconcile
		// which will catch that case.
		return nil
	}

	err = r.createBuildForIST(buildJob, currentHEAD, istRef)
	r.createdJobsCounter.WithLabelValues(istRef.org, istRef.repo, istRef.branch, boolToString(err == nil)).Inc()
	if err != nil {
		return fmt.Errorf("failed to create build for imageStreamTagL %w", err)
	}

	return nil
}

func (r *reconciler) promotionConfig(ist *imagev1.ImageStreamTag) (*cioperatorapi.ReleaseBuildConfiguration, error) {
	results, err := r.releaseBuildConfigs.GetFromIndex(configIndexName, configIndexKeyForIST(ist))
	if err != nil {
		return nil, fmt.Errorf("query index: %w", err)
	}
	switch len(results) {
	case 0:
		return nil, nil
	case 1:
		return results[0], nil
	default:
		// Config might get updated, so do not make this a nonRetriableError
		return nil, fmt.Errorf("found multiple promotion configs for ImageStreamTag. This is likely a configuration error")
	}
}

type branchReference struct {
	org    string
	repo   string
	branch string
	commit string
}

func refForIST(ist *imagev1.ImageStreamTag) (*branchReference, error) {
	metadata := &docker10.DockerImage{}
	if err := json.Unmarshal(ist.Image.DockerImageMetadata.Raw, metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal imagestream.image.dockerImageMetadata: %w", err)
	}

	branch := metadata.Config.Labels["io.openshift.build.commit.ref"]
	commit := metadata.Config.Labels["io.openshift.build.commit.id"]
	sourceLocation := metadata.Config.Labels["io.openshift.build.source-location"]
	if branch == "" {
		return nil, nonRetriableError{errors.New("imageStreamTag has no `io.openshift.build.commit.ref` label, can't find out source branch")}
	}
	if commit == "" {
		return nil, nonRetriableError{errors.New("ImageStreamTag has no `io.openshift.build.commit.id` label, can't find out source commit")}
	}
	if sourceLocation == "" {
		return nil, nonRetriableError{errors.New("imageStreamTag has no `io.openshift.build.source-location` label, can't find out source repo")}
	}
	sourceLocation = strings.TrimPrefix(sourceLocation, "https://github.com/")
	splitSourceLocation := strings.Split(sourceLocation, "/")
	if n := len(splitSourceLocation); n != 2 {
		return nil, nonRetriableError{fmt.Errorf("sourceLocation %q split by `/` does not return 2 but %d results, can not find out org/repo", sourceLocation, n)}
	}

	return &branchReference{
		org:    splitSourceLocation[0],
		repo:   splitSourceLocation[1],
		branch: branch,
		commit: commit,
	}, nil
}

func (r *reconciler) currentHEADForBranch(br *branchReference) (string, error) {
	repo, err := r.gitClientFactory.ClientFor(br.org, br.repo)
	if err != nil {
		return "", fmt.Errorf("failed to get git client for %s/%s: %w", br.org, br.repo, err)
	}
	branchHEADRef, err := repo.ShowRef(br.branch)
	if err != nil {
		return "", fmt.Errorf("failed to git show-ref %s: %w", br.branch, err)
	}

	return branchHEADRef, nil
}

func (r *reconciler) getPublishJob(br *branchReference, ciOPConfig *cioperatorapi.ReleaseBuildConfiguration) (*prowconfig.Postsubmit, error) {
	info := &prowgen.ProwgenInfo{
		Info: config.Info{
			Org:    br.org,
			Repo:   br.repo,
			Branch: br.branch,
		},
	}
	postsubmits := prowgen.GenerateJobs(ciOPConfig, info, jobconfig.Generated).AllStaticPostsubmits(nil)
	if n := len(postsubmits); n != 1 {
		return nil, fmt.Errorf("expected to find exactly one postsubmit, got %d", n)
	}
	return &postsubmits[0], nil
}

func (r *reconciler) isJobRunningForCommit(job *prowconfig.Postsubmit, commitSHA string, istRef *branchReference) (bool, error) {
	jobNameLabelValue := job.Name
	// Label values are capped at 63 characters and the job name frequently exceeds that.
	if len(jobNameLabelValue) > 63 {
		jobNameLabelValue = job.Name[0:62]
	}
	labelSelector := ctrlruntimeclient.MatchingLabels{
		kube.ProwJobAnnotation: jobNameLabelValue,
		kube.OrgLabel:          istRef.org,
		kube.RepoLabel:         istRef.repo,
		kube.ProwJobTypeLabel:  string(prowv1.PostsubmitJob),
	}
	namespaceSelector := ctrlruntimeclient.InNamespace(r.prowJobNamespace)

	prowJobs := &prowv1.ProwJobList{}
	if err := r.client.List(r.ctx, prowJobs, labelSelector, namespaceSelector); err != nil {
		return false, fmt.Errorf("failed to list prowjobs: %w", err)
	}

	for _, job := range prowJobs.Items {
		if job.Complete() {
			continue
		}
		if job.Spec.Refs != nil && job.Spec.Refs.BaseRef == commitSHA {
			return true, nil
		}
	}

	return false, nil
}

func (r *reconciler) createBuildForIST(job *prowconfig.Postsubmit, headSHA string, istRef *branchReference) error {
	prowJob := pjutil.NewProwJob(pjutil.PostsubmitSpec(*job, prowv1.Refs{
		Org:     istRef.org,
		Repo:    istRef.repo,
		BaseRef: istRef.branch,
		BaseSHA: headSHA,
	}), r.createdProwJobLabels, nil)
	prowJob.Namespace = r.prowJobNamespace

	if r.dryRun {
		serialized, _ := json.Marshal(prowJob)
		r.log.WithField("job_name", prowJob.Spec.Job).WithField("job", string(serialized)).Info("Not creating prowjob because dryRun is enabled")
		return nil
	}

	if err := r.client.Create(r.ctx, &prowJob); err != nil {
		return fmt.Errorf("failed to create prowjob: %w", err)
	}

	return nil
}

// nonRetriableError indicates that we encountered an error
// that we know wont resolve itself via retrying. We use it
// to still bubble the message up but swallow it after we
// logged it so we don't waste cycles on useless work.
type nonRetriableError struct {
	err error
}

func (nre nonRetriableError) Error() string {
	return nre.err.Error()
}

const configIndexName = "release-build-config-by-image-stream-tag"

func configIndexFn(in cioperatorapi.ReleaseBuildConfiguration) []string {
	var result []string
	for _, istRef := range release.PromotedTags(&in) {
		result = append(result, fmt.Sprintf("%s/%s:%s", istRef.Namespace, istRef.Name, istRef.Tag))
	}
	return result
}

func configIndexKeyForIST(ist *imagev1.ImageStreamTag) string {
	return ist.Namespace + "/" + ist.Name
}

func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
