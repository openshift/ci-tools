package steps

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestInputEnvironmentStep(t *testing.T) {
	name := "le step"
	values := map[string]string{"key": "value", "another key": "another value"}
	links := []api.StepLink{someStepLink("name")}
	ies := InputEnvironmentStep(name, values, links)

	specification := stepExpectation{
		name:     name,
		requires: nil,
		creates:  links,
		provides: providesExpectation{
			params: nil,
		},
		inputs: inputsExpectation{
			values: api.InputDefinition{"another value", "value"},
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

	examineStep(t, ies, specification)
	executeStep(t, ies, execSpecification)
}
