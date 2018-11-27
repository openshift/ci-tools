package steps

// This file contains helpers useful for testing ci-operator steps

import (
	"context"
	"reflect"
	"testing"

	"github.com/openshift/ci-operator/pkg/api"
	"k8s.io/apimachinery/pkg/util/diff"
)

type doneExpectation struct {
	value bool
	err   error
}

type providesExpectation struct {
	params api.ParameterMap
	link   api.StepLink
}

type inputsExpectation struct {
	values api.InputDefinition
	err    error
}

type stepExpectation struct {
	name     string
	requires []api.StepLink
	creates  []api.StepLink
	provides providesExpectation
	inputs   inputsExpectation
}

type executionExpectation struct {
	prerun   doneExpectation
	runError error
	postrun  doneExpectation
}

func someStepLink(as string) api.StepLink {
	return api.ExternalImageLink(api.ImageStreamTagReference{
		Cluster:   "cluster.com",
		Namespace: "namespace",
		Name:      "name",
		Tag:       "tag",
		As:        as,
	})
}

func examineStep(t *testing.T, step api.Step, expected stepExpectation) {
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
		t.Errorf("step.Inputs returned different error: expected %v, got %v", expected.inputs.err, err)
	}
}

func executeStep(t *testing.T, step api.Step, expected executionExpectation) {
	done, err := step.Done()
	if !reflect.DeepEqual(expected.prerun.value, done) {
		t.Errorf("step.Done() before Run() returned %t, expected %t)", done, expected.prerun.value)
	}

	if !reflect.DeepEqual(expected.prerun.err, err) {
		t.Errorf("step.Done() before Run() returned different error: expected %v, got %v", expected.prerun.err, err)
	}

	if err := step.Run(context.Background(), false); err != expected.runError {
		t.Errorf("step.Run returned different error: expected %v, got %v", expected.runError, err)
	}

	done, err = step.Done()
	if !reflect.DeepEqual(expected.postrun.value, done) {
		t.Errorf("step.Done() after Run() returned %t, expected %t)", done, expected.postrun.value)
	}
	if !reflect.DeepEqual(expected.postrun.err, err) {
		t.Errorf("step.Done() after Run() returned different error: expecter %v, got %v", expected.postrun.err, err)
	}
}
