package registry

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
)

type stack struct {
	records []stackRecord
	partial bool
}

func stackForChain() stack {
	return stack{partial: true}
}

func stackForWorkflow(name string, env api.TestEnvironment, deps api.TestDependencies, dnsConfig *api.StepDNSConfig, nodeArchitecture *api.NodeArchitecture) stack {
	return stack{
		records: []stackRecord{stackRecordForTest("workflow/"+name, env, deps, dnsConfig, nodeArchitecture)},
		partial: true,
	}
}

func stackForTest(name string, env api.TestEnvironment, deps api.TestDependencies, dns *api.StepDNSConfig, nodeArchitecture *api.NodeArchitecture) stack {
	return stack{records: []stackRecord{stackRecordForTest("test/"+name, env, deps, dns, nodeArchitecture)}}
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
					r.unusedEnv.Delete(e.Name)
				}
				return r.env[j].Default
			}
		}
	}
	return nil
}

func (s *stack) resolveDep(env string) string {
	for _, r := range s.records {
		for j, e := range r.deps {
			if e.Env == env {
				for _, r := range s.records {
					r.unusedDeps.Delete(e.Env)
				}
				return r.deps[j].Name
			}
		}
	}
	return ""
}

// resolveDNS propagates a dns config down from the highest level object downward
func (s *stack) resolveDNS(dns *api.StepDNSConfig) *api.StepDNSConfig {
	for _, r := range s.records {
		if r.dnsConfig != nil {
			return r.dnsConfig
		}
	}
	// If no overrides are found, return original
	return dns
}

// resolveNodeArchitecture propagates a nodeArchitecture to determine the type of node to utilize for the pod run.
func (s *stack) resolveNodeArchitecture(nodeArchitecture *api.NodeArchitecture) *api.NodeArchitecture {
	for _, r := range s.records {
		if r.nodeArchitecture != nil {
			return r.nodeArchitecture
		}
	}
	return nodeArchitecture
}

// checkUnused emits errors for each unused parameter/dependency in the record.
// `overridden` is an alternative list of steps used to exclude unused errors
// for parameters that exist only in overridden steps.  This can happen if a
// workflow sets a parameter for a step that is replaced by a test.
func (s *stack) checkUnused(r *stackRecord, overridden [][]api.TestStep, registry *registry) (ret []error) {
	if len(r.unusedEnv) == 0 && len(r.unusedDeps) == 0 {
		return nil
	}
	params, deps := sets.New[string](), sets.New[string]()
	for ; len(overridden) != 0; overridden = overridden[1:] {
		for _, s := range overridden[0] {
			if err := registry.iterateSteps(s, func(s *api.LiteralTestStep) {
				for _, e := range s.Environment {
					params.Insert(e.Name)
				}
				for _, d := range s.Dependencies {
					deps.Insert(d.Env)
				}
			}); err != nil {
				ret = append(ret, err)
			}
		}
	}
	if ret != nil {
		return
	}
	for u := range r.unusedEnv {
		if params.Has(u) {
			continue
		}
		var l []string
		for _, sr := range s.records {
			for _, e := range sr.env {
				if e.Name == u {
					l = append(l, sr.name)
					break
				}
			}
		}
		ret = append(ret, s.errorf("parameter %q is overridden in %v but not declared in any step", u, l))
	}
	for u := range r.unusedDeps {
		if deps.Has(u) {
			continue
		}
		var l []string
		for _, sr := range s.records {
			for _, d := range sr.deps {
				if d.Env == u {
					l = append(l, sr.name)
					break
				}
			}
		}
		ret = append(ret, s.errorf("dependency %q is overridden in %v but not declared in any step", u, l))
	}
	return
}

type stackRecord struct {
	name             string
	env              []api.StepParameter
	unusedEnv        sets.Set[string]
	deps             []api.StepDependency
	unusedDeps       sets.Set[string]
	dnsConfig        *api.StepDNSConfig
	nodeArchitecture *api.NodeArchitecture
}

func stackRecordForStep(name string, env []api.StepParameter, deps []api.StepDependency, dns *api.StepDNSConfig, nodeArchitecture *api.NodeArchitecture) stackRecord {
	unusedEnv := sets.New[string]()
	for _, x := range env {
		unusedEnv.Insert(x.Name)
	}
	unusedDeps := sets.New[string]()
	for _, x := range deps {
		unusedDeps.Insert(x.Env)
	}
	return stackRecord{name: name, env: env, unusedEnv: unusedEnv, deps: deps, unusedDeps: unusedDeps, dnsConfig: dns, nodeArchitecture: nodeArchitecture}
}

func stackRecordForTest(name string, env api.TestEnvironment, deps api.TestDependencies, dns *api.StepDNSConfig, nodeArchitecture *api.NodeArchitecture) stackRecord {
	params := make([]api.StepParameter, 0, len(env))
	for k, v := range env {
		unique := v
		params = append(params, api.StepParameter{Name: k, Default: &unique})
	}
	dependencies := make([]api.StepDependency, 0, len(deps))
	for k, v := range deps {
		dependencies = append(dependencies, api.StepDependency{Name: v, Env: k})
	}
	return stackRecordForStep(name, params, dependencies, dns, nodeArchitecture)
}
