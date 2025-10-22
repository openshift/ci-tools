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
	ResolveWorkflow(name string) (api.MultiStageTestConfigurationLiteral, error)
	ResolveChain(name string) (api.RegistryChain, error)
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
		if _, err := reg.process([]api.TestStep{{Chain: &k}}, sets.New[string](), stackForChain()); err != nil {
			ret = append(ret, err...)
		}
	}
	for k, v := range workflowsByName {
		stack := stackForWorkflow(k, v.Environment, v.Dependencies, v.DNSConfig, v.NodeArchitecture)
		for _, s := range [][]api.TestStep{v.Pre, v.Test, v.Post} {
			if _, err := reg.process(s, sets.New[string](), stack); err != nil {
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
	var overridden [][]api.TestStep
	if config.Workflow != nil {
		var errs []error
		overridden, errs = r.mergeWorkflow(&config)
		if errs != nil {
			return api.MultiStageTestConfigurationLiteral{}, utilerrors.NewAggregate(errs)
		}
	}
	return r.resolveTest(config, stackForTest(name, config.Environment, config.Dependencies, config.DNSConfig, config.NodeArchitecture), overridden)
}

func (r *registry) mergeWorkflow(config *api.MultiStageTestConfiguration) ([][]api.TestStep, []error) {
	var overridden [][]api.TestStep
	workflow, ok := r.workflowsByName[*config.Workflow]
	if !ok {
		return nil, []error{fmt.Errorf("no workflow named %s", *config.Workflow)}
	}
	var errs []error
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
	config.DNSConfig = overwriteIfUnset(workflow.DNSConfig, config.DNSConfig)
	config.Observers = overwriteIfUnset(workflow.Observers, config.Observers)
	config.NodeArchitecture = overwriteIfUnset(workflow.NodeArchitecture, config.NodeArchitecture)

	if l, err := mergeLeases(workflow.Leases, config.Leases); err != nil {
		errs = append(errs, err)
	} else {
		config.Leases = l
	}
	if config.AllowSkipOnSuccess == nil {
		config.AllowSkipOnSuccess = workflow.AllowSkipOnSuccess
	}
	if config.AllowBestEffortPostSteps == nil {
		config.AllowBestEffortPostSteps = workflow.AllowBestEffortPostSteps
	}
	return overridden, errs
}

func (r *registry) resolveTest(
	config api.MultiStageTestConfiguration,
	stack stack,
	overridden [][]api.TestStep,
) (api.MultiStageTestConfigurationLiteral, error) {
	var resolveErrors []error
	expandedFlow := api.MultiStageTestConfigurationLiteral{
		ClusterProfile:           config.ClusterProfile,
		AllowSkipOnSuccess:       config.AllowSkipOnSuccess,
		AllowBestEffortPostSteps: config.AllowBestEffortPostSteps,
		Leases:                   config.Leases,
		DependencyOverrides:      config.DependencyOverrides,
	}

	stack.setNodeArchitectureOverrides(config.NodeArchitectureOverrides)
	if config.Workflow != nil {
		stack.push(stackRecordForTest("workflow/"+*config.Workflow, nil, nil, nil, nil))
	}
	pre, errs := r.process(config.Pre, sets.New[string](), stack)
	expandedFlow.Pre = append(expandedFlow.Pre, pre...)
	resolveErrors = append(resolveErrors, errs...)

	test, errs := r.process(config.Test, sets.New[string](), stack)
	expandedFlow.Test = append(expandedFlow.Test, test...)
	resolveErrors = append(resolveErrors, errs...)

	post, errs := r.process(config.Post, sets.New[string](), stack)
	expandedFlow.Post = append(expandedFlow.Post, post...)
	resolveErrors = append(resolveErrors, errs...)

	observerNames := sets.New[string]()
	for _, step := range append(pre, append(test, post...)...) {
		observerNames = observerNames.Union(sets.New[string](step.Observers...))
	}
	if config.Observers != nil {
		observerNames = observerNames.Union(sets.New[string](config.Observers.Enable...)).Delete(config.Observers.Disable...)
	}

	observers, errs := r.processObservers(observerNames, stack)
	resolveErrors = append(resolveErrors, errs...)
	expandedFlow.Observers = observers

	resolveErrors = append(resolveErrors, stack.checkUnused(&stack.records[0], overridden, r)...)

	if resolveErrors != nil {
		return api.MultiStageTestConfigurationLiteral{}, utilerrors.NewAggregate(resolveErrors)
	}
	return expandedFlow, nil
}

func (r *registry) ResolveWorkflow(name string) (api.MultiStageTestConfigurationLiteral, error) {
	workflow, ok := r.workflowsByName[name]
	if !ok {
		return api.MultiStageTestConfigurationLiteral{}, fmt.Errorf("no workflow named %s", name)
	}
	stack := stackForWorkflow(name, workflow.Environment, workflow.Dependencies, workflow.DNSConfig, workflow.NodeArchitecture)
	ret, err := r.resolveTest(workflow, stack, nil)
	return ret, err
}

func (r *registry) ResolveChain(name string) (api.RegistryChain, error) {
	steps, err := r.processChain(name, sets.New[string](), stack{})
	if err != nil {
		return api.RegistryChain{}, utilerrors.NewAggregate(err)
	}
	ret := api.RegistryChain{}
	for _, x := range steps {
		unique := x
		ret.Steps = append(ret.Steps, api.TestStep{LiteralTestStep: &unique})
	}
	return ret, nil
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

func overwriteIfUnset[T any](dst, src *T) *T {
	if src != nil {
		return src
	}
	return dst
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

func (r *registry) process(steps []api.TestStep, seen sets.Set[string], stack stack) (ret []api.LiteralTestStep, errs []error) {
	for _, step := range steps {
		if step.Chain != nil {
			steps, err := r.processChain(*step.Chain, seen, stack)
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

func (r *registry) processChain(name string, seen sets.Set[string], stack stack) ([]api.LiteralTestStep, []error) {
	chain, ok := r.chainsByName[name]
	if !ok {
		return nil, []error{stack.errorf("unknown step chain: %s", name)}
	}
	rec := stackRecordForStep("chain/"+name, chain.Environment, nil, nil, nil)
	stack.push(rec)
	defer stack.pop()
	ret, err := r.process(chain.Steps, seen, stack)
	err = append(err, stack.checkUnused(&rec, nil, r)...)
	return ret, err
}

func (r *registry) processStep(step *api.TestStep, seen sets.Set[string], stack stack) (ret api.LiteralTestStep, err []error) {
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

	// We always resolve dnsConfig to the highest-level object (job > workflow > step)
	// This pushes the responsibility of handling steps that need custom dnsConfigs to workflow
	// and job authors. This implementation allows for steps to be shared between teams.
	ret.DNSConfig = stack.resolveDNS(ret.DNSConfig)
	ret.NodeArchitecture = stack.resolveNodeArchitecture(ret)
	return ret, errs
}

func (r *registry) processObservers(observerNames sets.Set[string], stack stack) (ret []api.Observer, errs []error) {
	for _, name := range sets.List(observerNames) {
		observer, exists := r.observersByName[name]
		if !exists {
			errs = append(errs, fmt.Errorf("observer %q is referenced but no such observer is configured", name))
		}
		if observer.Environment != nil {
			env := make([]api.StepParameter, 0, len(observer.Environment))
			for _, e := range observer.Environment {
				if v := stack.resolve(e.Name); v != nil {
					e.Default = v
				} else if e.Default == nil && !stack.partial {
					errs = append(errs, stack.errorf("observer/%s: unresolved parameter: %s", observer.Name, e.Name))
				}
				env = append(env, e)
			}
			observer.Environment = env
		}
		ret = append(ret, observer)
	}
	return
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

		// Propagate NodeArchitecture to the MultiStageTestConfiguration
		if step.MultiStageTestConfiguration.NodeArchitecture == nil && step.NodeArchitecture != "" {
			step.MultiStageTestConfiguration.NodeArchitecture = &step.NodeArchitecture
		}

		resolvedConfig, err := resolver.Resolve(step.As, *step.MultiStageTestConfiguration)
		if err != nil {
			return api.ReleaseBuildConfiguration{}, fmt.Errorf("failed resolve MultiStageTestConfiguration: %w", err)
		}
		step.MultiStageTestConfigurationLiteral = &resolvedConfig
		// remove old multi stage config
		step.MultiStageTestConfiguration = nil
		resolvedTests = append(resolvedTests, step)
	}
	config.Tests = resolvedTests
	return config, nil
}
