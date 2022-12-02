package webreg

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestChainDotFile(t *testing.T) {
	rbac := "ipi-install-rbac"
	install := "ipi-install-install"
	gather := "ipi-deprovision-must-gather"
	deprovision := "ipi-deprovision-deprovision"
	for _, tc := range []struct {
		name  string
		chain api.RegistryChain
	}{{
		name: "ipi-install",
		chain: api.RegistryChain{
			As: "ipi-install",
			Steps: []api.TestStep{
				{Reference: &rbac},
				{Reference: &install},
			},
		},
	}, {
		name: "ipi-deprovision",
		chain: api.RegistryChain{
			As: "ipi-deprovision",
			Steps: []api.TestStep{
				{Reference: &gather},
				{Reference: &deprovision},
			},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			chains := registry.ChainByName{tc.chain.As: tc.chain}
			actual := chainDotFile(tc.chain.As, chains)
			testhelper.CompareWithFixture(t, actual)
		})
	}
}

func TestWorkflowDotFile(t *testing.T) {
	installChain := "ipi-install"
	deprovisionChain := "ipi-deprovision"
	rbac := "ipi-install-rbac"
	install := "ipi-install-install"
	gather := "ipi-deprovision-must-gather"
	deprovision := "ipi-deprovision-deprovision"
	chains := registry.ChainByName{
		"ipi-install": {
			Steps: []api.TestStep{
				{Reference: &rbac},
				{Reference: &install},
			},
		},
		"ipi-deprovision": {
			Steps: []api.TestStep{
				{Reference: &gather},
				{Reference: &deprovision},
			},
		},
	}
	for _, tc := range []struct {
		name     string
		workflow api.MultiStageTestConfiguration
	}{{
		name: "ipi",
		workflow: api.MultiStageTestConfiguration{
			Pre:  []api.TestStep{{Chain: &installChain}},
			Post: []api.TestStep{{Chain: &deprovisionChain}},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			workflows := registry.WorkflowByName{tc.name: tc.workflow}
			actual := workflowDotFile(tc.name, workflows, chains, workflowType)
			testhelper.CompareWithFixture(t, actual)
		})
	}
}
