package steps

import (
	"context"
	"fmt"
	"log"
	"strings"

	imageapi "github.com/openshift/api/image/v1"
	"github.com/openshift/ci-operator/pkg/api"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// outputImageTagStep will ensure that a tag exists
// in the named ImageStream that resolves to the built
// pipeline image
type outputImageTagStep struct {
	config    api.OutputImageTagStepConfiguration
	istClient imageclientset.ImageStreamTagsGetter
	isClient  imageclientset.ImageStreamsGetter
	jobSpec   *api.JobSpec
}

func (s *outputImageTagStep) Inputs(ctx context.Context, dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *outputImageTagStep) Run(ctx context.Context, dry bool) error {
	toNamespace := s.namespace()
	if string(s.config.From) == s.config.To.Tag && toNamespace == s.jobSpec.Namespace && s.config.To.Name == api.StableImageStream {
		log.Printf("Tagging %s into %s", s.config.From, s.config.To.Name)
	} else {
		log.Printf("Tagging %s into %s/%s:%s", s.config.From, toNamespace, s.config.To.Name, s.config.To.Tag)
	}

	fromImage := "dry-fake"
	if !dry {
		from, err := s.istClient.ImageStreamTags(s.jobSpec.Namespace).Get(fmt.Sprintf("%s:%s", api.PipelineImageStream, s.config.From), meta.GetOptions{})
		if err != nil {
			return fmt.Errorf("could not resolve base image: %v", err)
		}
		fromImage = from.Image.Name
	}

	is := newImageStream(s.config.To.Namespace, s.config.To.Name)
	ist := s.imageStreamTag(fromImage)

	return createImageStreamWithTag(s.isClient, s.istClient, is, ist, dry)
}

func (s *outputImageTagStep) Done() (bool, error) {
	toNamespace := s.namespace()
	log.Printf("Checking for existence of %s/%s:%s", toNamespace, s.config.To.Name, s.config.To.Tag)
	ist, err := s.istClient.ImageStreamTags(toNamespace).Get(fmt.Sprintf("%s:%s", s.config.To.Name, s.config.To.Tag), meta.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("could not retrieve output imagestreamtag: %v", err)
	}

	// TODO(chance): this doesn't handle dry run since Done() doesn't have
	// information about if it's a dry-run
	from, err := s.istClient.ImageStreamTags(s.jobSpec.Namespace).Get(fmt.Sprintf("%s:%s", api.PipelineImageStream, s.config.From), meta.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("could not resolve base image: %v", err)
	}

	desiredIst := s.imageStreamTag(from.Image.Name)
	// if a tag already exists but doesn't match what we're looking for we're
	// not done
	return equality.Semantic.DeepEqual(ist.Tag, desiredIst.Tag), nil
}

func (s *outputImageTagStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.From), api.ReleaseImagesLink()}
}

func (s *outputImageTagStep) Creates() []api.StepLink {
	if len(s.config.To.As) > 0 {
		return []api.StepLink{api.ExternalImageLink(s.config.To), api.InternalImageLink(api.PipelineImageStreamTagReference(s.config.To.As))}
	}
	return []api.StepLink{api.ExternalImageLink(s.config.To)}
}

func (s *outputImageTagStep) Provides() (api.ParameterMap, api.StepLink) {
	if len(s.config.To.As) == 0 {
		return nil, nil
	}
	return api.ParameterMap{
		fmt.Sprintf("IMAGE_%s", strings.ToUpper(strings.Replace(s.config.To.As, "-", "_", -1))): func() (string, error) {
			is, err := s.isClient.ImageStreams(s.namespace()).Get(s.config.To.Name, meta.GetOptions{})
			if err != nil {
				return "", fmt.Errorf("could not retrieve output imagestream: %v", err)
			}
			var registry string
			if len(is.Status.PublicDockerImageRepository) > 0 {
				registry = is.Status.PublicDockerImageRepository
			} else if len(is.Status.DockerImageRepository) > 0 {
				registry = is.Status.DockerImageRepository
			} else {
				return "", fmt.Errorf("image stream %s has no accessible image registry value", s.config.To.As)
			}
			return fmt.Sprintf("%s:%s", registry, s.config.To.Tag), nil
		},
	}, api.ExternalImageLink(s.config.To)
}

func (s *outputImageTagStep) Name() string {
	if len(s.config.To.As) == 0 {
		return fmt.Sprintf("[output:%s:%s]", s.config.To.Name, s.config.To.Tag)
	}
	return s.config.To.As
}

func (s *outputImageTagStep) Description() string {
	if len(s.config.To.As) == 0 {
		return fmt.Sprintf("Tag the image %s into the image stream tag %s:%s", s.config.From, s.config.To.Name, s.config.To.Tag)
	}
	return fmt.Sprintf("Tag the image %s into the stable image stream", s.config.From)
}

func (s *outputImageTagStep) namespace() string {
	if len(s.config.To.Namespace) != 0 {
		return s.config.To.Namespace
	}
	return s.jobSpec.Namespace
}

func (s *outputImageTagStep) imageStreamTag(fromImage string) *imageapi.ImageStreamTag {
	return newImageStreamTag(
		s.jobSpec.Namespace, api.PipelineImageStream, fromImage,
		s.namespace(), s.config.To.Name, s.config.To.Tag,
	)
}

func OutputImageTagStep(config api.OutputImageTagStepConfiguration, istClient imageclientset.ImageStreamTagsGetter, isClient imageclientset.ImageStreamsGetter, jobSpec *api.JobSpec) api.Step {
	return &outputImageTagStep{
		config:    config,
		istClient: istClient,
		isClient:  isClient,
		jobSpec:   jobSpec,
	}
}
