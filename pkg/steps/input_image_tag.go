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
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/util"
)

var (
	apiCIRegistry = util.DomainForService("registry")
)

// inputImageTagStep will ensure that a tag exists
// in the pipeline ImageStream that resolves to
// the base image
type inputImageTagStep struct {
	config    api.InputImageTagStepConfiguration
	srcClient imageclientset.ImageV1Interface
	dstClient imageclientset.ImageV1Interface
	jobSpec   *api.JobSpec
	dryLogger *DryLogger

	imageName string
}

func (s *inputImageTagStep) Inputs(dry bool) (api.InputDefinition, error) {
	if dry {
		return api.InputDefinition{s.imageName}, nil
	}

	if len(s.imageName) > 0 {
		return api.InputDefinition{s.imageName}, nil
	}
	from, err := s.srcClient.ImageStreamTags(s.config.BaseImage.Namespace).Get(fmt.Sprintf("%s:%s", s.config.BaseImage.Name, s.config.BaseImage.Tag), meta.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not resolve base image: %v", err)
	}

	// check to see if the src and dst are the same cluster, in which case we can use a more efficient tagging path
	if len(s.config.BaseImage.Cluster) > 0 {
		if dstFrom, err := s.dstClient.ImageStreamTags(from.Namespace).Get(from.Name, meta.GetOptions{}); err == nil && dstFrom.UID == from.UID {
			s.config.BaseImage.Cluster = ""
		}
	}

	if len(s.config.BaseImage.Cluster) > 0 {
		log.Printf("Resolved %s/%s/%s:%s to %s", s.config.BaseImage.Cluster, s.config.BaseImage.Namespace, s.config.BaseImage.Name, s.config.BaseImage.Tag, from.Image.Name)
	} else {
		log.Printf("Resolved %s/%s:%s to %s", s.config.BaseImage.Namespace, s.config.BaseImage.Name, s.config.BaseImage.Tag, from.Image.Name)
	}
	s.imageName = from.Image.Name
	return api.InputDefinition{from.Image.Name}, nil
}

func (s *inputImageTagStep) Run(ctx context.Context, dry bool) error {
	return results.ForReason("tagging_input_image").ForError(s.run(ctx, dry))
}

func (s *inputImageTagStep) run(ctx context.Context, dry bool) error {
	if len(s.config.BaseImage.Cluster) > 0 {
		log.Printf("Tagging %s/%s/%s:%s into %s:%s", s.config.BaseImage.Cluster, s.config.BaseImage.Namespace, s.config.BaseImage.Name, s.config.BaseImage.Tag, api.PipelineImageStream, s.config.To)
	} else {
		log.Printf("Tagging %s/%s:%s into %s:%s", s.config.BaseImage.Namespace, s.config.BaseImage.Name, s.config.BaseImage.Tag, api.PipelineImageStream, s.config.To)
	}

	_, err := s.Inputs(dry)
	if err != nil {
		return fmt.Errorf("could not resolve inputs for image tag step: %v", err)
	}

	ist := &imageapi.ImageStreamTag{
		ObjectMeta: meta.ObjectMeta{
			Name:      fmt.Sprintf("%s:%s", api.PipelineImageStream, s.config.To),
			Namespace: s.jobSpec.Namespace,
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

	if len(s.config.BaseImage.Cluster) > 0 && s.srcClient != s.dstClient {
		from := coreapi.ObjectReference{Kind: "DockerImage", Name: fmt.Sprintf("%s/%s/%s@sha256:SHA",
			apiCIRegistry, s.config.BaseImage.Namespace, s.config.BaseImage.Name)}
		if !dry {
			from, err = istObjectReference(s.srcClient, s.config.BaseImage)
			if err != nil {
				return fmt.Errorf("failed to reference source image stream tag: %v", err)
			}
		}
		ist.Tag.From = &from
	}

	if dry {
		s.dryLogger.AddImageStreamTag(ist)
		return nil
	}

	if _, err := s.dstClient.ImageStreamTags(s.jobSpec.Namespace).Create(ist); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create imagestreamtag for input image: %v", err)
	}
	// Wait image is ready
	importCtx, cancel := context.WithTimeout(ctx, 35*time.Minute)
	defer cancel()
	if err := wait.PollImmediateUntil(10*time.Second, func() (bool, error) {
		pipeline, err := s.dstClient.ImageStreams(s.jobSpec.Namespace).Get(api.PipelineImageStream, meta.GetOptions{})
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
	is, err := client.ImageStreams(reference.Namespace).Get(reference.Name, meta.GetOptions{})
	if err != nil {
		return coreapi.ObjectReference{}, fmt.Errorf("could not resolve remote image stream: %v", err)
	}
	var repo string
	if len(is.Status.PublicDockerImageRepository) > 0 {
		repo = is.Status.PublicDockerImageRepository
	} else if len(is.Status.DockerImageRepository) > 0 {
		repo = is.Status.DockerImageRepository
	} else {
		return coreapi.ObjectReference{}, fmt.Errorf("remote image stream %s has no accessible image registry value", reference.Name)
	}
	ist, err := client.ImageStreamTags(reference.Namespace).Get(fmt.Sprintf("%s:%s", reference.Name, reference.Tag), meta.GetOptions{})
	if err != nil {
		return coreapi.ObjectReference{}, fmt.Errorf("could not resolve remote image stream tag: %v", err)
	}
	return coreapi.ObjectReference{Kind: "DockerImage", Name: fmt.Sprintf("%s@%s", repo, ist.Image.Name)}, nil
}

func (s *inputImageTagStep) Requires() []api.StepLink {
	return nil
}

func (s *inputImageTagStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func (s *inputImageTagStep) Provides() (api.ParameterMap, api.StepLink) {
	return nil, nil
}

func (s *inputImageTagStep) Name() string { return fmt.Sprintf("[input:%s]", s.config.To) }

func (s *inputImageTagStep) Description() string {
	return fmt.Sprintf("Find the input image %s and tag it into the pipeline", s.config.To)
}

func InputImageTagStep(config api.InputImageTagStepConfiguration, srcClient, dstClient imageclientset.ImageV1Interface, jobSpec *api.JobSpec, dryLogger *DryLogger) api.Step {
	// when source and destination client are the same, we don't need to use external imports
	if srcClient == dstClient {
		config.BaseImage.Cluster = ""
	}
	return &inputImageTagStep{
		config:    config,
		srcClient: srcClient,
		dstClient: dstClient,
		jobSpec:   jobSpec,
		dryLogger: dryLogger,
	}
}
