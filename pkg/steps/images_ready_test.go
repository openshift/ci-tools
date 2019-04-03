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
	executeStep(t, irs, execSpecification, nil)
}

func TestPrepublishImagesReadyStep(t *testing.T) {
	links := []api.StepLink{someStepLink("ONE")}
	irs := PrepublishImagesReadyStep(links)
	specification := stepExpectation{
		name:     "[prepublish]",
		requires: links,
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
	executeStep(t, irs, execSpecification, nil)
}
