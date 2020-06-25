package steps

import (
	"context"
	"fmt"
	"log"
	"time"

	imageapi "github.com/openshift/api/image/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/util"
)

var (
	apiCIRegistry = api.DomainForService(api.ServiceRegistry)
)

// inputImageTagStep will ensure that a tag exists
// in the pipeline ImageStream that resolves to
// the base image
type inputImageTagStep struct {
	config  api.InputImageTagStepConfiguration
	client  imageclientset.ImageV1Interface
	jobSpec *api.JobSpec

	imageName string
}

func (s *inputImageTagStep) Inputs() (api.InputDefinition, error) {
	if len(s.imageName) > 0 {
		return api.InputDefinition{s.imageName}, nil
	}
	from, err := s.client.ImageStreamTags(s.config.BaseImage.Namespace).Get(fmt.Sprintf("%s:%s", s.config.BaseImage.Name, s.config.BaseImage.Tag), metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not resolve base image: %w", err)
	}

	log.Printf("Resolved %s/%s:%s to %s", s.config.BaseImage.Namespace, s.config.BaseImage.Name, s.config.BaseImage.Tag, from.Image.Name)
	s.imageName = from.Image.Name
	return api.InputDefinition{from.Image.Name}, nil
}

func (s *inputImageTagStep) Run(ctx context.Context) error {
	return results.ForReason("tagging_input_image").ForError(s.run(ctx))
}

func (s *inputImageTagStep) run(ctx context.Context) error {
	log.Printf("Tagging %s/%s:%s into %s:%s", s.config.BaseImage.Namespace, s.config.BaseImage.Name, s.config.BaseImage.Tag, api.PipelineImageStream, s.config.To)

	if _, err := s.Inputs(); err != nil {
		return fmt.Errorf("could not resolve inputs for image tag step: %w", err)
	}

	ist := &imageapi.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s:%s", api.PipelineImageStream, s.config.To),
			Namespace: s.jobSpec.Namespace(),
		},
		Tag: &imageapi.TagReference{
			ReferencePolicy: imageapi.TagReferencePolicy{
				Type: imageapi.LocalTagReferencePolicy,
			},
			From: &coreapi.ObjectReference{
				Kind:      "ImageStreamImage",
				Name:      fmt.Sprintf("%s@%s", s.config.BaseImage.Name, s.imageName),
				Namespace: s.config.BaseImage.Namespace,
			},
		},
	}

	if _, err := s.client.ImageStreamTags(s.jobSpec.Namespace()).Create(ist); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create imagestreamtag for input image: %w", err)
	}
	// Wait image is ready
	importCtx, cancel := context.WithTimeout(ctx, 35*time.Minute)
	defer cancel()
	if err := wait.PollImmediateUntil(10*time.Second, func() (bool, error) {
		pipeline, err := s.client.ImageStreams(s.jobSpec.Namespace()).Get(api.PipelineImageStream, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		_, exists := util.ResolvePullSpec(pipeline, string(s.config.To), true)
		if !exists {
			log.Printf("waiting for importing %s ...", ist.ObjectMeta.Name)
		}
		return exists, nil
	}, importCtx.Done()); err != nil {
		log.Printf("could not resolve tag %s in imagestream %s: %v", s.config.To, api.PipelineImageStream, err)
		return err
	}
	return nil
}

func istObjectReference(client imageclientset.ImageV1Interface, reference api.ImageStreamTagReference) (coreapi.ObjectReference, error) {
	is, err := client.ImageStreams(reference.Namespace).Get(reference.Name, metav1.GetOptions{})
	if err != nil {
		return coreapi.ObjectReference{}, fmt.Errorf("could not resolve remote image stream: %w", err)
	}
	var repo string
	if len(is.Status.PublicDockerImageRepository) > 0 {
		repo = is.Status.PublicDockerImageRepository
	} else if len(is.Status.DockerImageRepository) > 0 {
		repo = is.Status.DockerImageRepository
	} else {
		return coreapi.ObjectReference{}, fmt.Errorf("remote image stream %s has no accessible image registry value", reference.Name)
	}
	ist, err := client.ImageStreamTags(reference.Namespace).Get(fmt.Sprintf("%s:%s", reference.Name, reference.Tag), metav1.GetOptions{})
	if err != nil {
		return coreapi.ObjectReference{}, fmt.Errorf("could not resolve remote image stream tag: %w", err)
	}
	return coreapi.ObjectReference{Kind: "DockerImage", Name: fmt.Sprintf("%s@%s", repo, ist.Image.Name)}, nil
}

func (s *inputImageTagStep) Requires() []api.StepLink {
	return nil
}

func (s *inputImageTagStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func (s *inputImageTagStep) Provides() api.ParameterMap {
	return nil
}

func (s *inputImageTagStep) Name() string { return fmt.Sprintf("[input:%s]", s.config.To) }

func (s *inputImageTagStep) Description() string {
	return fmt.Sprintf("Find the input image %s and tag it into the pipeline", s.config.To)
}

func InputImageTagStep(config api.InputImageTagStepConfiguration, client imageclientset.ImageV1Interface, jobSpec *api.JobSpec) api.Step {
	// when source and destination client are the same, we don't need to use external imports
	return &inputImageTagStep{
		config:  config,
		client:  client,
		jobSpec: jobSpec,
	}
}
