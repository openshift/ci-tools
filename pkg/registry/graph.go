package registry

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"
)

// Type identifies the type of registry element a Node refers to
type Type int

const (
	Workflow Type = iota
	Chain
	Reference
)

// Node is an interface that allows a user to identify ancestors and descendants of a step registry element
type Node interface {
	// Name returns the name of the registry element a Node refers to
	Name() string
	// Type returns the type of the registry element a Node refers to
	Type() Type
	// AncestorNames returns a set of strings containing the names of all of the node's ancestors
	AncestorNames() sets.String
	// DescendantNames returns a set of strings containing the names of all of the node's descendants
	DescendantNames() sets.String
	// ParentNames returns a set of strings containing the names of all the node's parents
	ParentNames() sets.String
	// ChildrenNames returns a set of strings containing the names of all the node's children
	ChildrenNames() sets.String
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

func (set workflowNodeSet) insert(node *workflowNode) {
	set[node] = sets.Empty{}
}

func (set chainNodeSet) insert(node *chainNode) {
	set[node] = sets.Empty{}
}

func (set referenceNodeSet) insert(node *referenceNode) {
	set[node] = sets.Empty{}
}

func (n *nodeWithName) Name() string {
	return n.name
}

func (*workflowNode) Type() Type {
	return Workflow
}

func (*chainNode) Type() Type {
	return Chain
}

func (*referenceNode) Type() Type {
	return Reference
}

func (n *nodeWithParents) ParentNames() sets.String {
	parents := sets.NewString()
	for parent := range n.workflowParents {
		parents.Insert(parent.Name())
	}
	for parent := range n.chainParents {
		parents.Insert(parent.Name())
	}
	return parents
}

func (*workflowNode) ParentNames() sets.String { return sets.NewString() }

func (n *nodeWithChildren) ChildrenNames() sets.String {
	children := sets.NewString()
	for child := range n.referenceChildren {
		children.Insert(child.Name())
	}
	for child := range n.chainChildren {
		children.Insert(child.Name())
	}
	return children
}

func (*referenceNode) ChildrenNames() sets.String { return sets.NewString() }

func (n *nodeWithParents) AncestorNames() sets.String {
	ancestors := n.ParentNames()
	for parent := range n.chainParents {
		ancestors.Insert(parent.AncestorNames().List()...)
	}
	return ancestors
}

func (*workflowNode) AncestorNames() sets.String { return sets.NewString() }

func (n *nodeWithChildren) DescendantNames() sets.String {
	descendants := n.ChildrenNames()
	for child := range n.chainChildren {
		descendants.Insert(child.DescendantNames().List()...)
	}
	return descendants
}

func (*referenceNode) DescendantNames() sets.String { return sets.NewString() }

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
		ancestors = sets.NewString()
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
		ancestorsCopy := sets.NewString(ancestors.UnsortedList()...)
		traversedPathCopy := append(traversedPath[:0:0], traversedPath...)
		traversedPathCopy = append(traversedPathCopy, node.name)
		if err := hasCycles(child, ancestorsCopy, traversedPathCopy); err != nil {
			return err
		}
	}
	return nil
}

// NewGraph returns a NodeByType map representing the provided step references, chains, and workflows as a directed graph.
func NewGraph(stepsByName ReferenceByName, chainsByName ChainByName, workflowsByName WorkflowByName) (NodeByName, error) {
	nodesByName := make(NodeByName)
	// References can only be children; load them so they can be added as children by workflows and chains
	referenceNodes := make(referenceNodeByName)
	for name := range stepsByName {
		node := &referenceNode{
			nodeWithName:    newNodeWithName(name),
			nodeWithParents: newNodeWithParents(),
		}
		referenceNodes[name] = node
		nodesByName[name] = node
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
		nodesByName[name] = node
		for _, step := range chain {
			if step.Reference != nil {
				if _, exists := referenceNodes[*step.Reference]; !exists {
					return nil, fmt.Errorf("Chain %s contains non-existent reference %s", name, *step.Reference)
				}
				node.addReferenceChild(referenceNodes[*step.Reference])
			}
			if step.Chain != nil {
				parentChildChain[node] = *step.Chain
			}
		}
	}
	for parent, child := range parentChildChain {
		if _, exists := chainNodes[child]; !exists {
			return nil, fmt.Errorf("Chain %s contains non-existent chain %s", parent.Name(), child)
		}
		parent.addChainChild(chainNodes[child])
	}
	// verify that no cycles exist
	for _, chain := range chainNodes {
		if err := hasCycles(chain, sets.NewString(), []string{}); err != nil {
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
		nodesByName[name] = node
		steps := append(workflow.Pre, append(workflow.Test, workflow.Post...)...)
		for _, step := range steps {
			if step.Reference != nil {
				if _, exists := referenceNodes[*step.Reference]; !exists {
					return nil, fmt.Errorf("Workflow %s contains non-existent reference %s", name, *step.Reference)
				}
				node.addReferenceChild(referenceNodes[*step.Reference])
			}
			if step.Chain != nil {
				if _, exists := chainNodes[*step.Chain]; !exists {
					return nil, fmt.Errorf("Workflow %s contains non-existent chain %s", name, *step.Chain)
				}
				node.addChainChild(chainNodes[*step.Chain])
			}
		}
	}
	return nodesByName, nil
}
