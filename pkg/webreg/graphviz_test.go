package webreg

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestChainDotFile(t *testing.T) {
	_, chains, _, _, _, _, err := load.Registry("../../test/multistage-registry/registry", load.RegistryFlag(0))
	if err != nil {
		t.Fatalf("Failed to load registry: %v", err)
	}
	for _, tc := range []struct {
		name, chain string
	}{{
		name:  "ipi-install",
		chain: "ipi-install",
	}, {
		name:  "ipi-deprovision",
		chain: "ipi-deprovision",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			actual := chainDotFile(tc.chain, chains)
			testhelper.CompareWithFixture(t, actual)
		})
	}
}

func TestWorkflowDotFile(t *testing.T) {
	_, chains, workflows, _, _, _, err := load.Registry("../../test/multistage-registry/registry", load.RegistryFlag(0))
	if err != nil {
		t.Fatalf("Failed to load registry: %v", err)
	}
	for _, tc := range []struct {
		name, workflow string
	}{{
		name:     "ipi",
		workflow: "ipi",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			actual := workflowDotFile(tc.name, workflows, chains, workflowType)
			testhelper.CompareWithFixture(t, actual)
		})
	}
}
