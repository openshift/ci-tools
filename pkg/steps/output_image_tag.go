package steps

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	crcontrollerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

// outputImageTagStep will ensure that a tag exists
// in the named ImageStream that resolves to the built
// pipeline image
type outputImageTagStep struct {
	config  api.OutputImageTagStepConfiguration
	client  loggingclient.LoggingClient
	jobSpec *api.JobSpec
}

func (s *outputImageTagStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*outputImageTagStep) Validate() error { return nil }

func (s *outputImageTagStep) Run(ctx context.Context) error {
	return results.ForReason("tagging_output_image").ForError(s.run(ctx))
}

func (s *outputImageTagStep) run(ctx context.Context) error {
	toNamespace := s.namespace()
	if string(s.config.From) == s.config.To.Tag && toNamespace == s.jobSpec.Namespace() && s.config.To.Name == api.StableImageStream {
		logrus.Infof("Tagging %s into %s", s.config.From, s.config.To.Name)
	} else {
		logrus.Infof("Tagging %s into %s", s.config.From, s.config.To.ISTagName())
	}
	from := &imagev1.ImageStreamTag{}
	namespace := s.jobSpec.Namespace()
	name := fmt.Sprintf("%s:%s", api.PipelineImageStream, s.config.From)
	if err := s.client.Get(ctx, crclient.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, from); err != nil {
		return fmt.Errorf("could not resolve base image from %s/%s: %w", namespace, name, err)
	}
	refPolicy := imagev1.LocalTagReferencePolicy
	if s.config.To.ReferencePolicy.Type != "" {
		refPolicy = s.config.To.ReferencePolicy.Type
	}
	desired := s.imageStreamTag(from.Image.Name, refPolicy)
	ist := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: desired.ObjectMeta.Namespace,
			Name:      desired.ObjectMeta.Name,
		},
	}

	// Retry on conflicts with exponential backoff to avoid thundering. Note that `Patch` is
	// not supposed return a conflict so in theory we should not need it but we do:
	// > Clayton Coleman  6 hours ago
	// > i think we may have found a bug in kube, which is exciting
	if waitErr := wait.ExponentialBackoff(wait.Backoff{Steps: 4, Factor: 2, Duration: time.Second}, func() (bool, error) {
		_, err := crcontrollerutil.CreateOrPatch(ctx, s.client, ist, func() error {
			ist.Tag = desired.Tag
			return nil
		})
		switch {
		case err != nil && errors.IsConflict(err):
			return false, nil
		case err != nil && errors.IsAlreadyExists(err):
			return true, nil
		case err != nil:
			return false, err
		}
		return true, nil
	}); waitErr != nil {
		return fmt.Errorf("could not upsert output imagestreamtag: %w", waitErr)
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
		api.ReleaseImagesLink(api.LatestReleaseName),
	}
}

func (s *outputImageTagStep) Creates() (ret []api.StepLink) {
	ret = append(ret, api.ExternalImageLink(s.config.To))
	if len(s.config.To.As) > 0 {
		ret = append(ret, api.InternalImageLink(api.PipelineImageStreamTagReference(s.config.To.As)))
	}
	return
}

func (s *outputImageTagStep) Provides() api.ParameterMap {
	if len(s.config.To.As) == 0 {
		return nil
	}
	return api.ParameterMap{
		utils.StableImageEnv(s.config.To.As): utils.ImageDigestFor(s.client, func() string {
			return s.config.To.Namespace
		}, s.config.To.Name, s.config.To.Tag),
	}
}

func (s *outputImageTagStep) Name() string { return s.config.TargetName() }

func (s *outputImageTagStep) Description() string {
	if len(s.config.To.As) == 0 {
		return fmt.Sprintf("Tag the image %s into the image stream tag %s:%s", s.config.From, s.config.To.Name, s.config.To.Tag)
	}
	return fmt.Sprintf("Tag the image %s into the stable image stream", s.config.From)
}

func (s *outputImageTagStep) Objects() []crclient.Object {
	return s.client.Objects()
}

func (s *outputImageTagStep) namespace() string {
	if len(s.config.To.Namespace) != 0 {
		return s.config.To.Namespace
	}
	return s.jobSpec.Namespace()
}

func (s *outputImageTagStep) imageStreamTag(fromImage string, referencePolicy imagev1.TagReferencePolicyType) *imagev1.ImageStreamTag {
	if referencePolicy == "" {
		referencePolicy = imagev1.LocalTagReferencePolicy
	} else if referencePolicy == imagev1.SourceTagReferencePolicy || referencePolicy == "source" {
		referencePolicy = imagev1.SourceTagReferencePolicy
	}
	return &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s:%s", s.config.To.Name, s.config.To.Tag),
			Namespace: s.namespace(),
		},
		Tag: &imagev1.TagReference{
			ReferencePolicy: imagev1.TagReferencePolicy{
				Type: referencePolicy,
			},
			ImportPolicy: imagev1.TagImportPolicy{
				ImportMode: imagev1.ImportModePreserveOriginal,
			},
			From: &coreapi.ObjectReference{
				Kind:      "ImageStreamImage",
				Name:      fmt.Sprintf("%s@%s", api.PipelineImageStream, fromImage),
				Namespace: s.jobSpec.Namespace(),
			},
		},
	}
}

func OutputImageTagStep(config api.OutputImageTagStepConfiguration, client loggingclient.LoggingClient, jobSpec *api.JobSpec) api.Step {
	return &outputImageTagStep{
		config:  config,
		client:  client,
		jobSpec: jobSpec,
	}
}
