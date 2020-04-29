package imagestreamtagreconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/git"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/pjutil"
	"sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/prowgen"
	"github.com/openshift/ci-tools/pkg/steps/release"
)

type Options struct {
	DryRun                bool
	CIOperatorConfigAgent agents.ConfigAgent
	ProwJobNamespace      string
	GitClient             *git.Client
}

func AddToManager(mgr controllerruntime.Manager, opts Options) error {
	if err := imagev1.AddToScheme(mgr.GetScheme()); err != nil {
		return fmt.Errorf("failed to add imagev1 to scheme: %w", err)
	}
	if err := prowv1.AddToScheme(mgr.GetScheme()); err != nil {
		return fmt.Errorf("failed to add prowv1 to scheme: %w", err)
	}

	r := &reconciler{
		ctx:              context.Background(),
		log:              logrus.WithField("controller", "imageStreamTagReconciler"),
		client:           mgr.GetClient(),
		dryRun:           opts.DryRun,
		ciOPConfigAgent:  opts.CIOperatorConfigAgent,
		prowJobNamespace: opts.ProwJobNamespace,
		gitClient:        opts.GitClient,
	}
	c, err := controller.New(
		"imageStreamTagReconciler",
		mgr,
		// We currently have 50k ImageStreamTags in the OCP namespace and need to periodically reconcile all of them,
		// so don't be stingy with the workers
		controller.Options{Reconciler: r, MaxConcurrentReconciles: 100})
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

	return nil
}

type reconciler struct {
	ctx              context.Context
	log              *logrus.Entry
	client           ctrlruntimeclient.Client
	dryRun           bool
	ciOPConfigAgent  agents.ConfigAgent
	prowJobNamespace string
	gitClient        *git.Client
}

func (r *reconciler) Reconcile(req controllerruntime.Request) (controllerruntime.Result, error) {
	log := r.log.WithField("name", req.Name).WithField("namespace", req.Namespace)
	log.Info("Starting reconciliation")
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

	ciOPConfig, publishedViaCIOperator := r.isPublishedFromCIOperatorRepo(log, ist)
	if !publishedViaCIOperator {
		// We don't know how to build this
		return nil
	}

	istRef, err := refForIST(ist)
	if err != nil {
		return fmt.Errorf("failed to get ref for imageStreamTag: %w", err)
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
	if istHasBuildRunning || err != nil {
		return wrapErrIfNonNil("failed to check if there is a running build for imageStreamTag", err)
	}

	if err := r.createBuildForIST(buildJob, currentHEAD, istRef); err != nil {
		return fmt.Errorf("failed to create build for imageStreamTagL %w", err)
	}

	return nil
}

func (r *reconciler) isPublishedFromCIOperatorRepo(log *logrus.Entry, ist *imagev1.ImageStreamTag) (*cioperatorapi.ReleaseBuildConfiguration, bool) {
	// TODO: Index this, what we do here is incredibly slow. And remember, we do it 50k times
	// during startup and resync
	for _, config := range r.ciOPConfigAgent.GetAll() {
		for _, istRef := range release.PromotedTags(&config) {
			if ist.Name == istRef.Name+":"+istRef.Name {
				return &config, true
			}
		}
	}

	return nil, false
}

type branchReference struct {
	org    string
	repo   string
	branch string
	commit string
}

func refForIST(ist *imagev1.ImageStreamTag) (*branchReference, error) {
	imageMetadataLabels := struct {
		Labels map[string]string `json:"labels"`
	}{}
	if err := json.Unmarshal(ist.Image.DockerImageMetadata.Raw, &imageMetadataLabels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal imagestream.image.dockerImageMetadata: %w", err)
	}

	branch := imageMetadataLabels.Labels["io.openshift.build.commit.ref"]
	commit := imageMetadataLabels.Labels["io.openshift.build.commit.id"]
	sourceLocation := imageMetadataLabels.Labels["io.openshift.build.source-location"]
	if branch == "" {
		return nil, nre(errors.New("imageStreamTag has no `io.openshift.build.commit.ref` label, can't find out source branch"))
	}
	if commit == "" {
		return nil, nre(errors.New("ImageStreamTag has no `io.openshift.build.commit.id` label, can't find out source commit"))
	}
	if sourceLocation == "" {
		return nil, nre(errors.New("imageStreamTag has no `io.openshift.build.source-location` label, can't find out source repo"))
	}
	sourceLocation = strings.TrimPrefix(sourceLocation, "https://github.com/")
	splitSourceLocation := strings.Split(sourceLocation, "/")
	if n := len(splitSourceLocation); n != 2 {
		return nil, nre(fmt.Errorf("sourceLocation %q split by `/` does not return 2 but %d results, can not find out org/repo", sourceLocation, n))
	}

	return &branchReference{
		org:    splitSourceLocation[0],
		repo:   splitSourceLocation[1],
		branch: branch,
		commit: commit,
	}, nil
}

func (r *reconciler) currentHEADForBranch(br *branchReference) (string, error) {
	repo, err := r.gitClient.Clone(br.org, br.repo)
	if err != nil {
		return "", fmt.Errorf("failed to get git client for %s/%s: %w", br.org, br.repo, err)
	}
	branchHEADRef, err := repo.RevParse(br.branch)
	if err != nil {
		return "", fmt.Errorf("fauld to git rev-parse %s: %w", br.branch, err)
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
	labelSelector := ctrlruntimeclient.MatchingLabels{
		// Label values are capped at 63 characters and the job name frequently exceeds that.
		kube.ProwJobAnnotation: job.Name[0:62],
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
	}), nil, nil)
	prowJob.Namespace = r.prowJobNamespace

	if r.dryRun {
		r.log.Infof("Not creating %s prowjob because dryRun is enabled", prowJob.Spec.Job)
		return nil
	}

	if err := r.client.Create(r.ctx, &prowJob); err != nil {
		return fmt.Errorf("failed to create prowjob: %w", err)
	}

	return nil
}

func wrapErrIfNonNil(wrapping string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(wrapping+" %w", err)
}

func nre(err error) error {
	return nonRetriableError{err: err}
}

type nonRetriableError struct {
	err error
}

func (nre nonRetriableError) Error() string {
	return nre.err.Error()
}
