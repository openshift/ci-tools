package steps

import (
	"context"
	"fmt"
	"log"
	"strconv"

	imageapi "github.com/openshift/api/image/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-operator/pkg/api"
)

// PrePublishOutputImageTagStep will ensure that a tag exists
// in the named ImageStream that resolves to the built
// pipeline image
type prePublishOutputImageTagStep struct {
	config    api.PrePublishOutputImageTagStepConfiguration
	istClient imageclientset.ImageStreamTagsGetter
	isClient  imageclientset.ImageStreamsGetter
	jobSpec   *api.JobSpec
	tag       string
}

type prepublishConfig struct {
	namespace, name, tag string
}

func (s *prePublishOutputImageTagStep) Inputs(ctx context.Context, dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *prePublishOutputImageTagStep) Run(ctx context.Context, dry bool) error {
	log.Printf("Tagging %s into %s/%s:%s", s.config.From, s.config.To.Namespace, s.config.To.Name, s.tag)

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

func (s *prePublishOutputImageTagStep) Done() (bool, error) {
	log.Printf("Checking for existence of %s/%s:%s", s.config.To.Namespace, s.config.To.Name, s.tag)

	ist, err := s.istClient.ImageStreamTags(s.config.To.Namespace).Get(fmt.Sprintf("%s:%s", s.config.To.Name, s.tag), meta.GetOptions{})
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

func (s *prePublishOutputImageTagStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.From), api.ReleaseImagesLink()}
}

func (s *prePublishOutputImageTagStep) Creates() []api.StepLink {
	ref := api.ImageStreamTagReference{
		Name:      s.config.To.Name,
		Tag:       s.tag,
		Namespace: s.config.To.Namespace,
	}
	return []api.StepLink{api.ExternalImageLink(ref)}
}

func (s *prePublishOutputImageTagStep) Provides() (api.ParameterMap, api.StepLink) {
	return nil, nil
}

func (s *prePublishOutputImageTagStep) Name() string {
	return fmt.Sprintf("[prepublish:%s:%s]", s.config.To.Namespace, s.config.To.Name)
}

func (s *prePublishOutputImageTagStep) Description() string {
	return fmt.Sprintf("Tag the image %s into the image stream tag %s/%s:%s", s.config.From, s.config.To.Namespace, s.config.To.Name, s.tag)
}

func (s *prePublishOutputImageTagStep) imageStreamTag(fromImage string) *imageapi.ImageStreamTag {
	return newImageStreamTag(
		s.jobSpec.Namespace, api.PipelineImageStream, fromImage,
		s.config.To.Namespace, s.config.To.Name, s.tag,
	)
}

func PrePublishOutputImageTagStep(config api.PrePublishOutputImageTagStepConfiguration, istClient imageclientset.ImageStreamTagsGetter, isClient imageclientset.ImageStreamsGetter, jobSpec *api.JobSpec) api.Step {
	pull := jobSpec.Refs.Pulls[0]
	pullNumber := strconv.Itoa(pull.Number)
	tag := "pr-" + pullNumber
	return &prePublishOutputImageTagStep{
		config:    config,
		istClient: istClient,
		isClient:  isClient,
		jobSpec:   jobSpec,
		tag:       tag,
	}
}
