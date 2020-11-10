package promotionreconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/test-infra/prow/github"
	"sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/controller/promotionreconciler/prowjobreconciler"
	"github.com/openshift/ci-tools/pkg/controller/promotionreconciler/prowjobretrigger"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/promotion"
	"github.com/openshift/ci-tools/pkg/steps/release"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagmapper"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagwrapper"
)

type Options struct {
	CIOperatorConfigAgent agents.ConfigAgent
	GitHubClient          github.Client
	Enqueuer              prowjobreconciler.Enqueuer
}

const ControllerName = "promotionreconciler"

// AddToManager adds this controller to the manager, which must be the one for the cluster
// hosting our central image registry, as we react to changes in ImageStreamTags there.
func AddToManager(registryMgr controllerruntime.Manager, opts Options) error {
	if err := opts.CIOperatorConfigAgent.AddIndex(configIndexName, configIndexFn); err != nil {
		return fmt.Errorf("failed to add indexer to config-agent: %w", err)
	}

	log := logrus.WithField("controller", ControllerName)
	r := &reconciler{
		log:    log,
		client: imagestreamtagwrapper.MustNew(registryMgr.GetClient(), registryMgr.GetCache()),
		releaseBuildConfigs: func(identifier string) ([]*cioperatorapi.ReleaseBuildConfiguration, error) {
			return opts.CIOperatorConfigAgent.GetFromIndex(configIndexName, identifier)
		},
		gitHubClient: opts.GitHubClient,
		enqueueJob:   opts.Enqueuer,
	}
	c, err := controller.New(ControllerName, registryMgr, controller.Options{
		Reconciler: r,
		// We currently have 50k ImageStreamTags in the OCP namespace and need to periodically reconcile all of them,
		// so don't be stingy with the workers
		MaxConcurrentReconciles: 100,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(
		&source.Kind{Type: &imagev1.ImageStream{}},
		imagestreamtagmapper.New(func(r reconcile.Request) []reconcile.Request { return []reconcile.Request{r} }),
	); err != nil {
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
	log                 *logrus.Entry
	client              ctrlruntimeclient.Client
	releaseBuildConfigs ciOperatorConfigGetter
	gitHubClient        githubClient
	enqueueJob          prowjobreconciler.Enqueuer
}

func (r *reconciler) Reconcile(ctx context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
	log := r.log.WithField("name", req.Name).WithField("namespace", req.Namespace)
	log.Trace("Starting reconciliation")
	startTime := time.Now()
	defer func() { log.WithField("duration", time.Since(startTime)).Trace("Finished reconciliation") }()

	err := r.reconcile(ctx, req, log)
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

func (r *reconciler) reconcile(ctx context.Context, req controllerruntime.Request, log *logrus.Entry) error {
	ist := &imagev1.ImageStreamTag{}
	if err := r.client.Get(ctx, req.NamespacedName, ist); err != nil {
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
	if ciOPConfig == nil || !promotion.AllPromotionImageStreamTags(ciOPConfig).Has(req.String()) {
		// We don't know how to build this
		log.Trace("No promotionConfig found")
		return nil
	}
	log = log.WithField("org", ciOPConfig.Metadata.Org).WithField("repo", ciOPConfig.Metadata.Repo).WithField("branch", ciOPConfig.Metadata.Branch)

	istCommit, err := commitForIST(ist)
	if err != nil {
		return controllerutil.TerminalError(fmt.Errorf("failed to get commit for imageStreamTag: %w", err))
	}
	log = log.WithField("istCommit", istCommit)

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
	log = log.WithField("currentHEAD", currentHEAD)

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
	return prowjobretrigger.CurrentHEADForBranch(r.gitHubClient, metadata, log)
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
