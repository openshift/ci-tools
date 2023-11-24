package registry

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/api"
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
var nested = "nested"
var ipiConf = "ipi-conf"
var ipiConfAWS = "ipi-conf-aws"
var simpleObserver = "simple-observer"

var referenceMap = ReferenceByName{
	ipiInstallInstall:         {},
	ipiInstallRBAC:            {},
	ipiDeprovisionDeprovision: {},
	ipiDeprovisionMustGather:  {},
	ipiConf:                   {},
	ipiConfAWS:                {},
}

var observerMap = ObserverByName{
	simpleObserver: {},
}
var chainMap = ChainByName{
	ipiConfAWS: {
		Steps: []api.TestStep{{
			Reference: &ipiConf,
		}, {
			Reference: &ipiConfAWS,
		}},
	},
	ipiInstall: {
		Steps: []api.TestStep{{
			Reference: &ipiInstallInstall,
		}, {
			Reference: &ipiInstallRBAC,
		}},
	},
	ipiDeprovision: {
		Steps: []api.TestStep{{
			Reference: &ipiDeprovisionMustGather,
		}, {
			Reference: &ipiDeprovisionDeprovision,
		}},
	},
	nested: {
		Steps: []api.TestStep{{
			Chain: &ipiInstall,
		}, {
			Chain: &ipiDeprovision,
		}},
	},
}
var workflowMap = WorkflowByName{
	ipi: {
		Observers: &api.Observers{
			Enable: []string{simpleObserver},
		},
		Pre: []api.TestStep{{
			Chain: &ipiInstall,
		}},
		Post: []api.TestStep{{
			Chain: &ipiDeprovision,
		}},
	},
}

func nodesEqual(x, y []Node) bool {
	if len(x) != len(y) {
		return false
	}
	for _, xNode := range x {
		found := false
		for _, yNode := range y {
			if xNode.Name() == yNode.Name() && xNode.Type() == yNode.Type() {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestAncestors(t *testing.T) {
	testCases := []struct {
		name     string
		nodeType Type
		expected []Node
	}{{
		name:     ipi,
		nodeType: Workflow,
		expected: []Node{},
	}, {
		name:     ipiInstall,
		nodeType: Chain,
		expected: []Node{
			&workflowNode{nodeWithName: nodeWithName{name: ipi}},
			&chainNode{nodeWithName: nodeWithName{name: nested}},
		},
	}, {
		name:     ipiDeprovisionMustGather,
		nodeType: Reference,
		expected: []Node{
			&workflowNode{nodeWithName: nodeWithName{name: ipi}},
			&chainNode{nodeWithName: nodeWithName{name: ipiDeprovision}},
			&chainNode{nodeWithName: nodeWithName{name: nested}},
		},
	}, {
		name:     ipiConfAWS,
		nodeType: Reference,
		expected: []Node{
			&chainNode{nodeWithName: nodeWithName{name: ipiConfAWS}},
		},
	}}

	graph, err := NewGraph(referenceMap, chainMap, workflowMap, observerMap)
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}
	for _, testCase := range testCases {
		var node Node
		switch testCase.nodeType {
		case Reference:
			node = graph.References[testCase.name]
		case Chain:
			node = graph.Chains[testCase.name]
		case Workflow:
			node = graph.Workflows[testCase.name]
		case Observer:
			node = graph.Observers[testCase.name]
		}
		if !nodesEqual(node.Ancestors(), testCase.expected) {
			t.Errorf("%s: ancestor sets not equal", testCase.name)
		}
	}
}

func TestDescendants(t *testing.T) {
	testCases := []struct {
		name     string
		nodeType Type
		expected []Node
	}{{
		name:     ipi,
		nodeType: Workflow,
		expected: []Node{
			&chainNode{nodeWithName: nodeWithName{name: ipiInstall}},
			&referenceNode{nodeWithName: nodeWithName{name: ipiInstallInstall}},
			&referenceNode{nodeWithName: nodeWithName{name: ipiInstallRBAC}},
			&chainNode{nodeWithName: nodeWithName{name: ipiDeprovision}},
			&referenceNode{nodeWithName: nodeWithName{name: ipiDeprovisionMustGather}},
			&referenceNode{nodeWithName: nodeWithName{name: ipiDeprovisionDeprovision}},
			&observerNode{nodeWithName: nodeWithName{name: simpleObserver}},
		},
	}, {
		name:     ipiInstall,
		nodeType: Chain,
		expected: []Node{
			&referenceNode{nodeWithName: nodeWithName{name: ipiInstallInstall}},
			&referenceNode{nodeWithName: nodeWithName{name: ipiInstallRBAC}},
		},
	}, {
		name:     ipiDeprovisionMustGather,
		nodeType: Reference,
		expected: []Node{},
	}, {
		name:     nested,
		nodeType: Chain,
		expected: []Node{
			&chainNode{nodeWithName: nodeWithName{name: ipiInstall}},
			&referenceNode{nodeWithName: nodeWithName{name: ipiInstallInstall}},
			&referenceNode{nodeWithName: nodeWithName{name: ipiInstallRBAC}},
			&chainNode{nodeWithName: nodeWithName{name: ipiDeprovision}},
			&referenceNode{nodeWithName: nodeWithName{name: ipiDeprovisionMustGather}},
			&referenceNode{nodeWithName: nodeWithName{name: ipiDeprovisionDeprovision}},
		},
	}, {
		name:     ipiConfAWS,
		nodeType: Chain,
		expected: []Node{
			&referenceNode{nodeWithName: nodeWithName{name: ipiConfAWS}},
			&referenceNode{nodeWithName: nodeWithName{name: ipiConf}},
		},
	}}

	graph, err := NewGraph(referenceMap, chainMap, workflowMap, observerMap)
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}
	for _, testCase := range testCases {
		var node Node
		switch testCase.nodeType {
		case Reference:
			node = graph.References[testCase.name]
		case Chain:
			node = graph.Chains[testCase.name]
		case Workflow:
			node = graph.Workflows[testCase.name]
		case Observer:
			node = graph.Observers[testCase.name]
		}
		if !nodesEqual(node.Descendants(), testCase.expected) {
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

func combineChains(map1, map2 ChainByName) ChainByName {
	newMap := make(ChainByName)
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
			"ipi-install-2": {
				Steps: []api.TestStep{{
					Reference: &ipiInstall,
				}},
			},
		},
	}, {
		name:      "Invalid chain in chain",
		workflows: WorkflowByName{},
		chains: ChainByName{
			"ipi-install-2": {
				Steps: []api.TestStep{{
					Chain: &ipiInstallInstall,
				}},
			},
		},
	}, {
		name: "Invalid observer in workflow",
		workflows: WorkflowByName{
			"ipi2": api.MultiStageTestConfiguration{
				Observers: &api.Observers{Enable: []string{simpleObserver}},
			},
		},
		chains: ChainByName{
			"ipi-install-2": {
				Steps: []api.TestStep{{
					Chain: &ipiInstallInstall,
				}},
			},
		},
	}}

	for _, testCase := range testCases {
		workflows := combineWorkflows(workflowMap, testCase.workflows)
		chains := combineChains(chainMap, testCase.chains)
		if _, err := NewGraph(referenceMap, chains, workflows, observerMap); err == nil {
			t.Errorf("%s: No error returned on invalid registry", testCase.name)
		}
	}
}
