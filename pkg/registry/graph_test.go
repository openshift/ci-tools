package registry

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/api"
	"k8s.io/apimachinery/pkg/util/sets"
)

// These maps contain the representation of ../../test/multistage-registry/registry with the
// minimum of amount of information necessary to create a graph
var ipiInstallInstall = "ipi-install-install"
var ipiInstallRBAC = "ipi-install-rbac"
var ipiDeprovisionMustGather = "ipi-deprovision-must-gather"
var ipiDeprovisionDeprovision = "ipi-deprovision-deprovision"
var ipiInstall = "ipi-install"
var ipiDeprovision = "ipi-deprovision"
var ipi = "ipi"

var referenceMap = ReferenceByName{
	ipiInstallInstall:         {},
	ipiInstallRBAC:            {},
	ipiDeprovisionDeprovision: {},
	ipiDeprovisionMustGather:  {},
}
var chainMap = ChainByName{
	ipiInstall: {{
		Reference: &ipiInstallInstall,
	}, {
		Reference: &ipiInstallRBAC,
	}},
	ipiDeprovision: {{
		Reference: &ipiDeprovisionMustGather,
	}, {
		Reference: &ipiDeprovisionDeprovision,
	}},
}
var workflowMap = WorkflowByName{
	ipi: {
		Pre: []api.TestStep{{
			Chain: &ipiInstall,
		}},
		Post: []api.TestStep{{
			Chain: &ipiDeprovision,
		}},
	},
}

func TestAncestorNames(t *testing.T) {
	testCases := []struct {
		name string
		set  sets.String
	}{{
		name: ipi,
		set:  sets.String{},
	}, {
		name: ipiInstall,
		set: sets.String{
			ipi: sets.Empty{},
		},
	}, {
		name: ipiDeprovisionMustGather,
		set: sets.String{
			ipi:            sets.Empty{},
			ipiDeprovision: sets.Empty{},
		},
	}}

	graph, err := NewGraph(referenceMap, chainMap, workflowMap)
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}
	for _, testCase := range testCases {
		node := graph[testCase.name]
		if !testCase.set.Equal(node.AncestorNames()) {
			t.Errorf("%s: ancestor sets not equal", testCase.name)
		}
	}
}

func TestDescendantNames(t *testing.T) {
	testCases := []struct {
		name string
		set  sets.String
	}{{
		name: ipi,
		set: sets.String{
			ipiInstall:                sets.Empty{},
			ipiInstallInstall:         sets.Empty{},
			ipiInstallRBAC:            sets.Empty{},
			ipiDeprovision:            sets.Empty{},
			ipiDeprovisionMustGather:  sets.Empty{},
			ipiDeprovisionDeprovision: sets.Empty{},
		},
	}, {
		name: ipiInstall,
		set: sets.String{
			ipiInstallInstall: sets.Empty{},
			ipiInstallRBAC:    sets.Empty{},
		},
	}, {
		name: ipiDeprovisionMustGather,
		set:  sets.String{},
	}}

	graph, err := NewGraph(referenceMap, chainMap, workflowMap)
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}
	for _, testCase := range testCases {
		node := graph[testCase.name]
		if !testCase.set.Equal(node.DescendantNames()) {
			t.Errorf("%s: descendant sets not equal", testCase.name)
		}
	}
}

func TestHasCycles(t *testing.T) {
	chain1 := &chainNode{
		nodeWithName:     newNodeWithName("chain1"),
		nodeWithParents:  newNodeWithParents(),
		nodeWithChildren: newNodeWithChildren(),
	}
	chain2 := &chainNode{
		nodeWithName:     newNodeWithName("chain2"),
		nodeWithParents:  newNodeWithParents(),
		nodeWithChildren: newNodeWithChildren(),
	}
	chain1.addChainChild(chain2)
	chain3 := &chainNode{
		nodeWithName:     newNodeWithName("chain3"),
		nodeWithParents:  newNodeWithParents(),
		nodeWithChildren: newNodeWithChildren(),
	}
	chain2.addChainChild(chain3)

	chainSet := make(chainNodeSet)
	chainSet.insert(chain1)
	chainSet.insert(chain2)
	chainSet.insert(chain3)

	// No cycles currently exist; should pass
	for chain := range chainSet {
		if err := hasCycles(chain, nil, nil); err != nil {
			t.Errorf("Error reported unexpectedly: %v", err)
		}
	}
	// Add a cycle
	chain3.addChainChild(chain1)
	hasErr := false
	for chain := range chainSet {
		if err := hasCycles(chain, nil, nil); err != nil {
			hasErr = true
		}
	}
	if !hasErr {
		t.Errorf("Did not get error when a chain had a cycle")
	}
}

func combineChains(map1, map2 map[string][]api.TestStep) map[string][]api.TestStep {
	newMap := make(map[string][]api.TestStep)
	for k, v := range map1 {
		newMap[k] = v
	}
	for k, v := range map2 {
		newMap[k] = v
	}
	return newMap
}

func combineWorkflows(map1, map2 map[string]api.MultiStageTestConfiguration) map[string]api.MultiStageTestConfiguration {
	newMap := make(map[string]api.MultiStageTestConfiguration)
	for k, v := range map1 {
		newMap[k] = v
	}
	for k, v := range map2 {
		newMap[k] = v
	}
	return newMap
}

// TestNewGraph verifies that the graph successfully returns errors
// for invalid registry items. The TestAncestorNames and
// TestDescendantNames tests verify that the structure of the graph
// is correct, so that is not done in this test.
func TestNewGraph(t *testing.T) {
	testCases := []struct {
		name      string
		workflows WorkflowByName
		chains    ChainByName
	}{{
		name: "Invalid reference in workflow",
		workflows: WorkflowByName{
			"ipi2": api.MultiStageTestConfiguration{
				Pre: []api.TestStep{{
					Reference: &ipiInstall,
				}},
			}},
		chains: ChainByName{},
	}, {
		name: "Invalid chain in workflow",
		workflows: WorkflowByName{
			"ipi2": api.MultiStageTestConfiguration{
				Pre: []api.TestStep{{
					Chain: &ipiInstallInstall,
				}},
			}},
		chains: ChainByName{},
	}, {
		name:      "Invalid reference in chain",
		workflows: WorkflowByName{},
		chains: ChainByName{
			"ipi-install-2": []api.TestStep{{
				Reference: &ipiInstall,
			}},
		},
	}, {
		name:      "Invalid chain in chain",
		workflows: WorkflowByName{},
		chains: ChainByName{
			"ipi-install-2": []api.TestStep{{
				Chain: &ipiInstallInstall,
			}},
		},
	}}

	for _, testCase := range testCases {
		workflows := combineWorkflows(workflowMap, testCase.workflows)
		chains := combineChains(chainMap, testCase.chains)
		if _, err := NewGraph(referenceMap, chains, workflows); err == nil {
			t.Errorf("%s: No error returned on invalid registry", testCase.name)
		}
	}
}
