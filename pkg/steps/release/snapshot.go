package release

import (
	"context"
	"fmt"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

// releaseSnapshotStep snapshots the state of an integration ImageStream
type releaseSnapshotStep struct {
	name    string
	config  api.Integration
	client  loggingclient.LoggingClient
	jobSpec *api.JobSpec
}

func (r *releaseSnapshotStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (r *releaseSnapshotStep) Validate() error {
	return nil
}

func (r *releaseSnapshotStep) Run(ctx context.Context, o *api.RunOptions) error {
	return results.ForReason("creating_release_images").ForError(r.run(ctx))
}

func (r *releaseSnapshotStep) run(ctx context.Context) error {
	_, _, err := snapshotStream(ctx, r.client, r.config.Namespace, r.config.Name, r.jobSpec.Namespace, r.name)
	return err
}

// snapshotStream snapshots the source IS, returning it and the snapshot copy created
func snapshotStream(ctx context.Context, client loggingclient.LoggingClient, sourceNamespace, sourceName string, targetNamespace func() string, targetRelease string) (*imagev1.ImageStream, *imagev1.ImageStream, error) {
	source := &imagev1.ImageStream{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: sourceNamespace, Name: sourceName}, source); err != nil {
		return nil, nil, fmt.Errorf("could not resolve source imagestream %s/%s for release %s: %w", sourceNamespace, sourceName, targetRelease, err)
	}

	snapshot := &imagev1.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Namespace:   targetNamespace(),
			Name:        api.ReleaseStreamFor(targetRelease),
			Annotations: map[string]string{},
		},
		Spec: imagev1.ImageStreamSpec{
			LookupPolicy: imagev1.ImageLookupPolicy{
				Local: true,
			},
		},
	}
	if raw, ok := source.ObjectMeta.Annotations[releaseConfigAnnotation]; ok {
		snapshot.ObjectMeta.Annotations[releaseConfigAnnotation] = raw
	}
	for _, tag := range source.Status.Tags {
		if valid, _ := utils.FindStatusTag(source, tag.Tag); valid != nil {
			snapshot.Spec.Tags = append(snapshot.Spec.Tags, imagev1.TagReference{
				Name:            tag.Tag,
				From:            valid,
				ReferencePolicy: imagev1.TagReferencePolicy{Type: imagev1.LocalTagReferencePolicy},
			})
		}
	}
	// the Create call mutates the input object, so we need to copy it before returning
	created := snapshot.DeepCopy()
	if err := client.Create(ctx, created); err != nil && !kerrors.IsAlreadyExists(err) {
		return nil, nil, fmt.Errorf("could not create snapshot imagestream %s/%s for release %s: %w", sourceNamespace, sourceName, targetRelease, err)
	}
	return source, snapshot, nil
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

func ReleaseSnapshotStep(release string, config api.Integration, client loggingclient.LoggingClient, jobSpec *api.JobSpec) api.Step {
	return &releaseSnapshotStep{
		name:    release,
		config:  config,
		client:  client,
		jobSpec: jobSpec,
	}
}
