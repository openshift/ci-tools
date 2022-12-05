package webreg

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"os/exec"
	"strings"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/registry"
)

type node struct {
	label    string
	linkable bool
}

type subgraph struct {
	label       string
	nodes       []int
	subgraphs   []int
	firstNode   int
	lastNode    int
	hasSGParent bool
	linkable    bool
}

type edge struct {
	srcType edgeType
	dstType edgeType
	src     int
	dst     int
}

type graph struct {
	label     string
	nodes     []node
	subgraphs []subgraph
	edges     []edge
}

type edgeType int

const (
	subgraphType edgeType = iota
	nodeType
)

const (
	bootstrap413fonts     = "fontname=\"-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif,'Apple Color Emoji','Segoe UI Emoji','Segoe UI Symbol','Noto Color Emoji'\""
	bootstrap413monospace = "fontname=\"SFMono-Regular,Menlo,Monaco,Consolas,'Liberation Mono','Courier New',monospace\""
)

func addSubgraph(mainGraph graph, name string, root []api.TestStep, chains registry.ChainByName, isChild, linkable, isRoot bool) (graph, int) {
	sg := subgraph{
		label:       name,
		firstNode:   -1,
		hasSGParent: isChild,
		linkable:    linkable,
	}
	lastIndex := -1
	lastType := subgraphType
	for _, step := range root {
		var currNode *node
		currSG := -1
		if step.LiteralTestStep != nil {
			currNode = &node{label: step.As, linkable: false}
		} else if step.Reference != nil {
			currNode = &node{label: *step.Reference, linkable: true}
		} else if step.Chain != nil {
			mainGraph, currSG = addSubgraph(mainGraph, *step.Chain, chains[*step.Chain].Steps, chains, !isRoot, true, false)
		}
		// create new edge
		var newIndex int
		var newType edgeType
		if currNode != nil {
			newType = nodeType
			mainGraph.nodes = append(mainGraph.nodes, *currNode)
			newIndex = len(mainGraph.nodes) - 1
			if sg.firstNode == -1 {
				sg.firstNode = newIndex
			}
			sg.nodes = append(sg.nodes, newIndex)
		} else if currSG != -1 {
			newType = subgraphType
			newIndex = currSG
			if sg.firstNode == -1 {
				sg.firstNode = mainGraph.subgraphs[newIndex].firstNode
			}
			sg.subgraphs = append(sg.subgraphs, newIndex)
		}
		if lastIndex != -1 {
			newEdge := edge{
				dst:     newIndex,
				dstType: newType,
			}
			newEdge.srcType = lastType
			newEdge.src = lastIndex
			mainGraph.edges = append(mainGraph.edges, newEdge)
		}
		lastIndex = newIndex
		lastType = newType
	}
	// this is used to identify a node that can be linked from is this sg is a src for an edge
	if lastType == nodeType {
		sg.lastNode = lastIndex
	} else {
		sg.lastNode = mainGraph.subgraphs[lastIndex].lastNode
	}
	if !isRoot {
		mainGraph.subgraphs = append(mainGraph.subgraphs, sg)
	}
	return mainGraph, len(mainGraph.subgraphs) - 1
}

func addEmptySubgraph(mainGraph graph, name string) (graph, int) {
	sg := subgraph{
		label:     name,
		firstNode: -1,
	}
	emptyNode := node{label: "Intentionally left blank"}
	mainGraph.nodes = append(mainGraph.nodes, emptyNode)
	sg.nodes = append(sg.nodes, len(mainGraph.nodes)-1)
	sg.firstNode = len(mainGraph.nodes) - 1
	sg.lastNode = sg.firstNode
	mainGraph.subgraphs = append(mainGraph.subgraphs, sg)
	return mainGraph, len(mainGraph.subgraphs) - 1

}

func workflowDotFile(name string, workflows registry.WorkflowByName, chains registry.ChainByName, wfType string) string {
	workflow := workflows[name]
	mainGraph := graph{
		label: fmt.Sprintf("%s \"%s\"", wfType, name),
	}
	var preIndex, testIndex, postIndex int
	if len(workflow.Pre) == 0 {
		mainGraph, preIndex = addEmptySubgraph(mainGraph, "Pre")
	} else {
		mainGraph, preIndex = addSubgraph(mainGraph, "Pre", workflow.Pre, chains, false, false, false)
	}
	if len(workflow.Test) == 0 {
		mainGraph, testIndex = addEmptySubgraph(mainGraph, "Test")
	} else {
		mainGraph, testIndex = addSubgraph(mainGraph, "Test", workflow.Test, chains, false, false, false)
	}
	if len(workflow.Post) == 0 {
		mainGraph, postIndex = addEmptySubgraph(mainGraph, "Post")
	} else {
		mainGraph, postIndex = addSubgraph(mainGraph, "Post", workflow.Post, chains, false, false, false)
	}
	preTestEdge := edge{
		src:     preIndex,
		dst:     testIndex,
		srcType: subgraphType,
		dstType: subgraphType,
	}
	mainGraph.edges = append(mainGraph.edges, preTestEdge)
	testPostEdge := edge{
		src:     testIndex,
		dst:     postIndex,
		srcType: subgraphType,
		dstType: subgraphType}
	mainGraph.edges = append(mainGraph.edges, testPostEdge)
	return writeDotFile(mainGraph)
}

