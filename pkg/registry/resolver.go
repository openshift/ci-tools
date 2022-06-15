package registry

import (
	"fmt"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/validation"
)

type Resolver interface {
	Resolve(name string, config api.MultiStageTestConfiguration) (api.MultiStageTestConfigurationLiteral, error)
}

type ReferenceByName map[string]api.LiteralTestStep
type ChainByName map[string]api.RegistryChain
type WorkflowByName map[string]api.MultiStageTestConfiguration
type ObserverByName map[string]api.Observer

// Validate verifies the internal consistency of steps, chains, and workflows.
// A superset of this validation is performed later when actual test
// configurations are resolved.
func Validate(stepsByName ReferenceByName, chainsByName ChainByName, workflowsByName WorkflowByName, observersByName ObserverByName) error {
	reg := registry{stepsByName, chainsByName, workflowsByName, observersByName}
	var ret []error
	for k := range chainsByName {
		if _, err := reg.process([]api.TestStep{{Chain: &k}}, sets.NewString(), stackForChain()); err != nil {
			ret = append(ret, err...)
		}
	}
	for k, v := range workflowsByName {
		stack := stackForWorkflow(k, v.Environment, v.Dependencies)
		for _, s := range [][]api.TestStep{v.Pre, v.Test, v.Post} {
			if _, err := reg.process(s, sets.NewString(), stack); err != nil {
				ret = append(ret, err...)
			}
		}
		ret = append(ret, stack.checkUnused(&stack.records[0], nil, &reg)...)
	}
	for _, v := range observersByName {
		ret = append(ret, validation.Observer(v)...)
	}
	return utilerrors.NewAggregate(ret)
}

// registry will hold all the registry information needed to convert between the
// user provided configs referencing the registry and the internal, complete
// representation
type registry struct {
	stepsByName     ReferenceByName
	chainsByName    ChainByName
	workflowsByName WorkflowByName
	observersByName ObserverByName
}

func NewResolver(stepsByName ReferenceByName, chainsByName ChainByName, workflowsByName WorkflowByName, observersByName ObserverByName) Resolver {
	return &registry{
		stepsByName:     stepsByName,
		chainsByName:    chainsByName,
		workflowsByName: workflowsByName,
		observersByName: observersByName,
	}
}

