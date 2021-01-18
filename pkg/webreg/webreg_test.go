package webreg

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/registry"
)

func TestGetDependencyDataItems(t *testing.T) {
	stepWithoutDep := api.TestStep{
		LiteralTestStep: &api.LiteralTestStep{
			As: "step-without-dependency",
		},
	}

	registrySteps := registry.ReferenceByName{
		stepWithoutDep.As: *stepWithoutDep.LiteralTestStep,
	}
	for _, step := range []string{"step-1", "step-2"} {
		for _, image := range []string{"image-1", "image-2"} {
			for _, variable := range []string{"var", "anothervar"} {
				name := fmt.Sprintf("%s-depends-on-%s-under-%s", step, image, variable)
				registrySteps[name] = api.LiteralTestStep{
					As:           name,
					Dependencies: []api.StepDependency{{Name: image, Env: variable}},
				}
			}
		}
	}

	stepPtr := func(name string) *api.LiteralTestStep {
		if step, ok := registrySteps[name]; !ok {
			t.Fatalf("TEST BUG: step '%s' not in test registry", name)
			return nil
		} else {
			return &step
		}
	}

	registryChains := registry.ChainByName{
		"chain-with-step-1-depends-on-image-1-under-var": api.RegistryChain{
			As:    "chain-with-step-1-depends-on-image-1-under-var",
			Steps: []api.TestStep{{LiteralTestStep: stepPtr("step-1-depends-on-image-1-under-var")}},
		},
	}

	testCases := []struct {
		description string
		inputSteps  []api.TestStep
		overrides   api.TestDependencies

		expected map[string]dependencyVars
	}{
		{
			description: "no input, no data",
		},
		{
			description: "step without dependencies, no data",
			inputSteps:  []api.TestStep{stepWithoutDep},
		},
		{
			description: "literal step with dependency",
			inputSteps:  []api.TestStep{{LiteralTestStep: stepPtr("step-1-depends-on-image-1-under-var")}},
			expected: map[string]dependencyVars{
				"image-1": {
					"var": dependencyLine{
						Steps: []string{"step-1-depends-on-image-1-under-var"},
					},
				},
			},
		},
		{
			description: "reference to a step with dependency",
			inputSteps:  []api.TestStep{{Reference: pointer.StringPtr("step-1-depends-on-image-1-under-var")}},
			expected: map[string]dependencyVars{
				"image-1": {
					"var": dependencyLine{
						Steps: []string{"step-1-depends-on-image-1-under-var"},
					},
				},
			},
		},
		{
			description: "reference to a chain that contains a step with dependency",
			inputSteps:  []api.TestStep{{Chain: pointer.StringPtr("chain-with-step-1-depends-on-image-1-under-var")}},
			expected: map[string]dependencyVars{
				"image-1": {
					"var": dependencyLine{
						Steps: []string{"step-1-depends-on-image-1-under-var"},
					},
				},
			},
		},
		{
			description: "two steps depending on same image under same name",
			inputSteps: []api.TestStep{
				{LiteralTestStep: stepPtr("step-1-depends-on-image-1-under-var")},
				{LiteralTestStep: stepPtr("step-2-depends-on-image-1-under-var")},
			},
			expected: map[string]dependencyVars{
				"image-1": {
					"var": dependencyLine{Steps: []string{"step-1-depends-on-image-1-under-var", "step-2-depends-on-image-1-under-var"}},
				},
			},
		},
		{
			description: "two steps depending on same image under different names",
			inputSteps: []api.TestStep{
				{LiteralTestStep: stepPtr("step-1-depends-on-image-1-under-var")},
				{LiteralTestStep: stepPtr("step-1-depends-on-image-1-under-anothervar")},
			},
			expected: map[string]dependencyVars{
				"image-1": {
					"var":        dependencyLine{Steps: []string{"step-1-depends-on-image-1-under-var"}},
					"anothervar": dependencyLine{Steps: []string{"step-1-depends-on-image-1-under-anothervar"}},
				},
			},
		},
		{
			description: "two steps depending on different image under same name",
			inputSteps: []api.TestStep{
				{LiteralTestStep: stepPtr("step-1-depends-on-image-1-under-var")},
				{LiteralTestStep: stepPtr("step-2-depends-on-image-2-under-var")},
			},
			expected: map[string]dependencyVars{
				"image-1": {"var": dependencyLine{Steps: []string{"step-1-depends-on-image-1-under-var"}}},
				"image-2": {"var": dependencyLine{Steps: []string{"step-2-depends-on-image-2-under-var"}}},
			},
		},
		{
			description: "two steps depending on same image under different names, one is overridden",
			inputSteps: []api.TestStep{
				{LiteralTestStep: stepPtr("step-1-depends-on-image-1-under-var")},
				{LiteralTestStep: stepPtr("step-1-depends-on-image-1-under-anothervar")},
			},
			overrides: map[string]string{"anothervar": "workflow-overrode-this"},
			expected: map[string]dependencyVars{
				"image-1": {
					"var": dependencyLine{Steps: []string{"step-1-depends-on-image-1-under-var"}},
				},
				"workflow-overrode-this": {
					"anothervar": dependencyLine{
						Steps:    []string{"step-1-depends-on-image-1-under-anothervar"},
						Override: true,
					},
				},
			},
		},
	}

	t.Parallel()

	for i := range testCases {
		t.Run(testCases[i].description, func(t *testing.T) {
			data := getDependencyDataItems(testCases[i].inputSteps, registrySteps, registryChains, testCases[i].overrides)
			if diff := cmp.Diff(testCases[i].expected, data); diff != "" {
				t.Errorf("%s: data differs from expected:\n%s", testCases[i].description, diff)
			}
		})
	}
}
