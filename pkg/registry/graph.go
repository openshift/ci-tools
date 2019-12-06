package registry

import (
	"fmt"

	"github.com/openshift/ci-tools/pkg/api"
	"k8s.io/apimachinery/pkg/util/sets"
)

// Type declarations

// Type identifies the type of registry element a Node refers to
type Type int

const (
	Workflow Type = iota
	Chain
	Reference
)

// Node is an interface that allows a user to identify ancestors and descendants of a step registry element
type Node interface {
	Name() string
	Type() Type
	AncestorNames() sets.String
	DescendantNames() sets.String
}

// NodeByName provides a mapping from node name to the Node interface
type NodeByName map[string]Node

type nodeWithName struct {
	name string
}

type nodeWithParents struct {
	workflowParents workflowNodeSet
	chainParents    chainNodeSet
}

type nodeWithChildren struct {
	chainChildren     chainNodeSet
	referenceChildren referenceNodeSet
}

type workflowNode struct {
	nodeWithName
	nodeWithChildren
}

type chainNode struct {
	nodeWithName
	nodeWithParents
	nodeWithChildren
}

type referenceNode struct {
	nodeWithName
	nodeWithParents
}

// Verify that all node types implement Node
var _ Node = &workflowNode{}
var _ Node = &chainNode{}
var _ Node = &referenceNode{}

// internal node type sets
type workflowNodeSet map[*workflowNode]sets.Empty
type chainNodeSet map[*chainNode]sets.Empty
type referenceNodeSet map[*referenceNode]sets.Empty

// Name -> internal node type maps
type workflowNodeByName map[string]*workflowNode
type chainNodeByName map[string]*chainNode
type referenceNodeByName map[string]*referenceNode

// Set functions

func (set workflowNodeSet) insert(node *workflowNode) {
	set[node] = sets.Empty{}
}

func (set workflowNodeSet) list() []*workflowNode {
	res := make([]*workflowNode, 0, len(set))
	for key := range set {
		res = append(res, key)
	}
	return res
}

func (set chainNodeSet) insert(node *chainNode) {
	set[node] = sets.Empty{}
}

func (set chainNodeSet) list() []*chainNode {
	res := make([]*chainNode, 0, len(set))
	for key := range set {
		res = append(res, key)
	}
	return res
}

func (set referenceNodeSet) insert(node *referenceNode) {
	set[node] = sets.Empty{}
}

func (set referenceNodeSet) list() []*referenceNode {
	res := make([]*referenceNode, 0, len(set))
	for key := range set {
		res = append(res, key)
	}
	return res
}

// Interface Functions
// Name function

func (n *nodeWithName) Name() string {
	return n.name
}

// Type functions

func (*workflowNode) Type() Type {
	return Workflow
}

func (*chainNode) Type() Type {
	return Chain
}

func (*referenceNode) Type() Type {
	return Reference
}

// AncestorNames functions

func (n *nodeWithParents) AncestorNames() sets.String {
	ancestors := sets.NewString()
	for parent := range n.workflowParents {
		ancestors.Insert(parent.Name())
	}
	for parent := range n.chainParents {
		ancestors.Insert(parent.Name())
		ancestors.Insert(parent.AncestorNames().List()...)
	}
	return ancestors
}

func (*workflowNode) AncestorNames() sets.String { return nil }

// DescendantNames function

func (n *nodeWithChildren) DescendantNames() sets.String {
	descendants := sets.NewString()
	for child := range n.referenceChildren {
		descendants.Insert(child.Name())
	}
	for child := range n.chainChildren {
		descendants.Insert(child.Name())
		descendants.Insert(child.DescendantNames().List()...)
	}
	return descendants
}

func (*referenceNode) DescendantNames() sets.String { return nil }

// Struct helper functions
// addChild functions

func (n *workflowNode) addChainChild(child *chainNode) {
	n.chainChildren.insert(child)
	child.workflowParents.insert(n)
}

func (n *workflowNode) addReferenceChild(child *referenceNode) {
	n.referenceChildren.insert(child)
	child.workflowParents.insert(n)
}

