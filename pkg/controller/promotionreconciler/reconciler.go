package promotionreconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
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
	DryRun                bool
	CIOperatorConfigAgent agents.ConfigAgent
	ConfigGetter          config.Getter
	GitHubClient          github.Client
	// The registryManager is set up to talk to the cluster
	// that contains our imageRegistry. This cluster is
	// most likely not the one the normal manager talks to.
	RegistryManager controllerruntime.Manager
}

const ControllerName = "promotionreconciler"

func AddToManager(mgr controllerruntime.Manager, opts Options) error {
	// Pre-Allocate the Image informer rather than letting it allocate on demand, because
	// starting the watch takes very long (~2 minutes) and having that delay added to our
	// first (# worker) reconciles skews the workqueue duration metric bigtimes.
	if _, err := opts.RegistryManager.GetCache().GetInformer(context.TODO(), &imagev1.Image{}); err != nil {
		return fmt.Errorf("failed to get informer for image: %w", err)
	}

	if err := opts.CIOperatorConfigAgent.AddIndex(configIndexName, configIndexFn); err != nil {
		return fmt.Errorf("failed to add indexer to config-agent: %w", err)
	}

	prowJobEnqueuer, err := prowjobreconciler.AddToManager(mgr, opts.ConfigGetter, opts.DryRun)
	if err != nil {
		return fmt.Errorf("failed to construct prowjobreconciler: %w", err)
	}

	log := logrus.WithField("controller", ControllerName)
	r := &reconciler{
		ctx:    context.Background(),
		log:    log,
		client: imagestreamtagwrapper.MustNew(opts.RegistryManager.GetClient(), opts.RegistryManager.GetCache()),
		releaseBuildConfigs: func(identifier string) ([]*cioperatorapi.ReleaseBuildConfiguration, error) {
			return opts.CIOperatorConfigAgent.GetFromIndex(configIndexName, identifier)
		},
		gitHubClient: opts.GitHubClient,
		enqueueJob:   prowJobEnqueuer,
	}
	c, err := controller.New(ControllerName, opts.RegistryManager, controller.Options{
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

// ciOperatorConfigGetter is needed to for testing. In non-test scenarios it is implemented
// by using an index on the agents.ConfigAgent
type ciOperatorConfigGetter func(identifier string) ([]*cioperatorapi.ReleaseBuildConfiguration, error)

type githubClient interface {
	GetRef(org, repo, ref string) (string, error)
}

type reconciler struct {
	ctx                 context.Context
	log                 *logrus.Entry
	client              ctrlruntimeclient.Client
	releaseBuildConfigs ciOperatorConfigGetter
	gitHubClient        githubClient
	enqueueJob          prowjobreconciler.Enqueuer
}

func (r *reconciler) Reconcile(req controllerruntime.Request) (controllerruntime.Result, error) {
	log := r.log.WithField("name", req.Name).WithField("namespace", req.Namespace)
	log.Trace("Starting reconciliation")
	startTime := time.Now()
	defer func() { log.WithField("duration", time.Since(startTime)).Trace("Finished reconciliation") }()

	err := r.reconcile(req, log)
	if err != nil {
		log := log.WithError(err)
		// Degrade terminal errors to debug, they most lilely just mean a given imageStreamTag wasn't built
		// via ci operator.
		if controllerutil.IsTerminal(err) {
			log.Debug("Reconciliation failed")
		} else {
			log.Error("Reconciliation failed")
		}
	}

	return controllerruntime.Result{}, controllerutil.SwallowIfTerminal(err)
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
		log.Trace("No promotionConfig found")
		return nil
	}

	istCommit, err := commitForIST(ist)
	if err != nil {
		return controllerutil.TerminalError(fmt.Errorf("failed to get commit for imageStreamTag: %w", err))
	}

	currentHEAD, found, err := r.currentHEADForBranch(ciOPConfig.Metadata, log)
	if err != nil {
		return fmt.Errorf("failed to get current git head for imageStreamTag: %w", err)
	}
	if !found {
		return controllerutil.TerminalError(fmt.Errorf("got 404 for %s/%s/%s from github, this likely means the repo or branch got deleted or we are not allowed to access it", ciOPConfig.Metadata.Org, ciOPConfig.Metadata.Repo, ciOPConfig.Metadata.Branch))
	}
	// ImageStreamTag is current, nothing to do
	if currentHEAD == istCommit {
		return nil
	}
	log = log.WithField("org", ciOPConfig.Metadata.Org).WithField("repo", ciOPConfig.Metadata.Repo).WithField("branch", ciOPConfig.Metadata.Branch).WithField("currentHEAD", currentHEAD)

	log.Info("Requesting prowjob creation")
	r.enqueueJob(prowjobreconciler.OrgRepoBranchCommit{
		Org:    ciOPConfig.Metadata.Org,
		Repo:   ciOPConfig.Metadata.Repo,
		Branch: ciOPConfig.Metadata.Branch,
		Commit: currentHEAD,
	})
	return nil
}

func (r *reconciler) promotionConfig(ist *imagev1.ImageStreamTag) (*cioperatorapi.ReleaseBuildConfiguration, error) {
	results, err := r.releaseBuildConfigs(configIndexKeyForIST(ist))
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

func commitForIST(ist *imagev1.ImageStreamTag) (string, error) {
	metadata := &docker10.DockerImage{}
	if err := json.Unmarshal(ist.Image.DockerImageMetadata.Raw, metadata); err != nil {
		return "", fmt.Errorf("failed to unmarshal imagestream.image.dockerImageMetadata: %w", err)
	}

	commit := metadata.Config.Labels["io.openshift.build.commit.id"]
	if commit == "" {
		return "", controllerutil.TerminalError(errors.New("ImageStreamTag has no `io.openshift.build.commit.id` label, can't find out source commit"))
	}

	return commit, nil
}

func (r *reconciler) currentHEADForBranch(metadata cioperatorapi.Metadata, log *logrus.Entry) (string, bool, error) {
	// We attempted for some time to use the gitClient for this, but we do so many reconciliations that
	// it results in a massive performance issues that can easely kill the developers laptop.
	ref, err := r.gitHubClient.GetRef(metadata.Org, metadata.Repo, "heads/"+metadata.Branch)
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
