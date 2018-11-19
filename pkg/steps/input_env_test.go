package steps

import (
	"context"
	"reflect"
	"testing"

	"github.com/openshift/ci-operator/pkg/api"
	"k8s.io/apimachinery/pkg/util/diff"
)

type doneExpectations struct {
	value bool
	err   error
}

type providesExpectations struct {
	params api.ParameterMap
	link   api.StepLink
}

type inputsExpectations struct {
	values api.InputDefinition
	err    error
}

type stepExpectations struct {
	name     string
	requires []api.StepLink
	creates  []api.StepLink
	provides providesExpectations
	inputs   inputsExpectations
	runError error
	done     doneExpectations
}

func examineStep(t *testing.T, step api.Step, expected stepExpectations) {
	// Test the "informative" methods
	if name := step.Name(); name != expected.name {
		t.Errorf("step.Name() mismatch: expected '%s', got '%s'", expected.name, name)
	}
	if desc := step.Description(); len(desc) == 0 {
		t.Errorf("step.Description() returned an empty string")
	}
	if reqs := step.Requires(); !reflect.DeepEqual(expected.requires, reqs) {
		t.Errorf("step.Requires() returned different links:\n%s", diff.ObjectReflectDiff(expected.requires, reqs))
	}
	if creates := step.Creates(); !reflect.DeepEqual(expected.creates, creates) {
		t.Errorf("step.Creates() returned different links:\n%s", diff.ObjectReflectDiff(expected.creates, creates))
	}

	params, link := step.Provides()
	if !reflect.DeepEqual(expected.provides.params, params) {
		t.Errorf("step.Provides returned different params\n%s", diff.ObjectReflectDiff(expected.provides.params, link))
	}
	if !reflect.DeepEqual(expected.provides.link, link) {
		t.Errorf("step.Provides returned different link\n%s", diff.ObjectReflectDiff(expected.provides.link, link))
	}

	inputs, err := step.Inputs(context.Background(), false)
	if !reflect.DeepEqual(expected.inputs.values, inputs) {
		t.Errorf("step.Inputs returned different inputs\n%s", diff.ObjectReflectDiff(expected.inputs.values, inputs))
	}
	if !reflect.DeepEqual(expected.inputs.err, err) {
		t.Errorf("step.Inputs returned different err\n%s", diff.ObjectReflectDiff(expected.inputs.err, err))
	}

	if err := step.Run(context.Background(), false); err != expected.runError {
		t.Errorf("step.Run returned different error: expected %v, got %v", expected.runError, err)
	}

	done, err := step.Done()
	if !reflect.DeepEqual(expected.done.value, done) {
		t.Errorf("step.Done returned %t, expected %t)", done, expected.done.value)
	}
	if !reflect.DeepEqual(expected.inputs.err, err) {
		t.Errorf("step.Done returned different err\n%s", diff.ObjectReflectDiff(expected.done.err, err))
	}
}

func TestInputEnvironmentStep(t *testing.T) {
	name := "le step"
	values := map[string]string{"key": "value", "another key": "another value"}
	links := []api.StepLink{
		api.ExternalImageLink(api.ImageStreamTagReference{
			"cluster.com", "namespace", "name", "tag", "AS",
		}),
	}
	ies := NewInputEnvironmentStep(name, values, links)

	specification := stepExpectations{
		name:     name,
		requires: nil,
		creates:  links,
		provides: providesExpectations{
			params: nil,
			link:   nil,
		},
		inputs: inputsExpectations{
			values: api.InputDefinition{"another value", "value"},
			err:    nil,
		},
		runError: nil,
		done: doneExpectations{
			value: true,
			err:   nil,
		},
	}

	examineStep(t, ies, specification)
}
