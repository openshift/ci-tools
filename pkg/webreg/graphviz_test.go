package webreg

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/openshift/ci-tools/pkg/util"
)

func TestChainDotFile(t *testing.T) {
	rbac := "ipi-install-rbac"
	install := "ipi-install-install"
	gather := "ipi-deprovision-must-gather"
	deprovision := "ipi-deprovision-deprovision"
	installChain := "ipi-install"
	deprovisionChain := "ipi-deprovision"
	chainOfChains := "chain-of-chains"
	chains := registry.ChainByName{
		installChain: api.RegistryChain{
			As: installChain,
			Steps: []api.TestStep{
				{Reference: &rbac},
				{Reference: &install},
			},
		},
		deprovisionChain: api.RegistryChain{
			As: deprovisionChain,
			Steps: []api.TestStep{
				{Reference: &gather},
				{Reference: &deprovision},
			},
		},
	}
	for _, tc := range []struct {
		name  string
		chain api.RegistryChain
	}{{
		name:  "empty",
		chain: api.RegistryChain{As: "empty"},
	}, {
		name:  installChain,
		chain: chains[installChain],
	}, {
		name:  deprovisionChain,
		chain: chains[deprovisionChain],
	}, {
		name: chainOfChains,
		chain: api.RegistryChain{
			As: chainOfChains,
			Steps: []api.TestStep{
				{Chain: &installChain},
				{Chain: &deprovisionChain},
			},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			c := util.CopyMap(chains)
			c[tc.chain.As] = tc.chain
			actual := chainDotFile(tc.chain.As, c)
			testhelper.CompareWithFixture(t, actual)
		})
	}
}

func TestWorkflowDotFile(t *testing.T) {
	empty := "empty"
	installChain := "ipi-install"
	deprovisionChain := "ipi-deprovision"
	rbac := "ipi-install-rbac"
	install := "ipi-install-install"
	gather := "ipi-deprovision-must-gather"
	deprovision := "ipi-deprovision-deprovision"
	chainOfChains := "chain-of-chains"
	chains := registry.ChainByName{
		"empty": {},
		installChain: {
			Steps: []api.TestStep{
				{Reference: &rbac},
				{Reference: &install},
			},
		},
		deprovisionChain: {
			Steps: []api.TestStep{
				{Reference: &gather},
				{Reference: &deprovision},
			},
		},
		chainOfChains: {
			Steps: []api.TestStep{
				{Chain: &installChain},
				{Chain: &deprovisionChain},
			},
		},
	}
	for _, tc := range []struct {
		name     string
		workflow api.MultiStageTestConfiguration
	}{{
		name:     "empty",
		workflow: api.MultiStageTestConfiguration{},
	}, {
		name: "ipi",
		workflow: api.MultiStageTestConfiguration{
			Pre:  []api.TestStep{{Chain: &installChain}},
			Post: []api.TestStep{{Chain: &deprovisionChain}},
		},
	}, {
		name: "empty-phase",
		workflow: api.MultiStageTestConfiguration{
			Pre: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{As: "pre"},
			}},
			Post: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{As: "post"},
			}},
		},
	}, {
		name: "empty-chain",
		workflow: api.MultiStageTestConfiguration{
			Pre: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{As: "pre"},
			}},
			Test: []api.TestStep{{Chain: &empty}},
			Post: []api.TestStep{{
				LiteralTestStep: &api.LiteralTestStep{As: "post"},
			}},
		},
	}, {
		name: "step-to-chain",
		workflow: api.MultiStageTestConfiguration{
			Pre: []api.TestStep{
				{Reference: &deprovision},
				{Chain: &installChain},
			},
		},
	}, {
		name: "chain-to-step",
		workflow: api.MultiStageTestConfiguration{
			Pre: []api.TestStep{
				{Chain: &installChain},
				{Reference: &deprovision},
			},
		},
	}, {
		name: "chain-of-chains",
		workflow: api.MultiStageTestConfiguration{
			Pre: []api.TestStep{
				{Chain: &chainOfChains},
			},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			workflows := registry.WorkflowByName{tc.name: tc.workflow}
			actual := workflowDotFile(tc.name, workflows, chains, workflowType)
			testhelper.CompareWithFixture(t, actual)
		})
	}
}
