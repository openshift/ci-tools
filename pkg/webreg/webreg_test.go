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

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.description, func(t *testing.T) {
			t.Parallel()
			data := getDependencyDataItems(tc.inputSteps, registrySteps, registryChains, tc.overrides)
			if diff := cmp.Diff(tc.expected, data); diff != "" {
				t.Errorf("%s: data differs from expected:\n%s", tc.description, diff)
			}
		})
	}
}

func TestGetEnvironmentDataItems(t *testing.T) {
	defaultVal := "default"

	stepWithoutVars := api.TestStep{
		LiteralTestStep: &api.LiteralTestStep{
			As: "step-without-vars",
		},
	}

	step1 := api.TestStep{
		LiteralTestStep: &api.LiteralTestStep{
			As: "step-1",
			Environment: []api.StepParameter{
				{
					Name:          "var1",
					Documentation: "var1 documentation",
					Default:       &defaultVal,
				},
			},
		},
	}

	step2 := api.TestStep{
		LiteralTestStep: &api.LiteralTestStep{
			As: "step-2",
			Environment: []api.StepParameter{
				{
					Name:          "var2",
					Documentation: "var2 documentation",
				},
			},
		},
	}

	step3 := api.TestStep{
		LiteralTestStep: &api.LiteralTestStep{
			As: "step-3",
			Environment: []api.StepParameter{
				{
					Name:          "var2",
					Documentation: "var2 documentation",
				},
			},
		},
	}

	registrySteps := registry.ReferenceByName{
		stepWithoutVars.As: *stepWithoutVars.LiteralTestStep,
		step1.As:           *step1.LiteralTestStep,
		step2.As:           *step2.LiteralTestStep,
		step3.As:           *step3.LiteralTestStep,
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
		"chain-with-step-1": api.RegistryChain{
			As:    "chain-with-step-1",
			Steps: []api.TestStep{{LiteralTestStep: stepPtr("step-1")}},
		},
		"chain-with-step1-and-step-2": api.RegistryChain{
			As:    "chain-with-step-1",
			Steps: []api.TestStep{{LiteralTestStep: stepPtr("step-1")}, {LiteralTestStep: stepPtr("step-2")}},
		},
		"chain-with-step1-and-step-2-and-step-3": api.RegistryChain{
			As:    "chain-with-step-1",
			Steps: []api.TestStep{{LiteralTestStep: stepPtr("step-1")}, {LiteralTestStep: stepPtr("step-2")}, {LiteralTestStep: stepPtr("step-3")}},
		},
	}

	testCases := []struct {
		description string
		inputSteps  []api.TestStep

		expected map[string]environmentLine
	}{
		{
			description: "no input, no data",
			expected:    map[string]environmentLine{},
		},
		{
			description: "Chain with single step without vars, no data",
			inputSteps:  []api.TestStep{stepWithoutVars},
			expected:    map[string]environmentLine{},
		},
		{
			description: "Chain with single step with value",
			inputSteps:  []api.TestStep{{LiteralTestStep: stepPtr("step-1")}},
			expected: map[string]environmentLine{
				"var1": {
					Documentation: "var1 documentation",
					Default:       &defaultVal,
					Steps:         []string{"step-1"},
				},
			},
		},
		{
			description: "Chain with two steps with value",
			inputSteps:  []api.TestStep{{LiteralTestStep: stepPtr("step-1")}, {LiteralTestStep: stepPtr("step-2")}},
			expected: map[string]environmentLine{
				"var1": {
					Documentation: "var1 documentation",
					Default:       &defaultVal,
					Steps:         []string{"step-1"},
				},
				"var2": {
					Documentation: "var2 documentation",
					Steps:         []string{"step-2"},
				},
			},
		},
		{
			description: "Chain with three steps with two steps having same value",
			inputSteps:  []api.TestStep{{LiteralTestStep: stepPtr("step-1")}, {LiteralTestStep: stepPtr("step-2")}, {LiteralTestStep: stepPtr("step-3")}},
			expected: map[string]environmentLine{
				"var1": {
					Documentation: "var1 documentation",
					Default:       &defaultVal,
					Steps:         []string{"step-1"},
				},
				"var2": {
					Documentation: "var2 documentation",
					Steps:         []string{"step-2", "step-3"},
				},
			},
		},
	}

	t.Parallel()

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.description, func(t *testing.T) {
			t.Parallel()
			data := getEnvironmentDataItems(tc.inputSteps, registrySteps, registryChains)
			if diff := cmp.Diff(tc.expected, data); diff != "" {
				t.Errorf("%s: data differs from expected:\n%s", tc.description, diff)
			}
		})
	}
}
