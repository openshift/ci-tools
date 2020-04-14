package imagestreamreconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	git "k8s.io/test-infra/prow/git/v2"
	"sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/steps/release"
)

func AddToManager(mgr controllerruntime.Manager) error {
	return controllerruntime.NewControllerManagedBy(mgr).
		For(&imagev1.ImageStreamTag{}).
		// We currently have 50k ImageStreamTags in the OCP namespace and need to periodically reconcile all of them,
		// so don't be stingy with the workers
		WithOptions(controller.Options{MaxConcurrentReconciles: 100}).
		Complete(&reconciler{
			log:    logrus.WithField("controller", "imageStreamReconciler"),
			client: mgr.GetClient(),
		})
}

type reconciler struct {
	ctx              context.Context
	log              *logrus.Entry
	client           ctrlruntimeclient.Client
	configAgent      agents.ConfigAgent
	gitClientFactory git.ClientFactory
}

func (r *reconciler) Reconcile(req controllerruntime.Request) (controllerruntime.Result, error) {
	log := r.log.WithField("name", req.Name).WithField("namespace", req.Namespace)
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

	publishedViaCIOperator, err := r.isPublishedFromCIOperatorRepo(ist)
	if !publishedViaCIOperator || err != nil {
		return wrapErrIfNonNil("failed to check if imageStreamTag is published from CI operator repo", err)
	}

	istCurrent, err := r.isISTCurrent(ist)
	if istCurrent || err != nil {
		return wrapErrIfNonNil("failed to check if imageStreamTag is up-to-date", err)
	}

	istHasBuildRunning, err := r.isBuildRunningForIST(ist)
	if istHasBuildRunning || err != nil {
		return wrapErrIfNonNil("failed to check if there is a running build for imageStreamTag", err)
	}

	if err := r.createBuildForIST(ist); err != nil {
		return fmt.Errorf("failed to create build for imageStreamTagL %w", err)
	}

	return nil
}

func (r *reconciler) isPublishedFromCIOperatorRepo(log *logrus.Entry, ist *imagev1.ImageStreamTag) (bool, error) {
	// TODO: Index this, what we do here is incredibly slow. And remember, we do it 50k times
	// during startup and resync
	for _, config := range r.configAgent.GetAll() {
		for _, istRef := range release.PromotedTags(config.PromotionConfiguration) {
			if ist.Name == istRef.Name+":"+istRef.Name {
				return true, nil
			}
		}
	}

	return false, nil
}

func (r *reconciler) isISTCurrent(ist *imagev1.ImageStreamTag) (bool, error) {
	imageMetadataLabels := struct {
		Labels map[string]string `json:"labels"`
	}{}
	if err := json.Unmarshal(ist.Image.DockerImageMetadata.Raw, &imageMetadataLabels); err != nil {
		return false, fmt.Errorf("failed to unmarshal imagestream.image.dockerImageMetadata: %w", err)
	}

	branch := imageMetadataLabels.Labels["io.openshift.build.commit.ref"]
	commit := imageMetadataLabels.Labels["io.openshift.build.commit.id"]
	sourceLocation := imageMetadataLabels.Labels["io.openshift.build.source-location"]
	if branch == "" {
		return false, nre(errors.New("imageStreamTag has no `io.openshift.build.commit.ref` label, can't find out source branch"))
	}
	if commit == "" {
		return false, nre(errors.New("ImageStreamTag has no `io.openshift.build.commit.id` label, can't find out source commit"))
	}
	if sourceLocation == "" {
		return false, nre(errors.New("imageStreamTag has no `io.openshift.build.source-location` label, can't find out source repo"))
	}
	sourceLocation = strings.TrimLeft(sourceLocation, "https://github.com/")
	splitSourceLocation := strings.Split(sourceLocation, "/")
	if n := len(splitSourceLocation); n != 2 {
		return false, nre(fmt.Errorf("sourceLocation %q split by `/` does not return 2 but %d results, can not find out org/repo", sourceLocation, n))
	}
	org, repo := splitSourceLocation[0], splitSourceLocation[1]

	gitClient, err := r.gitClientFactory.ClientFor(org, repo)
	if err != nil {
		return false, fmt.Errorf("failed to get git client for %s/%s: %w", org, repo, err)
	}
	branchHEADRef, err := gitClient.RevParse(branch)
	if err != nil {
		return false, fmt.Errorf("fauld to git rev-parse %s: %w", branch, err)
	}

	return branchHEADRef == commit, nil
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
