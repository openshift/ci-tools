package steps

// This file contains helpers useful for testing ci-operator steps

import (
	"context"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/openshift/ci-tools/pkg/api"
)

type doneExpectation struct {
	value bool
	err   bool
}

type providesExpectation struct {
	params map[string]string
}

type inputsExpectation struct {
	values api.InputDefinition
	err    bool
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
	runError bool
	postrun  doneExpectation
}

func someStepLink(as string) api.StepLink {
	return api.ExternalImageLink(api.ImageStreamTagReference{
		Namespace: "namespace",
		Name:      "name",
		Tag:       "tag",
		As:        as,
	})
}

func errorCheck(t *testing.T, message string, expected bool, err error) {
	t.Helper()
	if expected && err == nil {
		t.Errorf("%s: expected to return error, returned nil", message)
	}
	if !expected && err != nil {
		t.Errorf("%s: returned unexpected error: %v", message, err)
	}
}

func examineStep(t *testing.T, step api.Step, expected stepExpectation) {
	t.Helper()
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

	params := step.Provides()
	for expectedKey, expectedValue := range expected.provides.params {
		getFunc, ok := params[expectedKey]
		if !ok {
			t.Errorf("step.Provides: Parameters do not contain '%s' key (expected to return value '%s')", expectedKey, expectedValue)
			continue
		}
		value, err := getFunc()
		if err != nil {
			t.Errorf("step.Provides: params[%s]() returned error: %v", expectedKey, err)
		} else if value != expectedValue {
			t.Errorf("step.Provides: params[%s]() returned '%s', expected to return '%s'", expectedKey, value, expectedValue)
		}
	}

	inputs, err := step.Inputs()
	if !reflect.DeepEqual(expected.inputs.values, inputs) {
		t.Errorf("step.Inputs returned different inputs\n%s", diff.ObjectReflectDiff(expected.inputs.values, inputs))
	}
	errorCheck(t, "step.Inputs", expected.inputs.err, err)
}

func executeStep(t *testing.T, step api.Step, expected executionExpectation) {
	t.Helper()
	errorCheck(t, "step.Run()", expected.runError, step.Run(context.Background(), &api.RunOptions{}))
}
