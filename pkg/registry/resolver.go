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

// Validate verifies the internal consistency of steps, chains, and workflows.
// A superset of this validation is performed later when actual test
// configurations are resolved.
func Validate(stepsByName ReferenceByName, chainsByName ChainByName, workflowsByName WorkflowByName) error {
	reg := registry{stepsByName, chainsByName, workflowsByName}
	var ret []error
	for k := range chainsByName {
		if _, err := reg.process([]api.TestStep{{Chain: &k}}, sets.NewString(), stackForChain()); err != nil {
			ret = append(ret, err...)
		}
	}
	for k, v := range workflowsByName {
		stack := stackForWorkflow(k, v.Environment)
		for _, s := range [][]api.TestStep{v.Pre, v.Test, v.Post} {
			if _, err := reg.process(s, sets.NewString(), stack); err != nil {
				ret = append(ret, err...)
			}
		}
		ret = append(ret, stack.records[0].checkUnused(&stack)...)
	}
	return errors.NewAggregate(ret)
}

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
		if config.Environment == nil {
			config.Environment = make(api.TestEnvironment, len(workflow.Environment))
			for k, v := range workflow.Environment {
				config.Environment[k] = v
			}
		}
		if config.AllowSkipOnSuccess == nil {
			config.AllowSkipOnSuccess = workflow.AllowSkipOnSuccess
		}
	}
	expandedFlow := api.MultiStageTestConfigurationLiteral{
		ClusterProfile:     config.ClusterProfile,
		AllowSkipOnSuccess: config.AllowSkipOnSuccess,
	}
	stack := stackForTest(name, config.Environment)
	pre, errs := r.process(config.Pre, sets.NewString(), stack)
	expandedFlow.Pre = append(expandedFlow.Pre, pre...)
	resolveErrors = append(resolveErrors, errs...)

	test, errs := r.process(config.Test, sets.NewString(), stack)
	expandedFlow.Test = append(expandedFlow.Test, test...)
	resolveErrors = append(resolveErrors, errs...)

	post, errs := r.process(config.Post, sets.NewString(), stack)
	expandedFlow.Post = append(expandedFlow.Post, post...)
	resolveErrors = append(resolveErrors, errs...)
	resolveErrors = append(resolveErrors, stack.records[0].checkUnused(&stack)...)
	if resolveErrors != nil {
		return api.MultiStageTestConfigurationLiteral{}, errors.NewAggregate(resolveErrors)
	}
	return expandedFlow, nil
}

type stack struct {
	records []stackRecord
	partial bool
}

func stackForChain() stack {
	return stack{partial: true}
}

func stackForWorkflow(name string, env api.TestEnvironment) stack {
	return stack{
		records: []stackRecord{stackRecordForTest(name, env)},
		partial: true,
	}
}

func stackForTest(name string, env api.TestEnvironment) stack {
	return stack{records: []stackRecord{stackRecordForTest(name, env)}}
}

func (s *stack) push(r stackRecord) {
	s.records = append(s.records, r)
}

func (s *stack) pop() {
	s.records = s.records[:len(s.records)-1]
}

func (s *stack) errorf(format string, args ...interface{}) error {
	var b strings.Builder
	for i := range s.records {
		b.WriteString(s.records[i].name)
		b.WriteString(": ")
	}
	args = append([]interface{}{b.String()}, args...)
	return fmt.Errorf("%s"+format, args...)
}

func (s *stack) resolve(name string) *string {
	for _, r := range s.records {
		for j, e := range r.env {
			if e.Name == name {
				for _, r := range s.records {
					r.unused.Delete(e.Name)
				}
				return r.env[j].Default
			}
		}
	}
	return nil
}

type stackRecord struct {
	name   string
	env    []api.StepParameter
	unused sets.String
}

func stackRecordForStep(name string, env []api.StepParameter) stackRecord {
	unused := sets.NewString()
	for _, x := range env {
		unused.Insert(x.Name)
	}
	return stackRecord{name: name, env: env, unused: unused}
}

func stackRecordForTest(name string, env api.TestEnvironment) stackRecord {
	params := make([]api.StepParameter, 0, len(env))
	for k, v := range env {
		unique := v
		params = append(params, api.StepParameter{Name: k, Default: &unique})
	}
	return stackRecordForStep(name, params)
}

func (r *stackRecord) checkUnused(s *stack) (ret []error) {
	for u := range r.unused {
		ret = append(ret, s.errorf("no step declares parameter %q", u))
	}
	return
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
	rec := stackRecordForStep(name, chain.Environment)
	stack.push(rec)
	defer stack.pop()
	ret, err := r.process(chain.Steps, seen, stack)
	err = append(err, rec.checkUnused(&stack)...)
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
	if ret.Environment != nil {
		env := make([]api.StepParameter, 0, len(ret.Environment))
		for _, e := range ret.Environment {
			if v := stack.resolve(e.Name); v != nil {
				e.Default = v
			} else if e.Default == nil && !stack.partial {
				errs = append(errs, stack.errorf("%s: unresolved parameter: %s", ret.As, e.Name))
			}
			env = append(env, e)
		}
		ret.Environment = env
	}
	return ret, errs
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
