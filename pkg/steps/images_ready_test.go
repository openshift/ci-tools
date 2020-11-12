package steps

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestImagesReadyStep(t *testing.T) {
	links := []api.StepLink{someStepLink("ONE")}
	irs := ImagesReadyStep(links)
	specification := stepExpectation{
		name:     "[images]",
		requires: links,
		creates:  []api.StepLink{api.ImagesReadyLink()},
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
			value: true,
			err:   false,
		},
		runError: false,
		postrun: doneExpectation{
			value: true,
			err:   false,
		},
	}

	examineStep(t, irs, specification)
	executeStep(t, irs, execSpecification)
}
