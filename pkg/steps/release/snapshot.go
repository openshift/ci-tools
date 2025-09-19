package release

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/configresolver"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	"github.com/openshift/ci-tools/pkg/util"
)

// releaseSnapshotStep snapshots the state of an integration ImageStream
type releaseSnapshotStep struct {
	name             string
	config           api.Integration
	client           loggingclient.LoggingClient
	jobSpec          *api.JobSpec
	integratedStream *configresolver.IntegratedStream
}

func (r *releaseSnapshotStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

var NilIntegratedStreamError = errors.New("step snapshot an integrated stream without resolving required information")

func (r *releaseSnapshotStep) Validate() error {
	if r.integratedStream == nil {
		return NilIntegratedStreamError
	}
	return nil
}

func (r *releaseSnapshotStep) Run(ctx context.Context) error {
	return results.ForReason("creating_release_images").ForError(r.run(ctx))
}

func (r *releaseSnapshotStep) run(ctx context.Context) error {
	_, err := snapshotStream(ctx, r.client, r.config.Namespace, r.config.Name, r.jobSpec.Namespace, r.name, r.integratedStream, r.config.ReferencePolicy)
	return err
}

// snapshotStream snapshots the source IS and the snapshot copy created
func snapshotStream(ctx context.Context, client loggingclient.LoggingClient, sourceNamespace, sourceName string, targetNamespace func() string, targetRelease string, integratedStream *configresolver.IntegratedStream, refPolicy *imagev1.TagReferencePolicyType) (*imagev1.ImageStream, error) {
	targetName := api.ReleaseStreamFor(targetRelease)
	logrus.WithField("sourceNamespace", sourceNamespace).
		WithField("sourceName", sourceName).
		WithField("targetNamespace", targetNamespace()).
		WithField("targetName", targetName).
		WithField("tags", len(integratedStream.Tags)).
		Debug("Snapshotting tags on stream ...")
	snapshot := &imagev1.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Namespace:   targetNamespace(),
			Name:        targetName,
			Annotations: map[string]string{},
		},
		Spec: imagev1.ImageStreamSpec{
			LookupPolicy: imagev1.ImageLookupPolicy{
				Local: true,
			},
		},
	}
	if integratedStream.ReleaseControllerConfigName != "" {
		logrus.WithField("configName", integratedStream.ReleaseControllerConfigName).Debug("Setting up the release config annotation")
		value, err := configresolver.ReleaseControllerConfigNameToAnnotationValue(integratedStream.ReleaseControllerConfigName)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal release configuration on image stream %s: %w", targetName, err)
		}
		snapshot.ObjectMeta.Annotations[api.ReleaseConfigAnnotation] = value
	}
	for _, tag := range integratedStream.Tags {
		from := &coreapi.ObjectReference{
			Kind: "DockerImage",
			Name: api.QuayImageReference(api.ImageStreamTagReference{Namespace: sourceNamespace, Name: sourceName, Tag: tag}),
		}
		// a special case for cluster-bot
		if api.IsCreatedForClusterBotJob(sourceNamespace) {
			source := &imagev1.ImageStream{}
			if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: sourceNamespace, Name: sourceName}, source); err != nil {
				return nil, fmt.Errorf("could not resolve source imagestream %s/%s for release %s: %w", sourceNamespace, sourceName, targetRelease, err)
			}
			if valid, _ := utils.FindStatusTag(source, tag); valid != nil {
				from = valid
			} else {
				continue
			}
		}
		if refPolicy == nil {
			sourcePolicy := imagev1.SourceTagReferencePolicy
			refPolicy = &sourcePolicy
		}
		tagReference := imagev1.TagReference{
			Name:            tag,
			From:            from,
			ImportPolicy:    imagev1.TagImportPolicy{ImportMode: imagev1.ImportModePreserveOriginal},
			ReferencePolicy: imagev1.TagReferencePolicy{Type: *refPolicy},
		}
		snapshot.Spec.Tags = append(snapshot.Spec.Tags, tagReference)
	}

	created, err := util.CreateImageStreamWithMetrics(ctx, client, snapshot.DeepCopy(), client.MetricsAgent())
	if err != nil {
		return nil, fmt.Errorf("could not create snapshot imagestream %s/%s for release %s: %w", sourceNamespace, sourceName, targetRelease, err)
	}
	logrus.Infof("Waiting to import tags on imagestream (after taking snapshot) %s/%s ...", created.Namespace, created.Name)
	if err := utils.WaitForImportingISTag(ctx, client, created.Namespace, created.Name, nil, sets.New[string](), utils.DefaultImageImportTimeout, client.MetricsAgent()); err != nil {
		return nil, fmt.Errorf("failed to wait for importing imagestreamtags on %s/%s: %w", created.Namespace, created.Name, err)
	}
	logrus.Infof("Imported tags on imagestream (after taking snapshot) %s/%s", created.Namespace, created.Name)
	return snapshot, nil
}

func (r *releaseSnapshotStep) Name() string {
	return fmt.Sprintf("[release-inputs:%s]", r.name)
}

func (r *releaseSnapshotStep) Description() string {
	return fmt.Sprintf("Find all of the input images from %s/%s and tag them into the %s stream", r.config.Namespace, r.config.Name, api.ReleaseStreamFor(r.name))
}

func (r *releaseSnapshotStep) Requires() []api.StepLink {
	return []api.StepLink{}
}

func (r *releaseSnapshotStep) Creates() []api.StepLink {
	return []api.StepLink{api.ReleaseImagesLink(r.name)}
}

func (r *releaseSnapshotStep) Provides() api.ParameterMap {
	return nil
}

func (r *releaseSnapshotStep) Objects() []ctrlruntimeclient.Object {
	return r.client.Objects()
}

func ReleaseSnapshotStep(release string, config api.Integration, client loggingclient.LoggingClient, jobSpec *api.JobSpec, integratedStream *configresolver.IntegratedStream) api.Step {
	return &releaseSnapshotStep{
		name:             release,
		config:           config,
		client:           client,
		jobSpec:          jobSpec,
		integratedStream: integratedStream,
	}
}
