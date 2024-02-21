package release

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/util"
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

func (r *releaseSnapshotStep) Run(ctx context.Context) error {
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
		snapshot.Spec.Tags = append(snapshot.Spec.Tags, imagev1.TagReference{
			Name: tag.Tag,
			From: &coreapi.ObjectReference{
				Kind: "DockerImage",
				Name: api.QCIAPPCIImage(api.ImageStreamTagReference{Namespace: sourceNamespace, Name: sourceName, Tag: tag.Tag}),
			},
			ReferencePolicy: imagev1.TagReferencePolicy{Type: imagev1.LocalTagReferencePolicy},
		})

	}
	// the Create call mutates the input object, so we need to copy it before returning
	created := snapshot.DeepCopy()
	if err := client.Create(ctx, created); err != nil && !kerrors.IsAlreadyExists(err) {
		return nil, nil, fmt.Errorf("could not create snapshot imagestream %s/%s for release %s: %w", sourceNamespace, sourceName, targetRelease, err)
	}
	begin := time.Now()
	logrus.Infof("Waiting to import tags on imagestream %s/%s ...", created.Namespace, created.Name)
	for i, tag := range created.Spec.Tags {
		stable := &imagev1.ImageStream{}
		if err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			if time.Now().After(begin.Add(35 * time.Minute)) {
				return false, fmt.Errorf("timed out importing tags[%d] %s on imagestream %s/%s", i, tag.Name, created.Namespace, created.Name)
			}
			if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: created.Namespace, Name: created.Name}, stable); err != nil {
				return false, err
			}
			_, exist := util.ResolvePullSpec(stable, tag.Name, true)
			if !exist {
				logrus.Debugf("Waiting to import tags on imagestream %s/%s:%s ...", created.Namespace, created.Name, tag.Name)
			}
			return exist, nil
		}); err != nil {
			return nil, nil, fmt.Errorf("failed to import tags on imagestream %s/%s: %w", created.Namespace, created.Name, err)
		}
	}
	logrus.Infof("Imported tags on imagestream %s/%s", created.Namespace, created.Name)
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