func (r *registry) Resolve(name string, config api.MultiStageTestConfiguration) (api.MultiStageTestConfigurationLiteral, error) {
	var resolveErrors []error
	var overridden [][]api.TestStep
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
		} else {
			overridden = append(overridden, workflow.Pre)
		}
		if config.Test == nil {
			config.Test = workflow.Test
		} else {
			overridden = append(overridden, workflow.Test)
		}
		if config.Post == nil {
			config.Post = workflow.Post
		} else {
			overridden = append(overridden, workflow.Post)
		}
		config.Environment = mergeEnvironments(workflow.Environment, config.Environment)
		config.Dependencies = mergeDependencies(workflow.Dependencies, config.Dependencies)
		config.DependencyOverrides = mergeDependencyOverrides(workflow.DependencyOverrides, config.DependencyOverrides)
		if l, err := mergeLeases(workflow.Leases, config.Leases); err != nil {
			resolveErrors = append(resolveErrors, err)
		} else {
			config.Leases = l
		}
		if config.AllowSkipOnSuccess == nil {
			config.AllowSkipOnSuccess = workflow.AllowSkipOnSuccess
		}
		if config.AllowBestEffortPostSteps == nil {
			config.AllowBestEffortPostSteps = workflow.AllowBestEffortPostSteps
		}
	}
	expandedFlow := api.MultiStageTestConfigurationLiteral{
		ClusterProfile:           config.ClusterProfile,
		AllowSkipOnSuccess:       config.AllowSkipOnSuccess,
		AllowBestEffortPostSteps: config.AllowBestEffortPostSteps,
		Leases:                   config.Leases,
		DependencyOverrides:      config.DependencyOverrides,
	}
	stack := stackForTest(name, config.Environment, config.Dependencies)
	if config.Workflow != nil {
		stack.push(stackRecordForTest("workflow/"+*config.Workflow, nil, nil))
	}
	pre, errs := r.process(config.Pre, sets.NewString(), stack)
	expandedFlow.Pre = append(expandedFlow.Pre, pre...)
	resolveErrors = append(resolveErrors, errs...)

	test, errs := r.process(config.Test, sets.NewString(), stack)
	expandedFlow.Test = append(expandedFlow.Test, test...)
	resolveErrors = append(resolveErrors, errs...)

	post, errs := r.process(config.Post, sets.NewString(), stack)
	expandedFlow.Post = append(expandedFlow.Post, post...)
	resolveErrors = append(resolveErrors, errs...)
	resolveErrors = append(resolveErrors, stack.checkUnused(&stack.records[0], overridden, r)...)

	observerNames := sets.NewString()
	for _, step := range append(pre, append(test, post...)...) {
		observerNames = observerNames.Union(sets.NewString(step.Observers...))
	}
	if config.Observers != nil {
		observerNames = observerNames.Union(sets.NewString(config.Observers.Enable...)).Delete(config.Observers.Disable...)
	}
	var observers []api.Observer
	for _, name := range observerNames.List() {
		observer, exists := r.observersByName[name]
		if !exists {
			resolveErrors = append(resolveErrors, fmt.Errorf("observer %q is referenced but no such observer is configured", name))
		}
		observers = append(observers, observer)
	}
	expandedFlow.Observers = observers
	if resolveErrors != nil {
		return api.MultiStageTestConfigurationLiteral{}, utilerrors.NewAggregate(resolveErrors)
	}
	return expandedFlow, nil
}

// mergeEnvironments joins two environment maps.
// A copy of `dst` is returned with elements overwritten by those in `src` if
// they target the same variable.
func mergeEnvironments(dst api.TestEnvironment, src api.TestEnvironment) api.TestEnvironment {
	return mergeMaps(dst, src)
}

// mergeDependencies joins to dependency maps.
// A copy of `dst` is returned with elements overwritten by those in `src` if
// they target the same variable.
func mergeDependencies(dst api.TestDependencies, src api.TestDependencies) api.TestDependencies {
	return mergeMaps(dst, src)
}

// mergeDependencyOverrides joins two dependency_override maps.
// A copy of `dst` is returned with elements overwritten by those in `src` if
// they target the same variable.
func mergeDependencyOverrides(dst api.DependencyOverrides, src api.DependencyOverrides) api.DependencyOverrides {
	return mergeMaps(dst, src)
}

func mergeMaps(dst map[string]string, src map[string]string) map[string]string {
	if dst == nil && src == nil {
		return nil
	}
	ret := map[string]string{}
	for k, v := range dst {
		ret[k] = v
	}
	for k, v := range src {
		ret[k] = v
	}
	return ret
}

// mergeLeases joins two lease lists, checking for duplicates.
func mergeLeases(dst, src []api.StepLease) ([]api.StepLease, error) {
	var ret []api.StepLease
	seen := make(map[string]*api.StepLease)
	var dup []string
	for i := range dst {
		ret = append(ret, dst[i])
		seen[dst[i].Env] = &dst[i]
	}
	for i := range src {
		if p, ok := seen[src[i].Env]; ok {
			if *p != src[i] {
				dup = append(dup, src[i].Env)
			}
			continue
		}
		ret = append(ret, src[i])
		seen[src[i].Env] = &src[i]
	}
	if dup != nil {
		return nil, fmt.Errorf("cannot override workflow environment variable for lease(s): %v", dup)
	}
	return ret, nil
}