func (n *chainNode) addChainChild(child *chainNode) {
	n.chainChildren.insert(child)
	child.chainParents.insert(n)
}

func (n *chainNode) addReferenceChild(child *referenceNode) {
	n.referenceChildren.insert(child)
	child.chainParents.insert(n)
}

func newNodeWithName(name string) nodeWithName {
	return nodeWithName{name: name}
}

func newNodeWithParents() nodeWithParents {
	return nodeWithParents{
		chainParents:    make(chainNodeSet),
		workflowParents: make(workflowNodeSet),
	}
}

func newNodeWithChildren() nodeWithChildren {
	return nodeWithChildren{
		chainChildren:     make(chainNodeSet),
		referenceChildren: make(referenceNodeSet),
	}
}

func hasCycles(node *chainNode, ancestors sets.String, traversedPath []string) error {
	if ancestors == nil {
		ancestors = make(sets.String)
	}
	if ancestors.Has(node.name) {
		return fmt.Errorf("Cycle detected: %s is an ancestor of itself; traversedPath: %v", node.name, append(traversedPath, node.name))
	}
	ancestors.Insert(node.name)
	for child := range node.chainChildren {
		if child.Type() != Chain {
			continue
		}
		// get new copy of ancestors and traversedPath so the root node's set isn't changed
		ancestorsCopy := sets.NewString()
		for _, ancestor := range ancestors.List() {
			ancestorsCopy.Insert(ancestor)
		}
		traversedPathCopy := append(traversedPath[:0:0], traversedPath...)
		traversedPathCopy = append(traversedPathCopy, node.name)
		if err := hasCycles(child, ancestorsCopy, traversedPathCopy); err != nil {
			return err
		}
	}
	return nil
}

func NewGraph(stepsByName map[string]api.LiteralTestStep, chainsByName map[string][]api.TestStep, workflowsByName map[string]api.MultiStageTestConfiguration) (NodeByName, error) {
	nodesByName := make(NodeByName)
	// References can only be children; load them so they can be added as children by workflows and chains
	referenceNodes := make(referenceNodeByName)
	for name := range stepsByName {
		node := &referenceNode{
			nodeWithName:    newNodeWithName(name),
			nodeWithParents: newNodeWithParents(),
		}
		referenceNodes[name] = node
		nodesByName[name] = Node(node)
	}
	// since we may load the parent chain before a child chain, we need to make the parent->child links after loading all chains
	parentChildChain := make(map[*chainNode]string)
	chainNodes := make(chainNodeByName)
	for name, chain := range chainsByName {
		node := &chainNode{
			nodeWithName:     newNodeWithName(name),
			nodeWithChildren: newNodeWithChildren(),
			nodeWithParents:  newNodeWithParents(),
		}
		chainNodes[name] = node
		nodesByName[name] = Node(node)
		for _, step := range chain {
			if step.Reference != nil {
				node.addReferenceChild(referenceNodes[*step.Reference])
			}
			if step.Chain != nil {
				parentChildChain[node] = *step.Chain
			}
		}
	}
	for parent, child := range parentChildChain {
		parent.addChainChild(chainNodes[child])
	}
	// verify that no cycles exist
	for _, chain := range chainNodes {
		if err := hasCycles(chain, make(sets.String), []string{}); err != nil {
			return nil, err
		}
	}
	workflowNodes := make(workflowNodeByName)
	for name, workflow := range workflowsByName {
		node := &workflowNode{
			nodeWithName:     newNodeWithName(name),
			nodeWithChildren: newNodeWithChildren(),
		}
		workflowNodes[name] = node
		nodesByName[name] = Node(node)
		steps := append(workflow.Pre, append(workflow.Test, workflow.Post...)...)
		for _, step := range steps {
			if step.Reference != nil {
				node.addReferenceChild(referenceNodes[*step.Reference])
			}
			if step.Chain != nil {
				node.addChainChild(chainNodes[*step.Chain])
			}
		}
	}
	return nodesByName, nil
}
