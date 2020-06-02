package registry

import (
	"fmt"
	"strings"

	"github.com/openshift/ci-tools/pkg/api"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
)

type Resolver interface {
	Resolve(name string, config api.MultiStageTestConfiguration) (api.MultiStageTestConfigurationLiteral, error)
}

type ReferenceByName map[string]api.LiteralTestStep
type ChainByName map[string]api.RegistryChain
type WorkflowByName map[string]api.MultiStageTestConfiguration

// registry will hold all the registry information needed to convert between the
// user provided configs referencing the registry and the internal, complete
// representation
type registry struct {
	stepsByName     ReferenceByName
	chainsByName    ChainByName
	workflowsByName WorkflowByName
}

func NewResolver(stepsByName ReferenceByName, chainsByName ChainByName, workflowsByName WorkflowByName) Resolver {
	return &registry{
		stepsByName:     stepsByName,
		chainsByName:    chainsByName,
		workflowsByName: workflowsByName,
	}
}

func (r *registry) Resolve(name string, config api.MultiStageTestConfiguration) (api.MultiStageTestConfigurationLiteral, error) {
	var resolveErrors []error
	if config.Workflow != nil {
		workflow, ok := r.workflowsByName[*config.Workflow]
		if !ok {
			return api.MultiStageTestConfigurationLiteral{}, fmt.Errorf("no workflow named %s", *config.Workflow)
		}
		if config.ClusterProfile == "" {
			config.ClusterProfile = workflow.ClusterProfile
		}
		if config.Pre == nil {
			config.Pre = workflow.Pre
		}
		if config.Test == nil {
			config.Test = workflow.Test
		}
		if config.Post == nil {
			config.Post = workflow.Post
		}
	}
	expandedFlow := api.MultiStageTestConfigurationLiteral{
		ClusterProfile: config.ClusterProfile,
	}
	stack := []stackRecord{stackRecord(name)}
	pre, errs := r.process(config.Pre, sets.NewString(), stack)
	expandedFlow.Pre = append(expandedFlow.Pre, pre...)
	resolveErrors = append(resolveErrors, errs...)

	test, errs := r.process(config.Test, sets.NewString(), stack)
	expandedFlow.Test = append(expandedFlow.Test, test...)
	resolveErrors = append(resolveErrors, errs...)

	post, errs := r.process(config.Post, sets.NewString(), stack)
	expandedFlow.Post = append(expandedFlow.Post, post...)
	resolveErrors = append(resolveErrors, errs...)

	if resolveErrors != nil {
		return api.MultiStageTestConfigurationLiteral{}, errors.NewAggregate(resolveErrors)
	}
	return expandedFlow, nil
}

type stackRecord string

func stackErrorf(s []stackRecord, format string, args ...interface{}) error {
	var b strings.Builder
	for i := range s {
		b.WriteString(string(s[i]))
		b.WriteString(": ")
	}
	args = append([]interface{}{b.String()}, args...)
	return fmt.Errorf("%s"+format, args...)
}

func (r *registry) process(steps []api.TestStep, seen sets.String, stack []stackRecord) (ret []api.LiteralTestStep, errs []error) {
	for _, step := range steps {
		if step.Chain != nil {
			steps, err := r.processChain(&step, seen, stack)
			errs = append(errs, err...)
			ret = append(ret, steps...)
		} else {
			if step, err := r.processStep(&step, seen, stack); err != nil {
				errs = append(errs, err)
			} else {
				ret = append(ret, step)
			}
		}
	}
	return
}

func (r *registry) processChain(step *api.TestStep, seen sets.String, stack []stackRecord) ([]api.LiteralTestStep, []error) {
	name := *step.Chain
	chain, ok := r.chainsByName[name]
	if !ok {
		return nil, []error{stackErrorf(stack, "unknown step chain: %s", name)}
	}
	return r.process(chain.Steps, seen, append(stack, stackRecord(name)))
}

func (r *registry) processStep(step *api.TestStep, seen sets.String, stack []stackRecord) (ret api.LiteralTestStep, err error) {
	if ref := step.Reference; ref != nil {
		var ok bool
		ret, ok = r.stepsByName[*ref]
		if !ok {
			return api.LiteralTestStep{}, stackErrorf(stack, "invalid step reference: %s", *ref)
		}
	} else if step.LiteralTestStep != nil {
		ret = *step.LiteralTestStep
	} else {
		return api.LiteralTestStep{}, stackErrorf(stack, "encountered TestStep where both `Reference` and `LiteralTestStep` are nil")
	}
	if seen.Has(ret.As) {
		return api.LiteralTestStep{}, stackErrorf(stack, "duplicate name: %s", ret.As)
	}
	seen.Insert(ret.As)
	return ret, nil
}

// ResolveConfig uses a resolver to resolve an entire ci-operator config
func ResolveConfig(resolver Resolver, config api.ReleaseBuildConfiguration) (api.ReleaseBuildConfiguration, error) {
	var resolvedTests []api.TestStepConfiguration
	for _, step := range config.Tests {
		// no changes if step is not multi-stage
		if step.MultiStageTestConfiguration == nil {
			resolvedTests = append(resolvedTests, step)
			continue
		}
		resolvedConfig, err := resolver.Resolve(step.As, *step.MultiStageTestConfiguration)
		if err != nil {
			return api.ReleaseBuildConfiguration{}, fmt.Errorf("Failed resolve MultiStageTestConfiguration: %v", err)
		}
		step.MultiStageTestConfigurationLiteral = &resolvedConfig
		// remove old multi stage config
		step.MultiStageTestConfiguration = nil
		resolvedTests = append(resolvedTests, step)
	}
	config.Tests = resolvedTests
	return config, nil
}
