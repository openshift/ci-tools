package steps

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"github.com/openshift/ci-operator/pkg/api"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
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

	return createImageStreamWithTag(s.isClient, s.istClient, s.jobSpec.Namespace, string(api.PipelineImageStream), string(s.config.From), s.config.To.Namespace, s.config.To.Name, s.tag, dry)
}

func (s *prePublishOutputImageTagStep) Done() (bool, error) {
	log.Printf("Checking for existence of %s/%s:%s", s.config.To.Namespace, s.config.To.Name, s.tag)
	return imagesStreamTagExists(s.istClient, s.config.To.Namespace, s.config.To.Name, s.tag)
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
