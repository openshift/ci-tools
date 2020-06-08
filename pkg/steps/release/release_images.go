package release

import (
	"context"
	"fmt"
	"github.com/openshift/ci-tools/pkg/results"
	"log"
	"strings"

	coreapi "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"

	imageapi "github.com/openshift/api/image/v1"

	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/util"
)

// stableImagesTagStep is used when no release configuration is necessary
type stableImagesTagStep struct {
	jobSpec   *api.JobSpec
	dstClient imageclientset.ImageV1Interface
	dryLogger *steps.DryLogger
}

func StableImagesTagStep(dstClient imageclientset.ImageV1Interface, jobSpec *api.JobSpec, dryLogger *steps.DryLogger) api.Step {
	return &stableImagesTagStep{
		dstClient: dstClient,
		jobSpec:   jobSpec,
		dryLogger: dryLogger,
	}
}

func (s *stableImagesTagStep) Run(ctx context.Context, dry bool) error {
	return results.ForReason("creating_stable_images").ForError(s.run(ctx, dry))
}

func (s *stableImagesTagStep) run(ctx context.Context, dry bool) error {
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
	if dry {
		s.dryLogger.AddObject(newIS.DeepCopyObject())
		return nil
	}
	_, err := s.dstClient.ImageStreams(s.jobSpec.Namespace()).Create(newIS)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create stable imagestreamtag: %v", err)
	}
	return nil
}

func (s *stableImagesTagStep) Inputs(dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *stableImagesTagStep) Requires() []api.StepLink { return []api.StepLink{} }

func (s *stableImagesTagStep) Creates() []api.StepLink {
	// we can only ever create the latest stable image stream with this step
	return []api.StepLink{api.StableImagesLink(api.LatestStableName)}
}

func (s *stableImagesTagStep) Provides() (api.ParameterMap, api.StepLink) { return nil, nil }

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
	dryLogger       *steps.DryLogger
}

func findSpecTag(is *imageapi.ImageStream, tag string) *coreapi.ObjectReference {
	for _, t := range is.Spec.Tags {
		if t.Name != tag {
			continue
		}
		return t.From
	}
	return nil
}

func findStatusTag(is *imageapi.ImageStream, tag string) (*coreapi.ObjectReference, string) {
	for _, t := range is.Status.Tags {
		if t.Tag != tag {
			continue
		}
		if len(t.Items) == 0 {
			return nil, ""
		}
		if len(t.Items[0].Image) == 0 {
			return &coreapi.ObjectReference{
				Kind: "DockerImage",
				Name: t.Items[0].DockerImageReference,
			}, ""
		}
		return &coreapi.ObjectReference{
			Kind:      "ImageStreamImage",
			Namespace: is.Namespace,
			Name:      fmt.Sprintf("%s@%s", is.Name, t.Items[0].Image),
		}, t.Items[0].Image
	}
	return nil, ""
}

func (s *releaseImagesTagStep) Inputs(dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func sourceName(config api.ReleaseTagConfiguration) string {
	return fmt.Sprintf("%s/%s:%s", config.Namespace, config.Name, api.ComponentFormatReplacement)
}

func (s *releaseImagesTagStep) Run(ctx context.Context, dry bool) error {
	return results.ForReason("creating_release_images").ForError(s.run(ctx, dry))
}

func (s *releaseImagesTagStep) run(ctx context.Context, dry bool) error {
	if dry {
		log.Printf("Tagging shared images from %s", sourceName(s.config))
	} else {
		if format, err := s.imageFormat(); err == nil {
			log.Printf("Tagged shared images from %s, images will be pullable from %s", sourceName(s.config), format)
		} else {
			log.Printf("Tagged shared images from %s", sourceName(s.config))
		}
	}

	is, err := s.client.ImageStreams(s.config.Namespace).Get(s.config.Name, meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not resolve stable imagestream: %v", err)
	}

	is.UID = ""
	newIS := &imageapi.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Name: api.StableStreamFor(api.LatestStableName),
		},
		Spec: imageapi.ImageStreamSpec{
			LookupPolicy: imageapi.ImageLookupPolicy{
				Local: true,
			},
		},
	}
	for _, tag := range is.Spec.Tags {
		if valid, _ := findStatusTag(is, tag.Name); valid != nil {
			newIS.Spec.Tags = append(newIS.Spec.Tags, imageapi.TagReference{
				Name: tag.Name,
				From: valid,
			})
		}
	}

	if dry {
		s.dryLogger.AddObject(newIS.DeepCopyObject())
		return nil
	}

	initialIS := newIS.DeepCopy()
	initialIS.Name = api.StableStreamFor(api.InitialStableName)

	_, err = s.client.ImageStreams(s.jobSpec.Namespace()).Create(newIS)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("could not copy stable imagestreamtag: %v", err)
	}

	is, err = s.client.ImageStreams(s.jobSpec.Namespace()).Create(initialIS)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("could not copy stable-initial imagestreamtag: %v", err)
	}

	for _, tag := range is.Spec.Tags {
		spec, ok := util.ResolvePullSpec(is, tag.Name, false)
		if !ok {
			continue
		}
		s.params.Set("IMAGE_"+componentToParamName(tag.Name), spec)
	}

	return nil
}

func (s *releaseImagesTagStep) Requires() []api.StepLink {
	return []api.StepLink{}
}

func (s *releaseImagesTagStep) Creates() []api.StepLink {
	return []api.StepLink{
		api.StableImagesLink(api.InitialStableName),
		api.StableImagesLink(api.LatestStableName),
	}
}

func (s *releaseImagesTagStep) Provides() (api.ParameterMap, api.StepLink) {
	return api.ParameterMap{
		"IMAGE_FORMAT": s.imageFormat,
	}, api.ImagesReadyLink()
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
	is, err := s.client.ImageStreams(s.jobSpec.Namespace()).Get(api.PipelineImageStream, meta.GetOptions{})
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

func ReleaseImagesTagStep(config api.ReleaseTagConfiguration, client imageclientset.ImageV1Interface, routeClient routeclientset.RoutesGetter, configMapClient coreclientset.ConfigMapsGetter, params *api.DeferredParameters, jobSpec *api.JobSpec, dryLogger *steps.DryLogger) api.Step {
	return &releaseImagesTagStep{
		config:          config,
		client:          client,
		routeClient:     routeClient,
		configMapClient: configMapClient,
		params:          params,
		jobSpec:         jobSpec,
		dryLogger:       dryLogger,
	}
}

func componentToParamName(component string) string {
	return strings.ToUpper(strings.Replace(component, "-", "_", -1))
}
