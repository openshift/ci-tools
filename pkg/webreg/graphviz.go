package webreg

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"os/exec"
	"strings"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/registry"
)

// node is a single named step in the graph
type node struct {
	label string
	// Whether this is a reference to a step in the registry.  Otherwise the
	// label is rendered as plain text.
	linkable bool
}

// subgraph is a group of sequential steps and/or other subgraphs
// Used to render both registry chains and the pre/test/post steps of a
// workflow.
type subgraph struct {
	label     string
	nodes     []int
	subgraphs []int
}

// edge connects node objects in the final drawing
// Every edge connects two nodes in the final output.  If an edge crosses one
// or more chain boundaries, either on the incoming or outgoing direction, it
// is clipped to the outermost box used to render the chains.  In this case,
// one or both of the `type` members are set to `subgraphType` and the
// corresponding `graph` members are set to the sub-graph against which the
// edge is to be clipped (options `lhead` and `ltail` in `graphviz`).
type edge struct {
	srcType, dstType   edgeType
	srcNode, dstNode   int
	srcGraph, dstGraph int
}

// graph is the container for all elements
// All other edge, sub-graph, etc. references are indices into this structure.
type graph struct {
	label     string
	nodes     []node
	edges     []edge
	subgraphs []subgraph
}

// graphBuilder maintains state required to build a `graph` object
type graphBuilder struct {
	chains registry.ChainByName
	// The final graph.
	// Can be initialized prior to the construction (e.g. with a label).
	graph graph
	// The next edge to be emitted when a new node is found.
	//
	// This variable is the only state necessary to create edges between nodes
	// of the graph.  It is modified throughout the traversal of the various
	// objects so that it contains the required information to emit a new edge
	// when a node is created, namely:
	//
	// - Its initial value is `noEdge`, so that the first node does not get an
	//   incoming edge.
	// - Whenever the traversal enters a chain, `dstType` is set to
	//   `subgraphType`, so that the incoming edge is clipped against the
	//   chain's bounding box.
	// - Whenever the traversal leaves a chain, `srcType` is similarly set to
	//   `subgraphType` for the same reason, for the outgoing edge.
	// - In both of the previous cases, `dstGraph`/`srcGraph` is set
	//   accordingly.  It contains the index of the chain's sub-graph.  Note
	//   that this is done by `addSubgraph` at every level and always at the
	//   end of the function, so that the edge is clipped against the outermost
	//   chain.
	// - Whenever a node (i.e. a step) is added to the graph, an edge is added
	//   based on the current state of the variable.  Its destination is the
	//   newly added node.  This variable is then changed in preparation for
	//   the subsequent edge (its source is the one just added).
	edge edge
}

// edgeType is used to create different types of edges
type edgeType uint8

const (
	// noEdge is the initial state, since no edge is needed for the first node
	noEdge edgeType = iota
	// subgraphType is an edge from/to a chain's bounding box
	subgraphType
	// nodeType is an edge directly from/to a node
	nodeType
)

const (
	// fonts from bootstrap 4.1.3 css
	bootstrap413fonts     = "fontname=\"-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif,'Apple Color Emoji','Segoe UI Emoji','Segoe UI Symbol','Noto Color Emoji'\""
	bootstrap413monospace = "fontname=\"SFMono-Regular,Menlo,Monaco,Consolas,'Liberation Mono','Courier New',monospace\""
)

var (
	//go:embed graphviz.go.tmpl
	graphvizTemplateStr string
	//go:embed graphviz_subgraph.go.tmpl
	graphvizSGTemplateStr string
	graphvizTemplate      = template.Must(
		template.New("webreg").Funcs(template.FuncMap{
			"label":    func(n node) string { return n.label },
			"linkable": func(n node) bool { return n.linkable },
			"srcNode":  func(e edge) int { return e.srcNode },
			"dstNode":  func(e edge) int { return e.dstNode },
			"attrs": func(e edge) string {
				var a []string
				if e.srcType == subgraphType {
					a = append(a, fmt.Sprintf("ltail=cluster_%d", e.srcGraph))
				}
				if e.dstType == subgraphType {
					a = append(a, fmt.Sprintf("lhead=cluster_%d", e.dstGraph))
					a = append(a, "minlen=2")
				}
				if len(a) == 0 {
					return ""
				}
				return fmt.Sprintf("[%s]", strings.Join(a, " "))
			},
		}).Parse(graphvizTemplateStr))
	graphvizSGTemplate = template.Must(
		template.New("webreg_sg").Funcs(template.FuncMap{
			"label": func(g graph, i int) string {
				return g.subgraphs[i].label
			},
			"nodes": func(g graph, i int) []int {
				return g.subgraphs[i].nodes
			},
		}).Parse(graphvizSGTemplateStr))
)

