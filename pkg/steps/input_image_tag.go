package steps

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
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
	if s.config.ExternalImage != nil {
		s.imageName = externalImageReference(s.config)
	}
	if len(s.imageName) > 0 {
		return api.InputDefinition{s.imageName}, nil
	}

	if api.IsCreatedForClusterBotJob(s.config.BaseImage.Namespace) {
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
	} else {
		imageName := api.QuayImageReference(s.config.BaseImage)
		logrus.Debugf("Resolved %s to %s.", s.config.BaseImage.ISTagName(), imageName)
		s.imageName = imageName
	}

	return api.InputDefinition{s.imageName}, nil
}

func (*inputImageTagStep) Validate() error { return nil }

func (s *inputImageTagStep) Run(ctx context.Context) error {
	return results.ForReason("tagging_input_image").ForError(s.run(ctx))
}

func (s *inputImageTagStep) run(ctx context.Context) error {
	if _, err := s.Inputs(); err != nil {
		return fmt.Errorf("could not resolve inputs for image tag step: %w", err)
	}

	var objectReferenceName string
	if s.config.ExternalImage != nil {
		externalPullSpec := externalImageReference(s.config)
		logrus.Infof("Tagging %s into %s:%s.", externalPullSpec, api.PipelineImageStream, s.config.To)
		objectReferenceName = externalPullSpec
	} else {
		logrus.Infof("Tagging %s into %s:%s.", s.config.BaseImage.ISTagName(), api.PipelineImageStream, s.config.To)
		objectReferenceName = api.QuayImageReference(s.config.BaseImage)
	}
	from := &coreapi.ObjectReference{
		Kind: "DockerImage",
		Name: objectReferenceName,
	}
	if api.IsCreatedForClusterBotJob(s.config.BaseImage.Namespace) {
		from = &coreapi.ObjectReference{
			Kind:      "ImageStreamImage",
			Name:      fmt.Sprintf("%s@%s", s.config.BaseImage.Name, s.imageName),
			Namespace: s.config.BaseImage.Namespace,
		}
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
			From: from,
			ImportPolicy: imagev1.TagImportPolicy{
				ImportMode: imagev1.ImportModePreserveOriginal,
			},
		},
	}

	if err := s.client.Create(ctx, ist); err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create imagestreamtag for input image: %w", err)
	}

	if err := waitForTagInSpec(ctx, s.client, s.jobSpec.Namespace(), api.PipelineImageStream, string(s.config.To), 3*time.Minute); err != nil {
		return fmt.Errorf("failed to wait for the tag %s to show in the spec of imagestream %s/%s", string(s.config.To), s.jobSpec.Namespace(), api.PipelineImageStream)
	}

	logrus.Debugf("Waiting to import tags on imagestream (after creating pipeline) %s/%s:%s ...", s.jobSpec.Namespace(), api.PipelineImageStream, s.config.To)
	if err := utils.WaitForImportingISTag(ctx, s.client, s.jobSpec.Namespace(), api.PipelineImageStream, nil, sets.New(string(s.config.To)), utils.DefaultImageImportTimeout); err != nil {
		return fmt.Errorf("failed to wait for importing imagestreamtags on %s/%s:%s: %w", s.jobSpec.Namespace(), api.PipelineImageStream, s.config.To, err)
	}
	logrus.Debugf("Imported tags on imagestream (after creating pipeline) %s/%s:%s", s.jobSpec.Namespace(), api.PipelineImageStream, s.config.To)
	return nil
}

// waitForTagInSpec waits for the tag on the image stream are to show in spec
func waitForTagInSpec(ctx context.Context, client ctrlruntimeclient.WithWatch, ns, name, tag string, timeout time.Duration) error {
	obj := &imagev1.ImageStream{}
	getEvaluator := func(tag string) func(obj runtime.Object) (bool, error) {
		return func(obj runtime.Object) (bool, error) {
			switch stream := obj.(type) {
			case *imagev1.ImageStream:
				for _, t := range stream.Spec.Tags {
					if t.Name == tag {
						return true, nil
					}
				}
				logrus.Debugf("Tag %s has not shown up in the spec of imagestream %s/%s, waiting ...", tag, stream.Namespace, stream.Name)
				return false, nil
			default:
				return false, fmt.Errorf("got an event that did not contain an imagestream: %v", obj)
			}
		}
	}
	return kubernetes.WaitForConditionOnObject(ctx, client, ctrlruntimeclient.ObjectKey{Namespace: ns, Name: name}, &imagev1.ImageStreamList{}, obj, getEvaluator(tag), timeout)
}

func externalImageReference(config *api.InputImageTagStepConfiguration) string {
	if config.ExternalImage.PullSpec != "" {
		return config.ExternalImage.PullSpec
	}
	return fmt.Sprintf("%s/%s/%s:%s", config.ExternalImage.Registry, config.ExternalImage.Namespace, config.ExternalImage.Name, config.ExternalImage.Tag)
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
