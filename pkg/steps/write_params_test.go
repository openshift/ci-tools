package steps

import (
	"os"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestWriteParamsStep(t *testing.T) {
	params := api.NewDeferredParameters(nil)
	params.Add("K1", func() (string, error) { return "V1", nil })
	params.Add("K2", func() (string, error) { return "V:2", nil })
	paramFile, err := os.CreateTemp("", "")
	if err != nil {
		t.Errorf("Failed to create temporary file: %v", err)
	}
	defer os.Remove(paramFile.Name())

	wps := WriteParametersStep(params, paramFile.Name())

	specification := stepExpectation{
		name:     "parameters/write",
		requires: []api.StepLink{api.AllStepsLink()},
		creates:  nil,
		provides: providesExpectation{
			params: nil,
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
	executeStep(t, wps, execSpecification)

	expectedWrittenParams := "K1=V1\nK2='V:2'\n"
	written, err := os.ReadFile(paramFile.Name())
	if err != nil {
		t.Errorf("Failed to read temporary file '%s' after it was supposed to be written into: %v", paramFile.Name(), err)
	}
	writtenParams := string(written)
	if writtenParams != expectedWrittenParams {
		t.Errorf("Params were not written out as expected:\n%s", diff.StringDiff(expectedWrittenParams, writtenParams))
	}
}
