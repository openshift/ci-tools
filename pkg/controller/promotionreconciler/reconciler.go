package promotionreconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/controller/promotionreconciler/prowjobreconciler"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/steps/release"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagwrapper"
)

type Options struct {
	DryRun                     bool
	CIOperatorConfigAgent      agents.ConfigAgent
	ConfigGetter               config.Getter
	GitHubClient               github.Client
	IgnoredGitHubOrganizations []string
	// The registryManager is set up to talk to the cluster
	// that contains our imageRegistry. This cluster is
	// most likely not the one the normal manager talks to.
	RegistryManager controllerruntime.Manager
}

const controllerName = "promotionreconciler"

func AddToManager(mgr controllerruntime.Manager, opts Options) error {
	if err := imagev1.AddToScheme(mgr.GetScheme()); err != nil {
		return fmt.Errorf("failed to add imagev1 to scheme: %w", err)
	}
	if err := prowv1.AddToScheme(mgr.GetScheme()); err != nil {
		return fmt.Errorf("failed to add prowv1 to scheme: %w", err)
	}
	// Pre-Allocate the Image informer rather than letting it allocate on demand, because
	// starting the watch takes very long (~2 minutes) and having that delay added to our
	// first (# worker) reconciles skews the workqueue duration metric bigtimes.
	if _, err := opts.RegistryManager.GetCache().GetInformer(&imagev1.Image{}); err != nil {
		return fmt.Errorf("failed to get informer for image: %w", err)
	}

	if err := opts.CIOperatorConfigAgent.AddIndex(configIndexName, configIndexFn); err != nil {
		return fmt.Errorf("failed to add indexer to config-agent: %w", err)
	}

	prowJobEnqueuer, err := prowjobreconciler.AddToManager(mgr, opts.ConfigGetter, opts.DryRun)
	if err != nil {
		return fmt.Errorf("failed to construct prowjobreconciler: %w", err)
	}

	log := logrus.WithField("controller", controllerName)
	r := &reconciler{
		ctx:                        context.Background(),
		log:                        log,
		client:                     imagestreamtagwrapper.New(opts.RegistryManager.GetClient()),
		releaseBuildConfigs:        opts.CIOperatorConfigAgent,
		gitHubClient:               opts.GitHubClient,
		ignoredGitHubOrganizations: sets.NewString(opts.IgnoredGitHubOrganizations...),
		enqueueJob:                 prowJobEnqueuer,
	}
	c, err := controller.New(controllerName, opts.RegistryManager, controller.Options{
		// Since we watch ImageStreams and not ImageStreamTags as the latter do not support
		// watch, we create a lot more events the needed. In order to decrease load, we coalesce
		// and delay all requests after a successful reconciliation for up to an hour.
		Reconciler: controllerutil.NewReconcileRequestCoalescer(r, 10*time.Minute),
		// We currently have 50k ImageStreamTags in the OCP namespace and need to periodically reconcile all of them,
		// so don't be stingy with the workers
		MaxConcurrentReconciles: 100,
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
					logrus.WithField("type", fmt.Sprintf("%T", mo.Object)).Error("Got object that was not an ImageStram")
					return nil
				}
				var requests []reconcile.Request
				for _, imageStreamTag := range imageStream.Spec.Tags {
					// Not sure why this happens but seems to be a thing
					if imageStreamTag.Name == "" {
						continue
					}
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
	gitHubClient               github.Client
	ignoredGitHubOrganizations sets.String
	enqueueJob                 prowjobreconciler.Enqueuer
}

func (r *reconciler) Reconcile(req controllerruntime.Request) (controllerruntime.Result, error) {
	log := r.log.WithField("name", req.Name).WithField("namespace", req.Namespace)
	log.Trace("Starting reconciliation")
	startTime := time.Now()
	defer func() { log.WithField("duration", time.Since(startTime)).Trace("Finished reconciliation") }()

	err := r.reconcile(req, log)
	isNonRetriable := errors.Is(err, nonRetriableError{})
	if err != nil {
		log := log.WithError(err).WithField("not-retriable", isNonRetriable)
		// Degrade non-retriable errors to debug, they most lilely just mean a given imageStreamTag wasn't built
		// via ci operator.
		if isNonRetriable {
			log.Debug("Reconciliation failed")
		} else {
			log.Error("Reconciliation failed")
		}
	}

	if !isNonRetriable {
		// Swallow non-retriable errors to avoid putting the item back into the workqueue
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
		log.Debug("No promotionConfig found")
		return nil
	}

	istRef, err := refForIST(ist)
	if err != nil {
		return nonRetriableError{fmt.Errorf("failed to get ref for imageStreamTag: %w", err)}
	}
	log = log.WithField("org", istRef.org).WithField("repo", istRef.repo).WithField("branch", istRef.branch)
	if r.ignoredGitHubOrganizations.Has(istRef.org) {
		log.WithField("github-organization", istRef.org).Debug("Ignoring ImageStreamTag because its source organization is configured to be ignored")
		return nil
	}

	currentHEAD, err := r.currentHEADForBranch(istRef, log)
	if err != nil {
		return fmt.Errorf("failed to get current git head for imageStreamTag: %w", err)
	}
	// ImageStreamTag is current, nothing to do
	if currentHEAD == istRef.commit {
		return nil
	}

	r.enqueueJob(prowjobreconciler.OrgRepoBranchCommit{
		Org:    istRef.org,
		Repo:   istRef.repo,
		Branch: istRef.branch,
		Commit: currentHEAD,
	})
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
		repo:   strings.TrimSuffix(splitSourceLocation[1], ".git"),
		branch: branch,
		commit: commit,
	}, nil
}

func (r *reconciler) currentHEADForBranch(br *branchReference, log *logrus.Entry) (string, error) {
	// We attempted for some time to use the gitClient for this, but we do so many reconciliations that
	// it results in a massive performance issues that can easely kill the developers laptop.
	ref, err := r.gitHubClient.GetRef(br.org, br.repo, "heads/"+br.branch)
	if err != nil {
		return "", fmt.Errorf("failed to get ref: %w", err)
	}
	return ref, nil
}

// nonRetriableError indicates that we encountered an error
// that we know wont resolve itself via retrying. We use it
// to still bubble the message up but swallow it after we
// logged it so we don't waste cycles on useless work.
type nonRetriableError struct {
	err error
}

// errors.Is compares via == which means if our .err holds something,
// we never match.
func (nonRetriableError) Is(target error) bool {
	_, ok := target.(nonRetriableError)
	return ok
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