func (r *registry) process(steps []api.TestStep, seen sets.String, stack stack) (ret []api.LiteralTestStep, errs []error) {
	for _, step := range steps {
		if step.Chain != nil {
			steps, err := r.processChain(&step, seen, stack)
			errs = append(errs, err...)
			ret = append(ret, steps...)
		} else {
			step, err := r.processStep(&step, seen, stack)
			errs = append(errs, err...)
			if err == nil {
				ret = append(ret, step)
			}
		}
	}
	return
}

func (r *registry) processChain(step *api.TestStep, seen sets.String, stack stack) ([]api.LiteralTestStep, []error) {
	name := *step.Chain
	chain, ok := r.chainsByName[name]
	if !ok {
		return nil, []error{stack.errorf("unknown step chain: %s", name)}
	}
	rec := stackRecordForStep("chain/"+name, chain.Environment, nil)
	stack.push(rec)
	defer stack.pop()
	ret, err := r.process(chain.Steps, seen, stack)
	err = append(err, stack.checkUnused(&rec, nil, r)...)
	return ret, err
}

func (r *registry) processStep(step *api.TestStep, seen sets.String, stack stack) (ret api.LiteralTestStep, err []error) {
	if ref := step.Reference; ref != nil {
		var ok bool
		ret, ok = r.stepsByName[*ref]
		if !ok {
			return api.LiteralTestStep{}, []error{stack.errorf("invalid step reference: %s", *ref)}
		}
	} else if step.LiteralTestStep != nil {
		ret = *step.LiteralTestStep
	} else {
		return api.LiteralTestStep{}, []error{stack.errorf("encountered TestStep where both `Reference` and `LiteralTestStep` are nil")}
	}
	if seen.Has(ret.As) {
		return api.LiteralTestStep{}, []error{stack.errorf("duplicate name: %s", ret.As)}
	}
	seen.Insert(ret.As)
	var errs []error
	if ret.Leases != nil {
		ret.Leases = append([]api.StepLease(nil), ret.Leases...)
	}
	if ret.Environment != nil {
		env := make([]api.StepParameter, 0, len(ret.Environment))
		for _, e := range ret.Environment {
			if v := stack.resolve(e.Name); v != nil {
				e.Default = v
			} else if e.Default == nil && !stack.partial {
				errs = append(errs, stack.errorf("step/%s: unresolved parameter: %s", ret.As, e.Name))
			}
			env = append(env, e)
		}
		ret.Environment = env
	}
	if ret.Dependencies != nil {
		deps := make([]api.StepDependency, 0, len(ret.Dependencies))
		for _, e := range ret.Dependencies {
			if v := stack.resolveDep(e.Env); v != "" {
				e.Name = v
			}
			deps = append(deps, e)
		}
		ret.Dependencies = deps
	}
	return ret, errs
}

// iterateSteps calls a function for each leaf child of a step.
func (r *registry) iterateSteps(s api.TestStep, f func(*api.LiteralTestStep)) error {
	switch {
	case s.Chain != nil:
		c, ok := r.chainsByName[*s.Chain]
		if !ok {
			return fmt.Errorf("invalid reference: %s", *s.Reference)
		}
		for _, s := range c.Steps {
			if err := r.iterateSteps(s, f); err != nil {
				return err
			}
		}
	case s.Reference != nil:
		r, ok := r.stepsByName[*s.Reference]
		if !ok {
			return fmt.Errorf("invalid reference: %s", *s.Reference)
		}
		f(&r)
	case s.LiteralTestStep != nil:
		f(s.LiteralTestStep)
	}
	return nil
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
			return api.ReleaseBuildConfiguration{}, fmt.Errorf("Failed resolve MultiStageTestConfiguration: %w", err)
		}
		step.MultiStageTestConfigurationLiteral = &resolvedConfig
		// remove old multi stage config
		step.MultiStageTestConfiguration = nil
		resolvedTests = append(resolvedTests, step)
	}
	config.Tests = resolvedTests
	return config, nil
}
