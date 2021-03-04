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

func stackForWorkflow(name string, env api.TestEnvironment, deps api.TestDependencies) stack {
	return stack{
		records: []stackRecord{stackRecordForTest("workflow/"+name, env, deps)},
		partial: true,
	}
}

func stackForTest(name string, env api.TestEnvironment, deps api.TestDependencies) stack {
	return stack{records: []stackRecord{stackRecordForTest("test/"+name, env, deps)}}
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

func (s *stack) checkUnused(r *stackRecord) (ret []error) {
	for u := range r.unusedEnv {
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
	name       string
	env        []api.StepParameter
	unusedEnv  sets.String
	deps       []api.StepDependency
	unusedDeps sets.String
}

func stackRecordForStep(name string, env []api.StepParameter, deps []api.StepDependency) stackRecord {
	unusedEnv := sets.NewString()
	for _, x := range env {
		unusedEnv.Insert(x.Name)
	}
	unusedDeps := sets.NewString()
	for _, x := range deps {
		unusedDeps.Insert(x.Env)
	}
	return stackRecord{name: name, env: env, unusedEnv: unusedEnv, deps: deps, unusedDeps: unusedDeps}
}

func stackRecordForTest(name string, env api.TestEnvironment, deps api.TestDependencies) stackRecord {
	params := make([]api.StepParameter, 0, len(env))
	for k, v := range env {
		unique := v
		params = append(params, api.StepParameter{Name: k, Default: &unique})
	}
	dependencies := make([]api.StepDependency, 0, len(deps))
	for k, v := range deps {
		dependencies = append(dependencies, api.StepDependency{Name: v, Env: k})
	}
	return stackRecordForStep(name, params, dependencies)
}
