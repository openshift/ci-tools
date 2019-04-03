package steps

import (
	"context"

	"github.com/openshift/ci-operator/pkg/api"
)

type imagesReadyLinkStep struct {
	requires, creates []api.StepLink
	name, description string
}

func (s *imagesReadyLinkStep) Inputs(ctx context.Context, dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *imagesReadyLinkStep) Run(ctx context.Context, dry bool) error {
	return nil
}

func (s *imagesReadyLinkStep) Done() (bool, error) {
	return true, nil
}

func (s *imagesReadyLinkStep) Requires() []api.StepLink {
	return s.requires
}

func (s *imagesReadyLinkStep) Creates() []api.StepLink {
	return s.creates
}

func (s *imagesReadyLinkStep) Provides() (api.ParameterMap, api.StepLink) {
	return nil, nil
}

func (s *imagesReadyLinkStep) Name() string { return s.name }

func (s *imagesReadyLinkStep) Description() string {
	return s.description
}

func ImagesReadyStep(requires []api.StepLink) api.Step {
	return newImagesReadyLinkStep(requires, []api.StepLink{api.ImagesReadyLink()}, "[images]", "All images are built and tagged into stable")
}

func PrepublishImagesReadyStep(requires []api.StepLink) api.Step {
	return newImagesReadyLinkStep(requires, nil, "[prepublish]", "All images are built and tagged into the prepublish namespace")
}

func newImagesReadyLinkStep(requires, creates []api.StepLink, name, description string) api.Step {
	return &imagesReadyLinkStep{
		requires:    requires,
		name:        name,
		creates:     creates,
		description: description,
	}
}
