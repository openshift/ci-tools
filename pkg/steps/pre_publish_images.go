package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"

	imageapi "github.com/openshift/api/image/v1"
	"github.com/openshift/ci-operator/pkg/api"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	is := &imageapi.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Name:      s.config.To.Name,
			Namespace: s.config.To.Namespace,
		},
	}

	ist := &imageapi.ImageStreamTag{
		ObjectMeta: meta.ObjectMeta{
			Name:      fmt.Sprintf("%s:%s", s.config.To.Name, s.tag),
			Namespace: s.config.To.Namespace,
		},
		Tag: &imageapi.TagReference{
			ReferencePolicy: imageapi.TagReferencePolicy{
				Type: imageapi.LocalTagReferencePolicy,
			},
			From: &coreapi.ObjectReference{
				Kind:      "ImageStreamImage",
				Name:      fmt.Sprintf("%s@%s", api.PipelineImageStream, fromImage),
				Namespace: s.jobSpec.Namespace,
			},
		},
	}

	if dry {
		isJSON, err := json.MarshalIndent(is, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal imagestream: %v", err)
		}
		fmt.Printf("%s\n", isJSON)

		istJSON, err := json.MarshalIndent(ist, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal imagestreamtag: %v", err)
		}
		fmt.Printf("%s\n", istJSON)
		return nil
	}

	_, err := s.isClient.ImageStreams(is.Namespace).Get(is.Name, meta.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = s.isClient.ImageStreams(is.Namespace).Create(is)
	}
	if err != nil {
		return fmt.Errorf("could not retrieve target imagestream: %v", err)
	}

	if err = s.istClient.ImageStreamTags(ist.Namespace).Delete(ist.Name, nil); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("could not remove output imagestreamtag: %v", err)
	}
	_, err = s.istClient.ImageStreamTags(ist.Namespace).Create(ist)
	if err != nil {
		return fmt.Errorf("could not create output imagestreamtag: %v", err)
	}

	return nil
}

func (s *prePublishOutputImageTagStep) Done() (bool, error) {
	log.Printf("Checking for existence of %s/%s:%s", s.config.To.Namespace, s.config.To.Name, s.tag)
	name := fmt.Sprintf("%s:%s", s.config.To.Name, s.tag)
	if _, err := s.istClient.ImageStreamTags(s.config.To.Namespace).Get(name, meta.GetOptions{}); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("could not retrieve output imagestreamtag %s: %v", name, err)
	}
	return true, nil
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
