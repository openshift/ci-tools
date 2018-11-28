package steps

import (
	"testing"

	"github.com/openshift/ci-operator/pkg/api"
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
			link:   nil,
		},
		inputs: inputsExpectation{
			values: nil,
			err:    nil,
		},
	}

	execSpecification := executionExpectation{
		prerun: doneExpectation{
			value: true,
			err:   nil,
		},
		runError: nil,
		postrun: doneExpectation{
			value: true,
			err:   nil,
		},
	}

	examineStep(t, irs, specification)
	executeStep(t, irs, execSpecification)
}
