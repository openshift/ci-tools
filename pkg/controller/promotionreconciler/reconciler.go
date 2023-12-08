package promotionreconciler

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	imagev1 "github.com/openshift/api/image/v1"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/helper"
	"github.com/openshift/ci-tools/pkg/controller/promotionreconciler/prowjobreconciler"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/promotion"
	"github.com/openshift/ci-tools/pkg/steps/release"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagmapper"
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

	IgnoredImageStreams []*regexp.Regexp
	Since               time.Duration
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
	configChangeChannel, err := opts.CIOperatorConfigAgent.SubscribeToIndexChanges(configIndexName)
	if err != nil {
		return fmt.Errorf("failed to subscribe to index changes for index %s: %w", configIndexName, err)
	}
	prowJobEnqueuer, err := prowjobreconciler.AddToManager(mgr, opts.ConfigGetter, opts.DryRun)
	if err != nil {
		return fmt.Errorf("failed to construct prowjobreconciler: %w", err)
	}
	releaseBuildConfigs := func(identifier string) ([]*cioperatorapi.ReleaseBuildConfiguration, error) {
		return opts.CIOperatorConfigAgent.GetFromIndex(configIndexName, identifier)
	}
	log := logrus.WithField("controller", ControllerName)
	go func() {
		for delta := range configChangeChannel {
			if err := handleCIOpConfigChange(opts.RegistryManager.GetClient(), releaseBuildConfigs, prowJobEnqueuer, opts.GitHubClient, delta, log); err != nil {
				log.WithError(err).Error("Failed to handle CI Operator config change")
			}
		}
	}()
	r := &reconciler{
		log:                 log,
		client:              imagestreamtagwrapper.MustNew(opts.RegistryManager.GetClient(), opts.RegistryManager.GetCache()),
		releaseBuildConfigs: releaseBuildConfigs,
		gitHubClient:        opts.GitHubClient,
		enqueueJob:          prowJobEnqueuer,
		since:               opts.Since,
	}
	c, err := controller.New(ControllerName, opts.RegistryManager, controller.Options{
		Reconciler: r,
		// We currently have 50k ImageStreamTags in the OCP namespace and need to periodically reconcile all of them,
		// so don't be stingy with the workers
		MaxConcurrentReconciles: 100,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(
		source.Kind(mgr.GetCache(), &imagev1.ImageStream{}),
		imagestreamtagmapper.New(func(r reconcile.Request) []reconcile.Request {
			if ignored(r, opts.IgnoredImageStreams) {
				return nil
			}
			return []reconcile.Request{r}
		}),
	); err != nil {
		return fmt.Errorf("failed to create watch for ImageStreams: %w", err)
	}
	r.log.Info("Successfully added reconciler to manager")

	return nil
}

func ignored(r reconcile.Request, ignoredImageStreams []*regexp.Regexp) bool {
	is := fmt.Sprintf("%s/%s", r.Namespace, r.Name)
	for _, re := range ignoredImageStreams {
		if re.MatchString(is) {
			logrus.WithField("is", is).Info("Ignored image stream")
			return true
		}
	}
	return false
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
	since               time.Duration
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

	if !ist.CreationTimestamp.After(time.Now().Add(-r.since)) {
		log.WithField("creationTimestamp", ist.CreationTimestamp).Trace("Ignored old imageStreamTag")
		return nil
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

	istCommit, err := commitForIST(ist, r.client)
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

	log.Info("Requesting prowjob creation for a stale imagestreamtag")
	r.enqueueJob(prowjobreconciler.OrgRepoBranchCommit{
		Org:    ciOPConfig.Metadata.Org,
		Repo:   ciOPConfig.Metadata.Repo,
		Branch: ciOPConfig.Metadata.Branch,
		Commit: currentHEAD,
	})
	return nil
}

func promotionConfig(releaseBuildConfigs ciOperatorConfigGetter, ist *imagev1.ImageStreamTag) (*cioperatorapi.ReleaseBuildConfiguration, error) {
	results, err := releaseBuildConfigs(configIndexKeyForIST(ist))
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

func (r *reconciler) promotionConfig(ist *imagev1.ImageStreamTag) (*cioperatorapi.ReleaseBuildConfiguration, error) {
	return promotionConfig(r.releaseBuildConfigs, ist)
}

func commitForIST(ist *imagev1.ImageStreamTag, client ctrlruntimeclient.Client) (string, error) {
	labels, err := helper.LabelsOnISTagImage(context.TODO(), client, ist, cioperatorapi.ReleaseArchitectureAMD64)
	if err != nil {
		return "", controllerutil.TerminalError(fmt.Errorf("failed to get value of the image label: %w", err))
	}
	if labels == nil {
		return "", controllerutil.TerminalError(errors.New("ImageStreamTag has no labels, can't find out source commit"))
	}
	commit := labels["io.openshift.build.commit.id"]
	if commit == "" {
		return "", controllerutil.TerminalError(errors.New("ImageStreamTag has no `io.openshift.build.commit.id` label, can't find out source commit"))
	}

	return commit, nil
}

func currentHEADForBranch(githubClient githubClient, metadata cioperatorapi.Metadata, log *logrus.Entry) (string, bool, error) {
	// We attempted for some time to use the gitClient for this, but we do so many reconciliations that
	// it results in a massive performance issues that can easely kill the developers laptop.
	ref, err := githubClient.GetRef(metadata.Org, metadata.Repo, "heads/"+metadata.Branch)
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

func (r *reconciler) currentHEADForBranch(metadata cioperatorapi.Metadata, log *logrus.Entry) (string, bool, error) {
	return currentHEADForBranch(r.gitHubClient, metadata, log)
}

const configIndexName = "release-build-config-by-image-stream-tag"

func configIndexFn(in cioperatorapi.ReleaseBuildConfiguration) []string {
	var result []string
	for _, istRef := range release.PromotedTags(&in) {
		result = append(result, istRef.ISTagName())
	}
	return result
}

func configIndexKeyForIST(ist *imagev1.ImageStreamTag) string {
	return ist.Namespace + "/" + ist.Name
}

func handleCIOpConfigChange(registryClient ctrlruntimeclient.Client,
	ciOperatorConfigGetter ciOperatorConfigGetter,
	prowJobEnqueuer prowjobreconciler.Enqueuer,
	githubClient githubClient,
	delta agents.IndexDelta,
	log *logrus.Entry) error {
	log = log.WithField("indexKey", delta.IndexKey)
	log.Debug("Handling CIOpConfig change")
	// We only care about new additions
	if len(delta.Added) == 0 {
		return nil
	}
	slashSplit := strings.Split(delta.IndexKey, "/")
	if len(slashSplit) != 2 {
		return fmt.Errorf("got an index delta event with a key that is not a valid namespace/name identifier: %s", delta.IndexKey)
	}
	namespace, name := slashSplit[0], slashSplit[1]
	var ist imagev1.ImageStreamTag
	if err := registryClient.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, &ist); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get imagestreamtag %s/%s: %w", namespace, name, err)
		}
		ist = imagev1.ImageStreamTag{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
		}
		ciOPConfig, err := promotionConfig(ciOperatorConfigGetter, &ist)
		if err != nil {
			return fmt.Errorf("failed to get promotionConfig for imagestreamtag %s/%s: %w", namespace, name, err)
		}
		if ciOPConfig == nil {
			return fmt.Errorf("get nil from promotionConfig for imagestreamtag %s/%s", namespace, name)
		}
		currentHEAD, found, err := currentHEADForBranch(githubClient, ciOPConfig.Metadata, log)
		if err != nil {
			return fmt.Errorf("failed to get current git head for %s/%s/%s and imageStreamTag %s/%s: %w",
				ciOPConfig.Metadata.Org, ciOPConfig.Metadata.Repo, ciOPConfig.Metadata.Branch, namespace, name, err)
		}
		if !found {
			return fmt.Errorf("got 404 from github for %s/%s/%s and imageStreamTag %s/%s, this likely means the repo or branch got deleted or we are not allowed to access it",
				ciOPConfig.Metadata.Org, ciOPConfig.Metadata.Repo, ciOPConfig.Metadata.Branch, namespace, name)
		}
		log.WithField("name", ist.Name).WithField("namespace", ist.Namespace).Info("Requesting prowjob creation for a missing the imagestreamtag")
		prowJobEnqueuer(prowjobreconciler.OrgRepoBranchCommit{
			Org:    ciOPConfig.Metadata.Org,
			Repo:   ciOPConfig.Metadata.Repo,
			Branch: ciOPConfig.Metadata.Branch,
			Commit: currentHEAD,
		})
	}
	log.Debug("Handled CIOpConfig change")
	return nil
}
