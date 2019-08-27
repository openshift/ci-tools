package registry

import (
	api "github.com/openshift/ci-tools/pkg/api"
	types "github.com/openshift/ci-tools/pkg/steps/types"
	"k8s.io/apimachinery/pkg/util/errors"
)

type Resolver interface {
	Resolve(config api.MultiStageTestConfiguration) (types.TestFlow, error)
}

// Registry will hold all the registry information needed to convert between the
// user provided configs referencing the registry and the internal, complete
// representation
type registry struct{}

func NewResolver() Resolver {
	return &registry{}
}

func (r *registry) Resolve(config api.MultiStageTestConfiguration) (types.TestFlow, error) {
	var resolveErrors []error
	expandedFlow := types.TestFlow{
		ClusterProfile: config.ClusterProfile,
	}
	for _, external := range config.Pre {
		newStep := toInternal(external)
		expandedFlow.Pre = append(expandedFlow.Pre, newStep)
	}
	for _, external := range config.Test {
		newStep := toInternal(external)
		expandedFlow.Test = append(expandedFlow.Test, newStep)
	}
	for _, external := range config.Post {
		newStep := toInternal(external)
		expandedFlow.Post = append(expandedFlow.Post, newStep)
	}
	// This currently never gets run as we don't have any situations that can result in errors yet.
	// This will become used as we add more functionality.
	if resolveErrors != nil {
		return types.TestFlow{}, errors.NewAggregate(resolveErrors)
	}
	return expandedFlow, nil
}

func toInternal(input api.TestStep) types.TestStep {
	return types.TestStep{
		As:          input.As,
		From:        input.From,
		Commands:    input.Commands,
		ArtifactDir: input.ArtifactDir,
		Resources:   input.Resources,
	}
}
