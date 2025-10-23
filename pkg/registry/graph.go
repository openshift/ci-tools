package registry

import (
	"fmt"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
)

// Type identifies the type of registry element a Node refers to
type Type int

const (
	Workflow Type = iota
	Chain
	Reference
	Observer
)

var nodeTypes = [4]string{
	Workflow:  "workflow",
	Reference: "reference",
	Chain:     "chain",
	Observer:  "observer",
}

// Node is an interface that allows a user to identify ancestors and descendants of a step registry element
type Node interface {
	// Name returns the name of the registry element a Node refers to
	Name() string
	// Type returns the type of the registry element a Node refers to
	Type() Type
	// Ancestors returns a set of nodes containing the names of all of the node's ancestors
	Ancestors() []Node
	// Descendants returns a set of nodes containing the names of all of the node's descendants
	Descendants() []Node
	// Parents returns a set of nodes containing the names of all the node's parents
	Parents() []Node
	// Children returns a set of nodes containing the names of all the node's children
	Children() []Node
}

// NodeByName provides a mapping from node name to the Node interface
type NodeByName struct {
	References map[string]Node
	Chains     map[string]Node
	Workflows  map[string]Node
	Observers  map[string]Node
}

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
	observerChildren observerNodeSet
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

type observerNode struct {
	nodeWithName
	workflowParents workflowNodeSet
}

// Verify that all node types implement Node
var _ Node = &workflowNode{}
var _ Node = &chainNode{}
var _ Node = &referenceNode{}
var _ Node = &observerNode{}

// internal node type sets
type workflowNodeSet map[*workflowNode]sets.Empty
type chainNodeSet map[*chainNode]sets.Empty
type referenceNodeSet map[*referenceNode]sets.Empty
type observerNodeSet map[*observerNode]sets.Empty

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

