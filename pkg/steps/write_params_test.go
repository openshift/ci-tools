package steps

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/openshift/ci-operator/pkg/api"
	"k8s.io/apimachinery/pkg/util/diff"
)

func TestWriteParamsStep(t *testing.T) {
	params := NewDeferredParameters()
	params.Add("K1", someStepLink("another-step"), func() (string, error) { return "V1", nil })
	params.Add("K2", someStepLink("another-step"), func() (string, error) { return "V:2", nil })
	paramFile, err := ioutil.TempFile("", "")
	if err != nil {
		t.Errorf("Failed to create temporary file: %v", err)
	}
	defer os.Remove(paramFile.Name())

	wps := WriteParametersStep(params, paramFile.Name())

	specification := stepExpectation{
		name:     "parameters/write",
		requires: []api.StepLink{someStepLink("another-step"), someStepLink("another-step")},
		creates:  nil,
		provides: providesExpectation{
			params: nil,
			link:   nil,
		},
		inputs: inputsExpectation{
			values: nil,
			err:    false,
		},
	}

	execSpecification := executionExpectation{
		prerun: doneExpectation{
			value: false,
			err:   false,
		},
		runError: false,
		postrun: doneExpectation{
			value: false,
			err:   false,
		},
	}

	examineStep(t, wps, specification)
	executeStep(t, wps, execSpecification, nil)

	expectedWrittenParams := "K1=V1\nK2='V:2'\n"
	written, err := ioutil.ReadFile(paramFile.Name())
	if err != nil {
		t.Errorf("Failed to read temporary file '%s' after it was supposed to be written into: %v", paramFile.Name(), err)
	}
	writtenParams := string(written)
	if writtenParams != expectedWrittenParams {
		t.Errorf("Params were not written out as expected:\n%s", diff.StringDiff(expectedWrittenParams, writtenParams))
	}
}
