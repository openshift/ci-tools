package release

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	"github.com/openshift/ci-tools/pkg/util"
)

const (
	releaseConfigAnnotation = "release.openshift.io/config"
	Label                   = "ci.openshift.io/release"
)

// stableImagesTagStep is used when no release configuration is necessary
type stableImagesTagStep struct {
	jobSpec *api.JobSpec
	client  loggingclient.LoggingClient
}

func StableImagesTagStep(client loggingclient.LoggingClient, jobSpec *api.JobSpec) api.Step {
	return &stableImagesTagStep{
		client:  client,
		jobSpec: jobSpec,
	}
}

func (s *stableImagesTagStep) Run(ctx context.Context) error {
	return results.ForReason("creating_stable_images").ForError(s.run(ctx))
}

func (s *stableImagesTagStep) run(ctx context.Context) error {
	logrus.Infof("Will output images to %s:%s", api.StableImageStream, api.ComponentFormatReplacement)

	newIS := &imagev1.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Namespace: s.jobSpec.Namespace(),
			Name:      api.StableImageStream,
		},
		Spec: imagev1.ImageStreamSpec{
			LookupPolicy: imagev1.ImageLookupPolicy{
				Local: true,
			},
		},
	}
	if err := s.client.Create(ctx, newIS); err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create stable imagestreamtag: %w", err)
	}
	return nil
}

func (s *stableImagesTagStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*stableImagesTagStep) Validate() error { return nil }

func (s *stableImagesTagStep) Requires() []api.StepLink { return []api.StepLink{} }

func (s *stableImagesTagStep) Creates() []api.StepLink {
	// we can only ever create the latest stable image stream with this step
	return []api.StepLink{api.ReleaseImagesLink(api.LatestReleaseName)}
}

func (s *stableImagesTagStep) Provides() api.ParameterMap { return nil }

func (s *stableImagesTagStep) Name() string { return "[output-images]" }

func (s *stableImagesTagStep) Description() string {
	return fmt.Sprintf("Create the output image stream %s", api.StableImageStream)
}

func (s *stableImagesTagStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

// releaseImagesTagStep will tag a full release suite
// of images in from the configured namespace. It is
// expected that builds will overwrite these tags at
// a later point, selectively
type releaseImagesTagStep struct {
	config  api.ReleaseTagConfiguration
	client  loggingclient.LoggingClient
	params  *api.DeferredParameters
	jobSpec *api.JobSpec
}

func (s *releaseImagesTagStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*releaseImagesTagStep) Validate() error { return nil }

func sourceName(config api.ReleaseTagConfiguration) string {
	return fmt.Sprintf("%s/%s:%s", config.Namespace, config.Name, api.ComponentFormatReplacement)
}

func (s *releaseImagesTagStep) Run(ctx context.Context) error {
	return results.ForReason("creating_release_images").ForError(s.run(ctx))
}

func (s *releaseImagesTagStep) run(ctx context.Context) error {
	if format, err := s.imageFormat(); err == nil {
		logrus.Infof("Tagged shared images from %s, images will be pullable from %s", sourceName(s.config), format)
	} else {
		logrus.Infof("Tagged shared images from %s", sourceName(s.config))
	}

	is, newIS, err := snapshotStream(ctx, s.client, s.config.Namespace, s.config.Name, s.jobSpec.Namespace, api.LatestReleaseName)
	if err != nil {
		return err
	}

	initialIS := newIS.DeepCopy()
	initialIS.Name = api.ReleaseStreamFor(api.InitialReleaseName)

	if err := s.client.Create(ctx, newIS); err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("could not copy stable imagestreamtag: %w", err)
	}

	if err := s.client.Create(ctx, initialIS); err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("could not copy stable-initial imagestreamtag: %w", err)
	}

	for _, tag := range is.Spec.Tags {
		spec, ok, _ := util.ResolvePullSpec(is, tag.Name, false)
		if !ok {
			continue
		}
		s.params.Set(utils.StableImageEnv(tag.Name), spec)
	}

	return nil
}

func (s *releaseImagesTagStep) Requires() []api.StepLink {
	return []api.StepLink{}
}

func (s *releaseImagesTagStep) Creates() []api.StepLink {
	return []api.StepLink{
		api.ReleaseImagesLink(api.InitialReleaseName),
		api.ReleaseImagesLink(api.LatestReleaseName),
	}
}

func (s *releaseImagesTagStep) Provides() api.ParameterMap {
	return api.ParameterMap{
		utils.ImageFormatEnv: s.imageFormat,
	}
}

func (s *releaseImagesTagStep) imageFormat() (string, error) {
	spec, err := s.repositoryPullSpec()
	if err != nil {
		return "REGISTRY", err
	}
	registry := strings.SplitN(spec, "/", 2)[0]
	format := fmt.Sprintf("%s/%s/%s:%s", registry, s.jobSpec.Namespace(), api.StableImageStream, api.ComponentFormatReplacement)
	return format, nil
}

func (s *releaseImagesTagStep) repositoryPullSpec() (string, error) {
	is := &imagev1.ImageStream{}
	if err := s.client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: api.PipelineImageStream}, is); err != nil {
		return "", err
	}
	if len(is.Status.PublicDockerImageRepository) > 0 {
		return is.Status.PublicDockerImageRepository, nil
	}
	if len(is.Status.DockerImageRepository) > 0 {
		return is.Status.DockerImageRepository, nil
	}
	return "", fmt.Errorf("no pull spec available for image stream %s", api.PipelineImageStream)
}

func (s *releaseImagesTagStep) Name() string { return s.config.InputsName() }

func (s *releaseImagesTagStep) Description() string {
	return fmt.Sprintf("Find all of the input images from %s and tag them into the output image stream", sourceName(s.config))
}

func (s *releaseImagesTagStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func ReleaseImagesTagStep(config api.ReleaseTagConfiguration, client loggingclient.LoggingClient, params *api.DeferredParameters, jobSpec *api.JobSpec) api.Step {
	return &releaseImagesTagStep{
		config:  config,
		client:  client,
		params:  params,
		jobSpec: jobSpec,
	}
}
