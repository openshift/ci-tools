package steps

import (
	"context"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
)

type imagesReadyStep struct {
	links []api.StepLink
}

func (s *imagesReadyStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*imagesReadyStep) Validate() error { return nil }

func (s *imagesReadyStep) Run(ctx context.Context, o *api.RunOptions) error {
	return nil
}

func (s *imagesReadyStep) Requires() []api.StepLink {
	return s.links
}

func (s *imagesReadyStep) Creates() []api.StepLink {
	return []api.StepLink{api.ImagesReadyLink()}
}

func (s *imagesReadyStep) Provides() api.ParameterMap {
	return nil
}

func (s *imagesReadyStep) Name() string { return "[images]" }

func (s *imagesReadyStep) Objects() []ctrlruntimeclient.Object {
	return nil
}

func (s *imagesReadyStep) Description() string { return "All images are built and tagged into stable" }

func ImagesReadyStep(links []api.StepLink) api.Step {
	return &imagesReadyStep{
		links: links,
	}
}
