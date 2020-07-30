package release

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/openshift/ci-tools/pkg/results"

	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/steps/utils"

	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"

	imageapi "github.com/openshift/api/image/v1"

	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/util"
)

const releaseConfigAnnotation = "release.openshift.io/config"

// stableImagesTagStep is used when no release configuration is necessary
type stableImagesTagStep struct {
	jobSpec   *api.JobSpec
	dstClient imageclientset.ImageV1Interface
}

func StableImagesTagStep(dstClient imageclientset.ImageV1Interface, jobSpec *api.JobSpec) api.Step {
	return &stableImagesTagStep{
		dstClient: dstClient,
		jobSpec:   jobSpec,
	}
}

func (s *stableImagesTagStep) Run(ctx context.Context) error {
	return results.ForReason("creating_stable_images").ForError(s.run(ctx))
}

func (s *stableImagesTagStep) run(ctx context.Context) error {
	log.Printf("Will output images to %s:%s", api.StableImageStream, api.ComponentFormatReplacement)

	newIS := &imageapi.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Name: api.StableImageStream,
		},
		Spec: imageapi.ImageStreamSpec{
			LookupPolicy: imageapi.ImageLookupPolicy{
				Local: true,
			},
		},
	}
	_, err := s.dstClient.ImageStreams(s.jobSpec.Namespace()).Create(ctx, newIS, meta.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create stable imagestreamtag: %w", err)
	}
	return nil
}

func (s *stableImagesTagStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

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

// releaseImagesTagStep will tag a full release suite
// of images in from the configured namespace. It is
// expected that builds will overwrite these tags at
// a later point, selectively
type releaseImagesTagStep struct {
	config          api.ReleaseTagConfiguration
	client          imageclientset.ImageV1Interface
	routeClient     routeclientset.RoutesGetter
	configMapClient coreclientset.ConfigMapsGetter
	params          *api.DeferredParameters
	jobSpec         *api.JobSpec
}

func (s *releaseImagesTagStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func sourceName(config api.ReleaseTagConfiguration) string {
	return fmt.Sprintf("%s/%s:%s", config.Namespace, config.Name, api.ComponentFormatReplacement)
}

func (s *releaseImagesTagStep) Run(ctx context.Context) error {
	return results.ForReason("creating_release_images").ForError(s.run(ctx))
}

func (s *releaseImagesTagStep) run(ctx context.Context) error {
	if format, err := s.imageFormat(); err == nil {
		log.Printf("Tagged shared images from %s, images will be pullable from %s", sourceName(s.config), format)
	} else {
		log.Printf("Tagged shared images from %s", sourceName(s.config))
	}

	is, err := s.client.ImageStreams(s.config.Namespace).Get(ctx, s.config.Name, meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not resolve stable imagestream: %w", err)
	}

	is.UID = ""
	newIS := &imageapi.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Name:        api.ReleaseStreamFor(api.LatestReleaseName),
			Annotations: map[string]string{},
		},
		Spec: imageapi.ImageStreamSpec{
			LookupPolicy: imageapi.ImageLookupPolicy{
				Local: true,
			},
		},
	}
	if raw, ok := is.ObjectMeta.Annotations[releaseConfigAnnotation]; ok {
		newIS.ObjectMeta.Annotations[releaseConfigAnnotation] = raw
	}
	for _, tag := range is.Spec.Tags {
		if valid, _ := utils.FindStatusTag(is, tag.Name); valid != nil {
			newIS.Spec.Tags = append(newIS.Spec.Tags, imageapi.TagReference{
				Name: tag.Name,
				From: valid,
			})
		}
	}

	initialIS := newIS.DeepCopy()
	initialIS.Name = api.ReleaseStreamFor(api.InitialReleaseName)

	_, err = s.client.ImageStreams(s.jobSpec.Namespace()).Create(ctx, newIS, meta.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("could not copy stable imagestreamtag: %w", err)
	}

	is, err = s.client.ImageStreams(s.jobSpec.Namespace()).Create(ctx, initialIS, meta.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("could not copy stable-initial imagestreamtag: %w", err)
	}

	for _, tag := range is.Spec.Tags {
		spec, ok := util.ResolvePullSpec(is, tag.Name, false)
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
	format := fmt.Sprintf("%s/%s/%s:%s", registry, s.jobSpec.Namespace(), fmt.Sprintf("%s%s", s.config.NamePrefix, api.StableImageStream), api.ComponentFormatReplacement)
	return format, nil
}

func (s *releaseImagesTagStep) repositoryPullSpec() (string, error) {
	is, err := s.client.ImageStreams(s.jobSpec.Namespace()).Get(context.TODO(), api.PipelineImageStream, meta.GetOptions{})
	if err != nil {
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

func (s *releaseImagesTagStep) Name() string { return "[release-inputs]" }

func (s *releaseImagesTagStep) Description() string {
	return fmt.Sprintf("Find all of the input images from %s and tag them into the output image stream", sourceName(s.config))
}

func ReleaseImagesTagStep(config api.ReleaseTagConfiguration, client imageclientset.ImageV1Interface, routeClient routeclientset.RoutesGetter, configMapClient coreclientset.ConfigMapsGetter, params *api.DeferredParameters, jobSpec *api.JobSpec) api.Step {
	return &releaseImagesTagStep{
		config:          config,
		client:          client,
		routeClient:     routeClient,
		configMapClient: configMapClient,
		params:          params,
		jobSpec:         jobSpec,
	}
}
