package steps

import (
	"context"
	"fmt"
	imageapi "github.com/openshift/api/image/v1"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"log"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
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

func (s *outputImageTagStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (s *outputImageTagStep) Run(ctx context.Context) error {
	return results.ForReason("tagging_output_image").ForError(s.run(ctx))
}

func (s *outputImageTagStep) run(ctx context.Context) error {
	toNamespace := s.namespace()
	if string(s.config.From) == s.config.To.Tag && toNamespace == s.jobSpec.Namespace() && s.config.To.Name == api.StableImageStream {
		log.Printf("Tagging %s into %s", s.config.From, s.config.To.Name)
	} else {
		log.Printf("Tagging %s into %s/%s:%s", s.config.From, toNamespace, s.config.To.Name, s.config.To.Tag)
	}
	from, err := s.istClient.ImageStreamTags(s.jobSpec.Namespace()).Get(context.TODO(), fmt.Sprintf("%s:%s", api.PipelineImageStream, s.config.From), meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not resolve base image: %w", err)
	}
	ist := s.imageStreamTag(from.Image.Name)

	// ensure that the image stream tag points to the correct input, retry
	// on conflict, and do nothing if another user creates before us
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		_, err := s.istClient.ImageStreamTags(toNamespace).Update(ctx, ist, meta.UpdateOptions{})
		if errors.IsNotFound(err) {
			_, err = s.istClient.ImageStreamTags(toNamespace).Create(ctx, ist, meta.CreateOptions{})
		}
		return err
	}); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("could not update output imagestreamtag: %w", err)
	}
	return nil
}

func (s *outputImageTagStep) Requires() []api.StepLink {
	return []api.StepLink{
		api.InternalImageLink(s.config.From),
		// Release input and import steps do not handle the
		// case when other steps are publishing tags to the
		// stable stream. Generally, this is not an issue as
		// the former run at the start of execution and the
		// latter only once images are built. However, in
		// specific configurations, authors may create an
		// execution graph where we race.
		api.StableImagesLink(api.LatestReleaseName),
	}
}

func (s *outputImageTagStep) Creates() []api.StepLink {
	if len(s.config.To.As) > 0 {
		return []api.StepLink{api.ExternalImageLink(s.config.To), api.InternalImageLink(api.PipelineImageStreamTagReference(s.config.To.As))}
	}
	return []api.StepLink{api.ExternalImageLink(s.config.To)}
}

func (s *outputImageTagStep) Provides() api.ParameterMap {
	if len(s.config.To.As) == 0 {
		return nil
	}
	return api.ParameterMap{
		utils.StableImageEnv(s.config.To.As): utils.ImageDigestFor(s.isClient, func() string {
			return s.config.To.Namespace
		}, s.config.To.Name, s.config.To.Tag),
	}
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
	return s.jobSpec.Namespace()
}

func (s *outputImageTagStep) imageStreamTag(fromImage string) *imageapi.ImageStreamTag {
	return &imageapi.ImageStreamTag{
		ObjectMeta: meta.ObjectMeta{
			Name:      fmt.Sprintf("%s:%s", s.config.To.Name, s.config.To.Tag),
			Namespace: s.namespace(),
		},
		Tag: &imageapi.TagReference{
			ReferencePolicy: imageapi.TagReferencePolicy{
				Type: imageapi.LocalTagReferencePolicy,
			},
			From: &coreapi.ObjectReference{
				Kind:      "ImageStreamImage",
				Name:      fmt.Sprintf("%s@%s", api.PipelineImageStream, fromImage),
				Namespace: s.jobSpec.Namespace(),
			},
		},
	}
}

func OutputImageTagStep(config api.OutputImageTagStepConfiguration, istClient imageclientset.ImageStreamTagsGetter, isClient imageclientset.ImageStreamsGetter, jobSpec *api.JobSpec) api.Step {
	return &outputImageTagStep{
		config:    config,
		istClient: istClient,
		isClient:  isClient,
		jobSpec:   jobSpec,
	}
}
