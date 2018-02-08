package steps

import (
	"encoding/json"
	"fmt"
	"log"

	imageapi "github.com/openshift/api/image/v1"
	"github.com/openshift/ci-operator/pkg/api"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// outputImageTagStep will ensure that a tag exists
// in the named ImageStream that resolves to the built
// pipeline image
type outputImageTagStep struct {
	config    api.OutputImageTagStepConfiguration
	istClient imageclientset.ImageStreamTagInterface
	isClient  imageclientset.ImageStreamInterface
	jobSpec   *JobSpec
}

func (s *outputImageTagStep) Run(dry bool) error {
	log.Printf("Creating ImageStream %s/%s\n", s.jobSpec.Identifier(), s.config.To.Name)
	is := &imageapi.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Name:      s.config.To.Name,
			Namespace: s.jobSpec.Identifier(),
		},
	}
	if dry {
		isJSON, err := json.Marshal(is)
		if err != nil {
			return fmt.Errorf("failed to marshal imagestream: %v", err)
		}
		fmt.Printf("%s", isJSON)
	} else {
		_, err := s.isClient.Create(is)
		if err != nil && ! errors.IsAlreadyExists(err) {
			return err
		}
	}

	log.Printf("Tagging %s/%s:%s into %s/%s:%s\n", s.jobSpec.Identifier(), PipelineImageStream, s.config.From, s.jobSpec.Identifier(), s.config.To.Name, s.config.To.Tag)
	fromImage := "dry-fake"
	if !dry {
		from, err := s.istClient.Get(fmt.Sprintf("%s:%s", PipelineImageStream, s.config.From), meta.GetOptions{})
		if err != nil {
			return fmt.Errorf("could not resolve base image: %v", err)
		}
		fromImage = from.Image.Name
	}
	ist := &imageapi.ImageStreamTag{
		ObjectMeta: meta.ObjectMeta{
			Name:      fmt.Sprintf("%s:%s", s.config.To.Name, s.config.To.Tag),
			Namespace: s.jobSpec.Identifier(),
		},
		Tag: &imageapi.TagReference{
			ReferencePolicy: imageapi.TagReferencePolicy{
				Type: imageapi.LocalTagReferencePolicy,
			},
			From: &coreapi.ObjectReference{
				Kind:      "ImageStreamImage",
				Name:      fmt.Sprintf("%s@%s", PipelineImageStream, fromImage),
				Namespace: s.jobSpec.Identifier(),
			},
		},
	}
	if dry {
		istJSON, err := json.Marshal(ist)
		if err != nil {
			return fmt.Errorf("failed to marshal imagestreamtag: %v", err)
		}
		fmt.Printf("%s", istJSON)
	} else {
		_, err := s.istClient.Create(ist)
		if errors.IsAlreadyExists(err) {
			// another job raced with us, but the end
			// result will be the same so we don't care
			return nil
		}
		return err
	}

	return nil
}

func (s *outputImageTagStep) Done() (bool, error) {
	log.Printf("Checking for existence of %s/%s:%s\n", s.jobSpec.Identifier(), PipelineImageStream, s.config.To)
	_, err := s.istClient.Get(
		fmt.Sprintf("%s:%s", PipelineImageStream, s.config.To),
		meta.GetOptions{},
	)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		} else {
			return false, err
		}
	} else {
		return true, nil
	}
}

func (s *outputImageTagStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.From), api.ReleaseImagesLink()}
}

func (s *outputImageTagStep) Creates() []api.StepLink {
	return []api.StepLink{api.ExternalImageLink(s.config.To)}
}

func OutputImageTagStep(config api.OutputImageTagStepConfiguration, istClient imageclientset.ImageStreamTagInterface, isClient imageclientset.ImageStreamInterface, jobSpec *JobSpec) api.Step {
	return &outputImageTagStep{
		config:    config,
		istClient: istClient,
		isClient:  isClient,
		jobSpec:   jobSpec,
	}
}
