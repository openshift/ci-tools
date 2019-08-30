package registry

import (
	"fmt"

	api "github.com/openshift/ci-tools/pkg/api"
	types "github.com/openshift/ci-tools/pkg/steps/types"
	"k8s.io/apimachinery/pkg/util/errors"
)

type Resolver interface {
	Resolve(config api.MultiStageTestConfiguration) (types.TestFlow, error)
}

// registry will hold all the registry information needed to convert between the
// user provided configs referencing the registry and the internal, complete
// representation
type registry struct {
	stepsByName map[string]api.LiteralTestStep
}

func NewResolver(stepsByName map[string]api.LiteralTestStep) Resolver {
	return &registry{
		stepsByName: stepsByName,
	}
}

func (r *registry) Resolve(config api.MultiStageTestConfiguration) (types.TestFlow, error) {
	var resolveErrors []error
	expandedFlow := types.TestFlow{
		ClusterProfile: config.ClusterProfile,
	}
	pre, errs := r.process(config.Pre)
	expandedFlow.Pre = append(expandedFlow.Pre, pre...)
	resolveErrors = append(resolveErrors, errs...)

	test, errs := r.process(config.Test)
	expandedFlow.Test = append(expandedFlow.Test, test...)
	resolveErrors = append(resolveErrors, errs...)

	post, errs := r.process(config.Post)
	expandedFlow.Post = append(expandedFlow.Post, post...)
	resolveErrors = append(resolveErrors, errs...)

	if resolveErrors != nil {
		return types.TestFlow{}, errors.NewAggregate(resolveErrors)
	}
	return expandedFlow, nil
}

func (r *registry) process(steps []api.TestStep) (internalSteps []types.TestStep, errs []error) {
	for _, external := range steps {
		var step api.LiteralTestStep
		if external.Reference != nil {
			var err error
			step, err = r.dereference(external)
			if err != nil {
				errs = append(errs, err)
			}
		} else if external.LiteralTestStep != nil {
			step = *external.LiteralTestStep
		} else {
			errs = append(errs, fmt.Errorf("Encountered TestStep where both `Reference` and `LiteralTestStep` are nil"))
			continue
		}
		internalStep := toInternal(step)
		internalSteps = append(internalSteps, internalStep)
	}
	return
}

func (r *registry) dereference(input api.TestStep) (api.LiteralTestStep, error) {
	step, ok := r.stepsByName[*input.Reference]
	if !ok {
		return api.LiteralTestStep{}, fmt.Errorf("invalid step reference: %s", *input.Reference)
	}
	return step, nil
}

func toInternal(input api.LiteralTestStep) types.TestStep {
	return types.TestStep{
		As:          input.As,
		From:        input.From,
		Commands:    input.Commands,
		ArtifactDir: input.ArtifactDir,
		Resources:   input.Resources,
	}
}
