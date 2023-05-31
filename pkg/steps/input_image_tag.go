package steps

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	"github.com/openshift/ci-tools/pkg/util"
)

// inputImageTagStep will ensure that a tag exists
// in the pipeline ImageStream that resolves to
// the base image
type inputImageTagStep struct {
	config  *api.InputImageTagStepConfiguration
	client  loggingclient.LoggingClient
	jobSpec *api.JobSpec

	imageName string
}

func (s *inputImageTagStep) Inputs() (api.InputDefinition, error) {
	if len(s.imageName) > 0 {
		return api.InputDefinition{s.imageName}, nil
	}
	from := imagev1.ImageStreamTag{}
	namespace := s.config.BaseImage.Namespace
	name := fmt.Sprintf("%s:%s", s.config.BaseImage.Name, s.config.BaseImage.Tag)
	if err := s.client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, &from); err != nil {
		return nil, fmt.Errorf("could not resolve base image from %s/%s: %w", namespace, name, err)
	}

	if len(s.config.Sources) > 0 {
		logrus.Debugf("Resolved %s (%s) to %s.", s.config.BaseImage.ISTagName(), s.config.FormattedSources(), from.Image.Name)
	} else {
		logrus.Debugf("Resolved %s to %s.", s.config.BaseImage.ISTagName(), from.Image.Name)
	}
	s.imageName = from.Image.Name
	return api.InputDefinition{from.Image.Name}, nil
}

func (*inputImageTagStep) Validate() error { return nil }

func (s *inputImageTagStep) Run(ctx context.Context) error {
	return results.ForReason("tagging_input_image").ForError(s.run(ctx))
}

func (s *inputImageTagStep) run(ctx context.Context) error {
	logrus.Infof("Tagging %s into %s:%s.", s.config.BaseImage.ISTagName(), api.PipelineImageStream, s.config.To)

	if _, err := s.Inputs(); err != nil {
		return fmt.Errorf("could not resolve inputs for image tag step: %w", err)
	}

	ist := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s:%s", api.PipelineImageStream, s.config.To),
			Namespace: s.jobSpec.Namespace(),
		},
		Tag: &imagev1.TagReference{
			ReferencePolicy: imagev1.TagReferencePolicy{
				Type: imagev1.LocalTagReferencePolicy,
			},
			From: &coreapi.ObjectReference{
				Kind:      "ImageStreamImage",
				Name:      fmt.Sprintf("%s@%s", s.config.BaseImage.Name, s.imageName),
				Namespace: s.config.BaseImage.Namespace,
			},
			ImportPolicy: imagev1.TagImportPolicy{
				ImportMode: imagev1.ImportModePreserveOriginal,
			},
		},
	}

	if err := s.client.Create(ctx, ist); err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create imagestreamtag for input image: %w", err)
	}

	// Wait image is ready
	importCtx, cancel := context.WithTimeout(ctx, 35*time.Minute)
	defer cancel()
	if err := wait.PollImmediateUntil(10*time.Second, func() (bool, error) {
		pipeline := &imagev1.ImageStream{}
		if err := s.client.Get(importCtx, ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: api.PipelineImageStream}, pipeline); err != nil {
			return false, err
		}
		_, exists := util.ResolvePullSpec(pipeline, string(s.config.To), true)
		if !exists {
			logrus.Debugf("Waiting to import %s ...", ist.ObjectMeta.Name)
		}
		return exists, nil
	}, importCtx.Done()); err != nil {
		logrus.WithError(err).Errorf("Could not resolve tag %s in imagestream %s.", s.config.To, api.PipelineImageStream)
		return err
	}
	return nil
}

func (s *inputImageTagStep) Requires() []api.StepLink {
	return nil
}

func (s *inputImageTagStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func (s *inputImageTagStep) Provides() api.ParameterMap {
	tag := s.config.To
	return api.ParameterMap{
		utils.PipelineImageEnvFor(tag): utils.ImageDigestFor(s.client, s.jobSpec.Namespace, api.PipelineImageStream, string(tag)),
	}
}

func (s *inputImageTagStep) Name() string { return s.config.TargetName() }

func (s *inputImageTagStep) Description() string {
	return fmt.Sprintf("Find the input image %s and tag it into the pipeline", s.config.To)
}

func (s *inputImageTagStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func InputImageTagStep(
	config *api.InputImageTagStepConfiguration,
	client loggingclient.LoggingClient,
	jobSpec *api.JobSpec) api.Step {
	// when source and destination client are the same, we don't need to use external imports
	return &inputImageTagStep{
		config:  config,
		client:  client,
		jobSpec: jobSpec,
	}
}