func WorkflowGraph(name string, workflows registry.WorkflowByName, chains registry.ChainByName, wfType string) ([]byte, error) {
	return renderDotFile(workflowDotFile(name, workflows, chains, wfType))
}

func chainDotFile(name string, chains registry.ChainByName) string {
	mainGraph, _ := addSubgraph(graph{}, name, chains[name].Steps, chains, false, true, true)
	mainGraph.label = fmt.Sprintf("Chain \"%s\"", name)
	return writeDotFile(mainGraph)
}

func ChainGraph(name string, chains registry.ChainByName) ([]byte, error) {
	return renderDotFile(chainDotFile(name, chains))
}

func renderDotFile(dot string) ([]byte, error) {
	cmd := exec.Command("dot", "-Tsvg")
	cmd.Stdin = bytes.NewBufferString(dot)
	buf := &bytes.Buffer{}
	cmd.Stderr = buf
	out, err := cmd.Output()
	if execErr, ok := err.(*exec.Error); ok && execErr.Err == exec.ErrNotFound {
		//http.Error(w, "The 'dot' binary is not installed", http.StatusBadRequest)
		return []byte{}, errors.New("The 'dot' binary is not installed")
	} else if err != nil {
		return out, errors.New(buf.String())
	}
	return out, err
}

func writeSubgraph(mainGraph graph, sg subgraph, index int, indentPrefix string) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("%ssubgraph cluster_%d {\n", indentPrefix, index))
	indentPrefix = fmt.Sprint(indentPrefix, "\t")
	builder.WriteString(fmt.Sprintf("%slabel=\"%s\";\n", indentPrefix, html.EscapeString(sg.label)))
	builder.WriteString(fmt.Sprint(indentPrefix, "labeljust=\"l\";\n"))
	if sg.linkable {
		builder.WriteString(fmt.Sprintf("%shref=\"/%s/%s\";\n", indentPrefix, "chain", html.EscapeString(sg.label)))
		builder.WriteString(fmt.Sprint(indentPrefix, bootstrap413monospace, ";\n"))
	} else {
		builder.WriteString(fmt.Sprint(indentPrefix, bootstrap413fonts, ";\n"))
	}
	for _, node := range sg.nodes {
		builder.WriteString(fmt.Sprintf("%s%d;\n", indentPrefix, node))
	}
	for _, sub := range sg.subgraphs {
		builder.WriteString(writeSubgraph(mainGraph, mainGraph.subgraphs[sub], sub, indentPrefix))
	}
	indentPrefix = strings.Replace(indentPrefix, "\t", "", 1)
	builder.WriteString(fmt.Sprint(indentPrefix, "}\n"))
	return builder.String()
}

func writeDotFile(mainGraph graph) string {
	indentPrefix := ""
	var builder strings.Builder
	builder.WriteString("digraph Webreg {\n")
	indentPrefix = fmt.Sprint(indentPrefix, "\t")
	builder.WriteString(fmt.Sprint(indentPrefix, "compound=true;\n"))
	builder.WriteString(fmt.Sprint(indentPrefix, "color=blue;\n"))
	// fonts from bootstrap 4.1.3 css
	builder.WriteString(fmt.Sprint(indentPrefix, bootstrap413fonts, ";\n"))
	builder.WriteString(fmt.Sprint(indentPrefix, "node[shape=rectangle ", bootstrap413monospace, "];\n"))
	builder.WriteString(fmt.Sprint(indentPrefix, "rankdir=TB;\n"))
	builder.WriteString(fmt.Sprintf("%slabel=\"%s\";\n", indentPrefix, html.EscapeString(mainGraph.label)))
	builder.WriteString("\n")
	for index, node := range mainGraph.nodes {
		href := ""
		if node.linkable {
			href = fmt.Sprintf(" href=\"/%s/%s\"", "reference", html.EscapeString(node.label))
		}
		builder.WriteString(fmt.Sprintf("%s%d [label=\"%s\"%s];\n", indentPrefix, index, node.label, href))
	}
	builder.WriteString("\n")
	for _, edge := range mainGraph.edges {
		var srcNode, dstNode int
		var attrs []string
		if edge.srcType == nodeType {
			srcNode = edge.src
		} else {
			srcNode = mainGraph.subgraphs[edge.src].lastNode
			attrs = append(attrs, fmt.Sprintf("ltail=cluster_%d", edge.src))
		}
		if edge.dstType == nodeType {
			dstNode = edge.dst
		} else {
			dstNode = mainGraph.subgraphs[edge.dst].firstNode
			attrs = append(attrs, fmt.Sprintf("lhead=cluster_%d", edge.dst))
		}
		builder.WriteString(fmt.Sprintf("%s%d -> %d ", indentPrefix, srcNode, dstNode))
		if len(attrs) > 0 {
			builder.WriteString(fmt.Sprintf("[%s", attrs[0]))
			if len(attrs) > 1 {
				builder.WriteString(fmt.Sprintf(" %s", attrs[1]))
			}
			if edge.dstType == subgraphType {
				builder.WriteString(" minlen=2")
			}
			builder.WriteString("]")
		}
		builder.WriteString(";\n")
	}
	builder.WriteString("\n")
	for index, sg := range mainGraph.subgraphs {
		if !sg.hasSGParent {
			builder.WriteString(writeSubgraph(mainGraph, sg, index, indentPrefix))
		}
	}
	builder.WriteString("}\n")
	return builder.String()
}
