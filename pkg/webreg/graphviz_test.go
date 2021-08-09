package webreg

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/openshift/ci-tools/pkg/load"
)

const ipiWorkflow = `digraph Webreg {
	compound=true;
	color=blue;
	fontname="-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif,'Apple Color Emoji','Segoe UI Emoji','Segoe UI Symbol','Noto Color Emoji'";
	node[shape=rectangle fontname="SFMono-Regular,Menlo,Monaco,Consolas,'Liberation Mono','Courier New',monospace"];
	rankdir=TB;
	label="Workflow &#34;ipi&#34;";

	0 [label="ipi-install-rbac" href="/reference/ipi-install-rbac"];
	1 [label="ipi-install-install" href="/reference/ipi-install-install"];
	2 [label="Intentionally left blank"];
	3 [label="ipi-deprovision-must-gather" href="/reference/ipi-deprovision-must-gather"];
	4 [label="ipi-deprovision-deprovision" href="/reference/ipi-deprovision-deprovision"];

	0 -> 1 ;
	3 -> 4 ;
	1 -> 2 [ltail=cluster_1 lhead=cluster_2 minlen=2];
	2 -> 3 [ltail=cluster_2 lhead=cluster_4 minlen=2];

	subgraph cluster_1 {
		label="Pre";
		labeljust="l";
		fontname="-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif,'Apple Color Emoji','Segoe UI Emoji','Segoe UI Symbol','Noto Color Emoji'";
		subgraph cluster_0 {
			label="ipi-install";
			labeljust="l";
			href="/chain/ipi-install";
			fontname="SFMono-Regular,Menlo,Monaco,Consolas,'Liberation Mono','Courier New',monospace";
			0;
			1;
		}
	}
	subgraph cluster_2 {
		label="Test";
		labeljust="l";
		fontname="-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif,'Apple Color Emoji','Segoe UI Emoji','Segoe UI Symbol','Noto Color Emoji'";
		2;
	}
	subgraph cluster_4 {
		label="Post";
		labeljust="l";
		fontname="-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif,'Apple Color Emoji','Segoe UI Emoji','Segoe UI Symbol','Noto Color Emoji'";
		subgraph cluster_3 {
			label="ipi-deprovision";
			labeljust="l";
			href="/chain/ipi-deprovision";
			fontname="SFMono-Regular,Menlo,Monaco,Consolas,'Liberation Mono','Courier New',monospace";
			3;
			4;
		}
	}
}`
const installChain = `digraph Webreg {
	compound=true;
	color=blue;
	fontname="-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif,'Apple Color Emoji','Segoe UI Emoji','Segoe UI Symbol','Noto Color Emoji'";
	node[shape=rectangle fontname="SFMono-Regular,Menlo,Monaco,Consolas,'Liberation Mono','Courier New',monospace"];
	rankdir=TB;
	label="Chain &#34;ipi-install&#34;";

	0 [label="ipi-install-rbac" href="/reference/ipi-install-rbac"];
	1 [label="ipi-install-install" href="/reference/ipi-install-install"];

	0 -> 1 ;

}`
const deprovisionChain = `digraph Webreg {
	compound=true;
	color=blue;
	fontname="-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif,'Apple Color Emoji','Segoe UI Emoji','Segoe UI Symbol','Noto Color Emoji'";
	node[shape=rectangle fontname="SFMono-Regular,Menlo,Monaco,Consolas,'Liberation Mono','Courier New',monospace"];
	rankdir=TB;
	label="Chain &#34;ipi-deprovision&#34;";

	0 [label="ipi-deprovision-must-gather" href="/reference/ipi-deprovision-must-gather"];
	1 [label="ipi-deprovision-deprovision" href="/reference/ipi-deprovision-deprovision"];

	0 -> 1 ;

}`

func TestChainDotFile(t *testing.T) {
	_, chains, _, _, _, _, err := load.Registry("../../test/multistage-registry/registry", load.RegistryFlag(0))
	if err != nil {
		t.Fatalf("Failed to load registry: %v", err)
	}
	ipiInstall := chainDotFile("ipi-install", chains)
	if ipiInstall != installChain {
		t.Errorf("Generated dot file for ipi-install differs from expected: %s", diff.StringDiff(installChain, ipiInstall))
	}
	ipiDeprovision := chainDotFile("ipi-deprovision", chains)
	if ipiDeprovision != deprovisionChain {
		t.Errorf("Generated dot file for ipi-deprovision differs from expected: %s", diff.StringDiff(deprovisionChain, ipiDeprovision))
	}
}

func TestWorkflowDotFile(t *testing.T) {
	_, chains, workflows, _, _, _, err := load.Registry("../../test/multistage-registry/registry", load.RegistryFlag(0))
	if err != nil {
		t.Fatalf("Failed to load registry: %v", err)
	}
	ipi := workflowDotFile("ipi", workflows, chains, workflowType)
	if ipi != ipiWorkflow {
		t.Errorf("Generated dot file for ipi differs from expected: %s", diff.StringDiff(ipiWorkflow, ipi))
	}
}