func (set observerNodeSet) insert(node *observerNode) {
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

func (*observerNode) Type() Type {
	return Observer
}

func (n *nodeWithParents) Parents() []Node {
	var parents []Node
	for parent := range n.workflowParents {
		parents = append(parents, parent)
	}
	for parent := range n.chainParents {
		parents = append(parents, parent)
	}
	return parents
}

func (*workflowNode) Parents() []Node { return nil }

func (n *nodeWithChildren) Children() []Node {
	var children []Node
	for child := range n.referenceChildren {
		children = append(children, child)
	}
	for child := range n.chainChildren {
		children = append(children, child)
	}
	return children
}

func (*referenceNode) Children() []Node { return nil }
func (*observerNode) Children() []Node  { return nil }

func (n *nodeWithParents) Ancestors() []Node {
	ancestors := n.Parents()
	for parent := range n.chainParents {
		ancestors = append(ancestors, parent.Ancestors()...)
	}
	return ancestors
}

func (*workflowNode) Ancestors() []Node { return nil }

func (n *observerNode) Ancestors() []Node {
	ret := n.Parents()
	for x := range n.workflowParents {
		ret = append(ret, x.Ancestors()...)
	}
	return ret
}

func (n *observerNode) Parents() []Node {
	var ret []Node
	for x := range n.workflowParents {
		ret = append(ret, x)
	}
	return ret
}

func (n *nodeWithChildren) Descendants() []Node {
	descendants := n.Children()
	for child := range n.chainChildren {
		descendants = append(descendants, child.Descendants()...)
	}
	return descendants
}

func (n *workflowNode) Descendants() []Node {
	ret := n.nodeWithChildren.Descendants()
	for child := range n.observerChildren {
		ret = append(ret, child)
	}
	return ret
}

func (*referenceNode) Descendants() []Node { return nil }
func (*observerNode) Descendants() []Node  { return nil }

func (n *workflowNode) addChainChild(child *chainNode) {
	n.chainChildren.insert(child)
	child.workflowParents.insert(n)
}

func (n *workflowNode) addReferenceChild(child *referenceNode) {
	n.referenceChildren.insert(child)
	child.workflowParents.insert(n)
}

func (n *workflowNode) addObserverChild(child *observerNode) {
	n.observerChildren.insert(child)
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

func FieldsForNode(n Node) logrus.Fields {
	return logrus.Fields{
		"node-name": n.Name(),
		"node-type": nodeTypes[n.Type()],
	}
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

func hasCycles(node *chainNode, ancestors sets.Set[string], traversedPath []string) error {
	if ancestors == nil {
		ancestors = sets.New[string]()
	}
	if ancestors.Has(node.name) {
		return fmt.Errorf("cycle detected: %s is an ancestor of itself; traversedPath: %v", node.name, append(traversedPath, node.name))
	}
	ancestors.Insert(node.name)
	for child := range node.chainChildren {
		if child.Type() != Chain {
			continue
		}
		// get new copy of ancestors and traversedPath so the root node's set isn't changed
		ancestorsCopy := sets.New[string](ancestors.UnsortedList()...)
		traversedPathCopy := append(traversedPath[:0:0], traversedPath...)
		traversedPathCopy = append(traversedPathCopy, node.name)
		if err := hasCycles(child, ancestorsCopy, traversedPathCopy); err != nil {
			return err
		}
	}
	return nil
}

// NewGraph returns a NodeByType map representing the provided step references, chains, and workflows as a directed graph.
func NewGraph(stepsByName ReferenceByName, chainsByName ChainByName, workflowsByName WorkflowByName, observersByName ObserverByName) (NodeByName, error) {
	nodesByName := NodeByName{
		References: make(map[string]Node, len(stepsByName)),
		Chains:     make(map[string]Node, len(chainsByName)),
		Workflows:  make(map[string]Node, len(workflowsByName)),
		Observers:  make(map[string]Node, len(observersByName)),
	}
	// References can only be children; load them so they can be added as children by workflows and chains
	referenceNodes := make(referenceNodeByName, len(stepsByName))
	for name := range stepsByName {
		node := &referenceNode{
			nodeWithName:    newNodeWithName(name),
			nodeWithParents: newNodeWithParents(),
		}
		referenceNodes[name] = node
		nodesByName.References[name] = node
	}
	for name := range observersByName {
		node := &observerNode{
			nodeWithName: newNodeWithName(name),
		}
		nodesByName.Observers[name] = node
	}

	// since we may load the parent chain before a child chain, we need to make the parent->child links after loading all chains
	parentChildChain := make(map[*chainNode][]string)
	chainNodes := make(chainNodeByName, len(chainsByName))
	for name, chain := range chainsByName {
		node := &chainNode{
			nodeWithName:     newNodeWithName(name),
			nodeWithChildren: newNodeWithChildren(),
			nodeWithParents:  newNodeWithParents(),
		}
		chainNodes[name] = node
		nodesByName.Chains[name] = node
		for _, step := range chain.Steps {
			if step.Reference != nil {
				if _, exists := referenceNodes[*step.Reference]; !exists {
					return nodesByName, fmt.Errorf("chain %s contains non-existent reference %s", name, *step.Reference)
				}
				node.addReferenceChild(referenceNodes[*step.Reference])
			}
			if step.Chain != nil {
				parentChildChain[node] = append(parentChildChain[node], *step.Chain)
			}
		}
	}
	for parent, children := range parentChildChain {
		for _, child := range children {
			if _, exists := chainNodes[child]; !exists {
				return nodesByName, fmt.Errorf("chain %s contains non-existent chain %s", parent.Name(), child)
			}
			parent.addChainChild(chainNodes[child])
		}
	}
	// verify that no cycles exist
	for _, chain := range chainNodes {
		if err := hasCycles(chain, sets.New[string](), []string{}); err != nil {
			return nodesByName, err
		}
	}
	workflowNodes := make(workflowNodeByName, len(workflowsByName))
	for name, workflow := range workflowsByName {
		node := &workflowNode{
			nodeWithName:     newNodeWithName(name),
			nodeWithChildren: newNodeWithChildren(),
		}
		workflowNodes[name] = node
		nodesByName.Workflows[name] = node
		if workflow.Observers != nil {
			for _, observer := range workflow.Observers.Enable {
				if child, exists := nodesByName.Observers[observer]; !exists {
					return nodesByName, fmt.Errorf("workflow %s contains non-existent observer %s", name, observer)
				} else {
					if node.observerChildren == nil {
						node.observerChildren = make(observerNodeSet)
					}
					child := child.(*observerNode)
					if child.workflowParents == nil {
						child.workflowParents = make(workflowNodeSet)
					}
					node.addObserverChild(child)
				}
			}
		}
		steps := append(workflow.Pre, append(workflow.Test, workflow.Post...)...)
		for _, step := range steps {
			if step.Reference != nil {
				if _, exists := referenceNodes[*step.Reference]; !exists {
					return nodesByName, fmt.Errorf("workflow %s contains non-existent reference %s", name, *step.Reference)
				}
				node.addReferenceChild(referenceNodes[*step.Reference])
			}
			if step.Chain != nil {
				if _, exists := chainNodes[*step.Chain]; !exists {
					return nodesByName, fmt.Errorf("workflow %s contains non-existent chain %s", name, *step.Chain)
				}
				node.addChainChild(chainNodes[*step.Chain])
			}
		}
	}
	return nodesByName, nil
}