// addSubgraph creates a sub-graph for a sequence of steps
func (b *graphBuilder) addSubgraph(label string, steps []api.TestStep) int {
	sg := subgraph{label: label}
	hasSrc := b.edge.srcType != noEdge
	edge := len(b.graph.edges)
	b.edge.dstType = subgraphType
	if len(steps) == 0 {
		b.addNode(&sg, node{label: "Intentionally left blank"})
	}
	for _, step := range steps {
		if step.LiteralTestStep != nil {
			b.addNode(&sg, node{label: step.As})
		} else if step.Reference != nil {
			b.addNode(&sg, node{label: *step.Reference, linkable: true})
		} else if step.Chain != nil {
			i := b.addSubgraph(*step.Chain, b.chains[*step.Chain].Steps)
			sg.subgraphs = append(sg.subgraphs, i)
		}
	}
	i := len(b.graph.subgraphs)
	b.graph.subgraphs = append(b.graph.subgraphs, sg)
	b.edge.srcType = subgraphType
	b.edge.srcGraph = i
	// If this isn't the first sub-graph and at least one edge has been added…
	if hasSrc && edge != len(b.graph.edges) {
		// …the incoming edge should point to this (and not to its first node).
		// Note that this assignment happens multiple times for the same edge as
		// the recursive stack is unwound: the last (i.e. the outermost)
		// sub-graph will be the final determinant of the destination.
		b.graph.edges[edge].dstGraph = i
	}
	return i
}

// addNode creates a single leaf node and, if necessary, an edge
func (b *graphBuilder) addNode(sg *subgraph, n node) {
	i := len(b.graph.nodes)
	b.graph.nodes = append(b.graph.nodes, n)
	sg.nodes = append(sg.nodes, i)
	b.addEdge(i)
}

// addEdge creates an edge based on the current state to node `i`
// `b.edge` is manipulated so it contains the necessary information for the
// subsequent edge.
func (b *graphBuilder) addEdge(i int) {
	b.edge.dstNode = i
	if b.edge.srcType != noEdge {
		b.graph.edges = append(b.graph.edges, b.edge)
	}
	b.edge = edge{srcType: nodeType, srcNode: b.edge.dstNode, dstType: nodeType}
}

func ChainGraph(name string, chains registry.ChainByName) ([]byte, error) {
	return renderDotFile(chainDotFile(name, chains))
}

func WorkflowGraph(name string, workflows registry.WorkflowByName, chains registry.ChainByName, wfType string) ([]byte, error) {
	return renderDotFile(workflowDotFile(name, workflows, chains, wfType))
}

func chainDotFile(name string, chains registry.ChainByName) string {
	chain := chains[name]
	b := graphBuilder{
		chains: chains,
		graph:  graph{label: fmt.Sprintf(`Chain "%s"`, name)},
	}
	i := b.addSubgraph(name, chain.Steps)
	return writeDotFile(b.graph, b.graph.subgraphs[i].subgraphs, true)
}

func workflowDotFile(name string, workflows registry.WorkflowByName, chains registry.ChainByName, wfType string) string {
	workflow := workflows[name]
	b := graphBuilder{
		chains: chains,
		graph:  graph{label: fmt.Sprintf(`%s "%s"`, wfType, name)},
	}
	roots := [3]int{
		b.addSubgraph("Pre", workflow.Pre),
		b.addSubgraph("Test", workflow.Test),
		b.addSubgraph("Post", workflow.Post),
	}
	return writeDotFile(b.graph, roots[:], false)
}

func renderDotFile(dot string) ([]byte, error) {
	cmd := exec.Command("dot", "-Tsvg")
	cmd.Stdin = bytes.NewBufferString(dot)
	buf := &bytes.Buffer{}
	cmd.Stderr = buf
	out, err := cmd.Output()
	if execErr, ok := err.(*exec.Error); ok && execErr.Err == exec.ErrNotFound {
		return []byte{}, errors.New("The 'dot' binary is not installed")
	} else if err != nil {
		return out, errors.New(buf.String())
	}
	return out, err
}

func writeSubgraph(g graph, tmpl *template.Template, b *strings.Builder, i int, prefix string, linkable bool) {
	if err := tmpl.Execute(b, map[string]any{
		"bootstrap413fonts":     template.HTML(bootstrap413fonts),
		"bootstrap413monospace": template.HTML(bootstrap413monospace),
		"g":                     g,
		"sg":                    i,
		"prefix":                prefix,
		"linkable":              linkable,
	}); err != nil {
		panic(fmt.Errorf("subgraph template rendering failed: %w", err))
	}
	for _, i := range g.subgraphs[i].subgraphs {
		writeSubgraph(g, tmpl, b, i, prefix+"\t", true)
	}
	b.WriteString(prefix)
	b.WriteString("}\n")
}

func writeDotFile(g graph, roots []int, linkable bool) string {
	var b strings.Builder
	if err := graphvizTemplate.Execute(&b, map[string]any{
		"bootstrap413fonts":     template.HTML(bootstrap413fonts),
		"bootstrap413monospace": template.HTML(bootstrap413monospace),
		"label":                 g.label,
		"nodes":                 g.nodes,
		"edges":                 g.edges,
	}); err != nil {
		panic(fmt.Errorf("graphviz template rendering failed: %w", err))
	}
	for _, i := range roots {
		writeSubgraph(g, graphvizSGTemplate, &b, i, "\t", linkable)
	}
	b.WriteString("}\n")
	return b.String()
}
