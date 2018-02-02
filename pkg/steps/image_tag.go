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

// imageTagStep will ensure that a tag exists
// in the pipeline ImageStream that resolves to
// the base image
type imageTagStep struct {
	config  api.ImageTagStepConfiguration
	client  imageclientset.ImageStreamTagInterface
	jobSpec *JobSpec
}

func (s *imageTagStep) Run(dry bool) error {
	log.Printf("Tagging %s/%s:%s into %s/%s:%s\n", s.config.BaseImage.Namespace, s.config.BaseImage.Name, s.config.BaseImage.Tag, s.jobSpec.Identifier(), PipelineImageStream, s.config.To)
	ist := &imageapi.ImageStreamTag{
		ObjectMeta: meta.ObjectMeta{
			Name:      fmt.Sprintf("%s:%s", PipelineImageStream, s.config.To),
			Namespace: s.jobSpec.Identifier(),
		},
		Tag: &imageapi.TagReference{
			ReferencePolicy: imageapi.TagReferencePolicy{
				Type: imageapi.LocalTagReferencePolicy,
			},
			From: &coreapi.ObjectReference{
				Kind:      "ImageStreamTag",
				Name:      fmt.Sprintf("%s:%s", s.config.BaseImage.Name, s.config.BaseImage.Tag),
				Namespace: s.config.BaseImage.Namespace,
			},
		},
	}
	if dry {
		istJSON, err := json.Marshal(ist)
		if err != nil {
			return fmt.Errorf("failed to marshal imagestreamtag: %v", err)
		}
		fmt.Printf("%s", istJSON)
		return nil
	}

	_, err := s.client.Update(ist)
	if errors.IsAlreadyExists(err) {
		// another job raced with us, but the end
		// err will be the same so we don't care
		return nil
	}
	return err
}

func (s *imageTagStep) Done() (bool, error) {
	log.Printf("Checking for existence of %s/%s:%s\n", s.jobSpec.Identifier(), PipelineImageStream, s.config.To)
	_, err := s.client.Get(
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

func (s *imageTagStep) Requires() []api.StepLink {
	return []api.StepLink{api.ExternalImageLink(s.config.BaseImage)}
}

func (s *imageTagStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func ImageTagStep(config api.ImageTagStepConfiguration, client imageclientset.ImageStreamTagInterface, jobSpec *JobSpec) api.Step {
	return &imageTagStep{
		config:  config,
		client:  client,
		jobSpec: jobSpec,
	}
}
