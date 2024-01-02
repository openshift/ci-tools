package steps

import (
	"context"
	"sort"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
)

type inputEnvironmentStep struct {
	name   string
	values map[string]string
	links  []api.StepLink
}

// InputEnvironmentStep acts as a shim for a given step, taking a precalculated set of
// inputs and returning those when executed. May be used to substitute a step that does work
// with another that simply reports output.
func InputEnvironmentStep(name string, values map[string]string, links []api.StepLink) api.Step {
	return &inputEnvironmentStep{
		name:   name,
		values: values,
		links:  links,
	}
}

var _ api.Step = &inputEnvironmentStep{}

func (s *inputEnvironmentStep) Inputs() (api.InputDefinition, error) {
	var values []string
	for _, v := range s.values {
		values = append(values, v)
	}
	sort.Strings(values)
	return values, nil
}

func (*inputEnvironmentStep) Validate() error { return nil }

func (s *inputEnvironmentStep) Run(ctx context.Context, o *api.RunOptions) error {
	return nil
}

func (s *inputEnvironmentStep) Name() string {
	return s.name
}

func (s *inputEnvironmentStep) Description() string {
	return "Used to stub out another step in the graph when the outputs are already known."
}

func (s *inputEnvironmentStep) Requires() []api.StepLink {
	return nil
}

func (s *inputEnvironmentStep) Creates() []api.StepLink {
	return s.links
}

func (s *inputEnvironmentStep) Provides() api.ParameterMap {
	return nil
}

func (s *inputEnvironmentStep) Objects() []ctrlruntimeclient.Object {
	return nil
}
