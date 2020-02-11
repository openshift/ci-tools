package webreg

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/alecthomas/chroma/formatters/html"
	"github.com/alecthomas/chroma/lexers"
	"github.com/alecthomas/chroma/styles"
	"github.com/sirupsen/logrus"
	prowConfig "k8s.io/test-infra/prow/config"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
)

const htmlPageStart = `
<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8"><title>%s</title>
<link rel="stylesheet" href="https://stackpath.bootstrapcdn.com/bootstrap/4.1.3/css/bootstrap.min.css" integrity="sha384-MCw98/SFnGE8fJT3GXwEOngsV7Zt27NXFoaoApmYm81iuXoPkFOJwJ8ERdknLPMO" crossorigin="anonymous">
<meta name="viewport" content="width=device-width, initial-scale=1, shrink-to-fit=no">
<style>
@namespace svg url(http://www.w3.org/2000/svg);
svg|a:link, svg|a:visited {
  cursor: pointer;
}

svg|a text,
text svg|a {
  fill: #007bff;
  text-decoration: none;
  background-color: transparent;
  -webkit-text-decoration-skip: objects;
}

svg|a:hover text, svg|a:active text {
  fill: #0056b3;
  text-decoration: underline;
}

@media (max-width: 992px) {
  .container {
    width: 100%%;
    max-width: none;
  }
}
pre {
	border: 10px solid transparent;
}
h1, h2, p {
	padding-top: 10px;
}
.info {
	text-decoration-line: underline;
	text-decoration-style: dotted;
	text-decoration-color: #c0c0c0;
}
button {
  padding:0.2em 1em;
  border-radius: 8px;
  cursor:pointer;
}
td {
  vertical-align: middle;
}
</style>
</head>
<body>
<div class="container">
`

const htmlPageEnd = `
<p class="small">Source code for this page located on <a href="https://github.com/openshift/ci-tools">GitHub</a></p>
</div>
</body>
</html>
`

const errPage = `
<h1><a href="/">Openshift CI Step Registry</a></h1>
{{ . }}
`

const mainPage = `<h1>Openshift CI Step Registry</h1>
<p>Not sure what the step registry is or how to use it? Visit the <a href="/help">help page</a>.</p>
<form action="/search">
  <div>
    Search for job: <input type="search" id="search" name="job" placeholder="Job Name">
	<button>Search</button>
  </div>
</form>
{{ template "workflowTable" .Workflows }}
{{ template "chainTable" .Chains }}
{{ template "referenceTable" .References}}
{{ template "jobTable" .Jobs }}
`

const workflowListPage = `
<h2><a href="/">Openshift CI Step Registry</a> &gt; Workflows
{{ template "workflowTable" .Workflows }}
`

const chainListPage = `
<h2><a href="/">Openshift CI Step Registry</a> &gt; Chains
{{ template "chainTable" .Chains }}
`

const referenceListPage = `
<h2><a href="/">Openshift CI Step Registry</a> &gt; References
{{ template "referenceTable" .References }}
`

const jobListPage = `
<h2><a href="/">Openshift CI Step Registry</a> &gt; Jobs
{{ template "jobTable" .Jobs }}
`

const referencePage = `
<h1><a href="/">Openshift CI Step Registry</a></h1>
<h2><a href="/references">References</a> &gt; <nobr>{{ .As }}</nobr></h2>
<p id="documentation">{{ .Documentation }}</p>
<h2 id="image">Container image used for this step: <span style="font-family:monospace">{{ .From }}</span></h2>
<h2 id="source">Source Code</h2>
{{ syntaxedSource .Commands }}
`

const chainPage = `
<h1><a href="/">Openshift CI Step Registry</a></h1>
<h2><a href="/chains">Chains</a> &gt; <nobr>{{ .As }}</nobr></h2>
<p id="documentation">{{ .Documentation }}</p>
<h2 id="steps" title="Step run by the chain, in runtime order">Steps</h2>
{{ template "stepTable" .Steps}}
<h2 id="graph" title="Visual representation of steps run by this chain">Step Graph</h2>
{{ chainGraph .As }}
`

// workflowJobPage defines the template for both jobs and workflows
const workflowJobPage = `
<h1><a href="/">Openshift CI Step Registry</a></h1>
{{ $type := .Type }}
{{ if eq $type "Job" }}
	<h2><a href="/search">Jobs</a> &gt; <nobr>{{ .As }}</nobr></h2>
{{ else if eq $type "Workflow" }}
	<h2><a href="/workflows">Workflows</a> &gt; <nobr>{{ .As }}</nobr></h2>
	<p id="documentation">{{ .Documentation }}</p>
{{ end }}
{{ if .Steps.ClusterProfile }}
	<h2 id="cluster_profile">Cluster Profile: <span style="font-family:monospace">{{ .Steps.ClusterProfile }}</span></h2>
{{ end }}
<h2 id="pre" title="Steps run by this {{ toLower $type }} to set up and configure the tests, in runtime order">Pre Steps</h2>
{{ template "stepTable" .Steps.Pre }}
<h2 id="test" title="Steps in the {{ toLower $type }} that run actual tests, in runtime order">Test Steps</h2>
{{ template "stepTable" .Steps.Test }}
<h2 id="post" title="Steps run by this {{ toLower $type }} to clean up and teardown test resources, in runtime order">Post Steps</h2>
{{ template "stepTable" .Steps.Post }}
<h2 id="graph" title="Visual representation of steps run by this {{ toLower $type }}">Step Graph</h2>
{{ workflowGraph .As }}
`

const jobSearchPage = `
<h1><a href="/">Openshift CI Step Registry</a></h1>
<h2>Multistage Test ProwJob Search</h2>
<form>
  <div>
    <input type="search" id="search" name="job" placeholder="Job Name">
	<button>Search</button>
  </div>
</form>
{{ template "jobTable" . }}
`

const templateDefinitions = `
{{ define "nameWithLink" }}
	<nobr><a href="/registry/{{ . }}" style="font-family:monospace">{{ . }}</a></nobr>
{{ end }}

{{ define "stepTable" }}
{{ if not . }}
	<p>No test steps configured.</p>
{{ else }}
	<table class="table">
	<thead>
		<tr>
			<th title="The name of the reference or chain" class="info">Name</th>
			<th title="The documentation for the reference or chain" class="info">Description</th>
		</tr>
	</thead>
	<tbody>
		{{ range $index, $step := . }}
			<tr>
				{{ $name := testStepName $step }}
				{{ $doc := docsForName $name }}
				{{ if not $step.LiteralTestStep }}
					<td>{{ template "nameWithLink" $name }}</td>
				{{ else }}
					<td>{{ $name }}</td>
				{{ end }}
				<td>{{ noescape $doc }}</td>
			</tr>
		{{ end }}
	</tbody>
	</table>
{{ end }}
{{ end }}

{{ define "stepList" }}
	<ul>
	{{ range $index, $step := .}}
		{{ $name := testStepName $step }}
		<li>{{ template "nameWithLink" $name }}</li>
	{{ end }}
	</ul>
{{ end }}

{{ define "workflowTable" }}
	<h2 id="workflows">Workflows</h2>
	<p>Workflows are the highest level registry components, defining a test from start to finish.</p>
	<table class="table">
		<thead>
			<tr>
				<th title="The name of the workflow" class="info">Name</th>
				<th title="What the workflow is supposed to do" class="info">Description</th>
				<th title="The registry elements used during setup" class="info">Pre</th>
				<th title="The registry elements containing the tests" class="info">Test</th>
				<th title="The registry elements used to teardown and clean up the test" class="info">Post</th>
			</tr>
		</thead>
		<tbody>
			{{ range $name, $config := . }}
				<tr>
					<td>{{ template "nameWithLink" $name }}</td>
					<td>{{ docsForName $name }}</td>
					<td>{{ template "stepList" $config.Pre }}</td>
					<td>{{ template "stepList" $config.Test }}</td>
					<td>{{ template "stepList" $config.Post }}</td>
				</tr>
			{{ end }}
		</tbody>
	</table>
{{ end }}

{{ define "chainTable" }}
	<h2 id="chains">Chains</h2>
	<p>Chains are registry components that allow users to string together multiple steps under one name.</p>
	<table class="table">
		<thead>
			<tr>
				<th title="The name of the chain" class="info">Name</th>
				<th title="What the chain is supposed to do" class="info">Description</th>
				<th title="The steps (references and other chains) that the chain runs (in order)" class="info">Steps</th>
			</tr>
		</thead>
		<tbody>
			{{ range $name, $config := . }}
				<tr>
					<td>{{ template "nameWithLink" $name }}</td>
					<td>{{ docsForName $name }}</td>
					<td>{{ template "stepList" $config }}</td>
				</tr>
			{{ end }}
		</tbody>
	</table>
{{ end }}

{{ define "referenceTable" }}
	<h2 id="references">References</h2>
	<p>References are the lowest level registry components, defining a command to run and a container to run the command in.</p>
	<table class="table">
		<thead>
			<tr>
				<th title="The name of the reference" class="info">Name</th>
				<th title="The documentation for the reference" class="info">Description</th>
			</tr>
		</thead>
		<tbody>
			{{ range $name, $config := . }}
				<tr>
					<td>{{ template "nameWithLink" $name }}</td>
					<td>{{ docsForName $name }}</td>
				</tr>
			{{ end }}
		</tbody>
	</table>
{{ end }}

{{ define "jobTable" }}
    <h2 id="jobs">Jobs</h2>
    <p>Jobs using the multistage test design.</p>
	<table class="table">
		<thead>
			<tr>
				<th title="GitHub organization that the job is from" class="info">Org</th>
				<th title="GitHub repo that the job is from" class="info">Repo</th>
				<th title="GitHub branch that the job is from" class="info">Branch</th>
				<th title="The multistage tests in the config" class="info">Tests</th>
			</tr>
		</thead>
		<tbody>
			{{ range $index, $org := .Orgs }}
				<tr>
					<td rowspan="{{ orgSpan $org }}" style="vertical-align: middle;">{{ $org.Name }}</td>
				</tr>
				{{ range $index, $repo := $org.Repos }}
				    {{ $repoLen := len $repo.Branches }}
					<tr>
						<td rowspan="{{ inc $repoLen }}" style="vertical-align: middle;">{{ $repo.Name }}</td>
					</tr>
					{{ range $index, $branch := $repo.Branches }}
						<tr>
							<td style="vertical-align: middle;">{{ $branch.Name }}</td>
							<td>
								<ul>
								{{ range $index, $test := $branch.Tests }}
									<li><nobr><a href="/job/{{$org.Name}}-{{$repo.Name}}-{{$branch.Name}}-{{$test}}" style="font-family:monospace">{{$test}}</a></nobr></li>
								{{ end }}
								</ul>
							</td>
						</tr>
					{{ end }}
				{{ end }}
			{{ end }}
		</tbody>
	</table>
{{ end }}
`

const helpPage = `
<h1><a href="/">Openshift CI Step Registry</a></h1>
<h1>What is the Multi-Stage Test and the Test Step Registry?</h1>

<p>
The multistage test style in the ci-operator is a modular test design that
allows users to create new tests by combining smaller, individual test steps.
These individual tests can be put into a shared registry that other tests can
access. This results in test workflows that are easier to maintain and
upgrade as multiple test workflows can share steps and don’t have to each be
updated individually to fix bugs or add new features. It also reduces the
chances of a mistake when copying a feature from one test workflow to
another.
</p>

<p>
To understand how the multistage tests and registry work, we must first talk
about the 3 components of the test registry and how to use those components
to create a test.
</p>

<h2>Reference:</h2>
<p>
A reference is the lowest level component in the test step registry. A
reference defines a base container image for a step, the filename of the
shell script to run inside the container, the resource requests and limits
for the container, and documentation for the reference. Example of a
reference:
</p>

<pre>
ref:
  as: ipi-conf
  from: centos:7
  commands: ipi-conf-commands.sh
  resources:
    requests:
      cpu: 1000m
      memory: 100Mi
  documentation: |-
	The IPI configure step generates the install-config.yaml file based on the cluster profile and optional input files.
</pre>

<p>
Note: the shell script file must follow the naming convention described later
in this help page.
</p>

<p>
The commands file must contain shell script in a shell language supported by
the <code>shellcheck</code> program used to validate the commands. However,
regardless of the shell language used for the commands, the web UI will
syntax highlight all commands as bash.
</p>

<p>
Sharing files between steps is supported via a shared directory. All
containers will have an environment variable <code>SHARED_DIR</code> which
contains the path of the shared directory. An artifacts directory also exists
for steps that produce artifacts. The path for the artifacts directory is
stored in the <code>ARTIFACTS_DIR</code> environment variable.
</p>

<p>
A reference may be referred to in chains, workflows, and ci-operator configs.
</p>

<h2>Chain:</h2>
<p>
A chain is a registry component that specifies multiple steps to be run.
Steps are run in the order that they are written. Steps specified by a chain
can be either references and other chains. Example of a chain:
</p>

<pre>
chain:
  as: ipi-deprovision
  steps:
  - chain: gather
  - ref: ipi-deprovision-deprovision
  documentation: |-
    The IPI deprovision step chain contains all the individual steps necessary to deprovision an OpenShift cluster.
</pre>

<h2>Workflow:</h2>
<p>
A workflow is the highest level component of the step registry. It is almost
identical to the syntax of the ci-operator config for multistage tests and
defines an entire test from start to finish. It has 4 basic components: a
cluster_type string (eg: <code>aws</code>, <code>azure4</code>,
<code>gcp</code>), and 3 chains: <code>pre</code>, <code>test</code>, and
<code>post</code>. The <code>pre</code> chain is intended to be used to set
up a testing environment (such as creating a test cluster), the
<code>test</code> chain is intended to contain all tests that a job wants to
run, and the <code>post</code> chain is intended to be used to clean up any
resources created/used by the test. If a step in <code>pre</code> or
<code>test</code> fails, all pending <code>pre</code> and <code>test</code>
steps are skipped and all <code>post</code> steps are run to ensure that
resources are properly cleaned up. This is an example of a workflow config:
</p>

<pre>
workflow:
  as: origin-e2e
  steps:
    pre:
    - ref: ipi-conf
    - chain: ipi-install
    test:
    - ref: origin-e2e-test
    post:
    - chain: ipi-deprovision
  documentation: |-
	The Origin E2E workflow executes the common end-to-end test suite.
</pre>

<h2>CI-Operator Test Config:</h2>
<p>
The CI-Operator test config syntax for multistage tests is very similar to
the registry workflow syntax. The main differences are that the ci-operator
config does not have a <code>documentation</code> field, and the ci-operator
config can specify a workflow to use. Also, the <code>cluster_profile</code>,
<code>pre</code>, <code>test</code>, and <code>post</code> fields are under a
<code>steps</code> field instead of <code>workflow</code>. Here is an example
of the <code>tests</code> section of a ci-operator config using the
multistage test design:
</p>

<pre>
tests:
- as: e2e-steps
  commands: ""
  steps:
    cluster_profile: aws
    workflow: origin-e2e
</pre>

<p>
In this example, the ci-operator config simply specifies the desired cluster
profile and the <code>origin-e2e</code> workflow shown in the example for the
<code>Workflow</code> section above.
</p>

<p>
Since the ci-operator-config and workflows share the same fields, it is
possible to override fields specified in a workflow. In cases where both the
workflow and a ci-operator config specify the same field, the ci-operator config’s
field has priority (i.e. the value from the ci-operator config is used).
</p>

<h2>Registry Layout:</h2>
<p>
To prevent naming collisions between all the registry components, the step
registry has a very strict naming scheme and directory layout. First, all
components have a prefix determined by the directory structure, similar to
how the ci-operator configs do. The prefix is the relative directory path
with all &#96;<code>/</code>&#96; characters changed to
&#96;<code>-</code>&#96;. For example, a file under the
<code>ipi/install/conf</code> directory would have as prefix of
<code>ipi-install-conf</code>. If there is a workflow, chain, or reference in
that directory, the <code>as</code> field for that component would need to be
the same as the prefix. Further, only one of reference, chain, or workflow
can be in a subdirectory (otherwise there would be a name conflict),
</p>

<p>
After the prefix, we apply a suffix based on what the file is defining. These
are the suffixes for the 4 file types that exist in the registry:
<ul style="margin-bottom:0px;">
  <li>Reference: <code>-ref.yaml</code></li>
  <li>Reference command script: <code>-commands.sh</code></li>
  <li>Chain: <code>-chain.yaml</code></li>
  <li>Workflow: <code>-workflow.yaml</code></li>
</ul>
</p>

<p>
Continuing the example above, a reference in the
<code>ipi/install/conf</code> subdirectory would have a filename of
<code>ipi-install-conf-ref.yaml</code> and the command would be
<code>ipi-install-conf-commands.sh</code>.
</p>

<p>
Other files that are allowed in the step registry but are not used for
testing are <code>OWNERS</code> files and files that end in <code>.md</code>.
</p>
`

const workflowType = "Workflow"
const jobType = "Job"

// workflowJob is a struct that can define either a workflow or a job
type workflowJob struct {
	api.RegistryWorkflow
	Type string
}

type Jobs struct {
	Orgs []Org
}

type Org struct {
	Name  string
	Repos []Repo
}

type Repo struct {
	Name     string
	Branches []Branch
}

type Branch struct {
	Name  string
	Tests []string
}

func getBaseTemplate(workflows registry.WorkflowByName, chains registry.ChainByName, docs map[string]string) *template.Template {
	base := template.New("baseTemplate").Funcs(
		template.FuncMap{
			"docsForName": func(name string) string {
				return docs[name]
			},
			"testStepName": getTestStepName,
			"noescape": func(str string) template.HTML {
				return template.HTML(str)
			},
			"toLower": strings.ToLower,
			"workflowGraph": func(as string) template.HTML {
				svg, err := WorkflowGraph(as, workflows, chains)
				if err != nil {
					return template.HTML(err.Error())
				}
				return template.HTML(svg)
			},
			"chainGraph": func(as string) template.HTML {
				svg, err := ChainGraph(as, chains)
				if err != nil {
					return template.HTML(err.Error())
				}
				return template.HTML(svg)
			},
			"orgSpan": func(o Org) int {
				rowspan := 0
				for _, repo := range o.Repos {
					rowspan += len(repo.Branches)
					rowspan++
				}
				return rowspan + 1
			},
			"inc": func(i int) int {
				return i + 1
			},
		},
	)
	base, err := base.Parse(templateDefinitions)
	if err != nil {
		logrus.Errorf("Failed to load step list template: %v", err)
	}
	return base
}

func getTestStepName(step api.TestStep) string {
	if step.LiteralTestStep != nil {
		return step.As
	} else if step.Reference != nil {
		return *step.Reference
	} else if step.Chain != nil {
		return *step.Chain
	}
	// this case shouldn't happen
	return ""
}

func jobToWorkflow(name string, config api.MultiStageTestConfiguration, workflows registry.WorkflowByName, docs map[string]string) (workflowJob, map[string]string) {
	// If there are literal test steps, we need to add the command to the docs, without changing the original map
	// check if there are literal test steps
	literalExists := false
	for _, step := range append(append(config.Pre, config.Test...), config.Post...) {
		if step.LiteralTestStep != nil {
			literalExists = true
			break
		}
	}
	if literalExists {
		newDocs := make(map[string]string)
		for k, v := range docs {
			newDocs[k] = v
		}
		docs = newDocs
		for _, step := range append(append(config.Pre, config.Test...), config.Post...) {
			if step.LiteralTestStep != nil {
				baseDoc := fmt.Sprintf(`Container image: <span style="font-family:monospace">%s</span>`, step.From)
				if highlighted, err := syntaxBash(step.Commands); err == nil {
					docs[step.As] = fmt.Sprintf("%s<br>Command: %s", baseDoc, highlighted)
					continue
				} else {
					logrus.WithError(err).Errorf("Failed to syntax highlight command %s for job %s", step.As, name)
					docs[step.As] = fmt.Sprintf("%s<br>Command: <pre>%s</pre>", baseDoc, step.Commands)
				}
			}
		}
	}
	if config.Workflow != nil {
		workflow := workflows[*config.Workflow]
		if config.ClusterProfile == "" {
			config.ClusterProfile = workflow.ClusterProfile
		}
		if config.Pre == nil {
			config.Pre = workflow.Pre
		}
		if config.Test == nil {
			config.Test = workflow.Test
		}
		if config.Post == nil {
			config.Post = workflow.Post
		}
	}
	return workflowJob{
		RegistryWorkflow: api.RegistryWorkflow{
			As:            name,
			Documentation: docs[name],
			Steps:         config,
		},
		Type: jobType}, docs
}

func writeErrorPage(w http.ResponseWriter, pageErr error, status int) {
	errPage, err := template.New("errPage").Parse(errPage)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s: %v", http.StatusText(http.StatusInternalServerError), err)
		return
	}
	w.WriteHeader(status)
	writePage(w, "Error: Openshift CI Registry", errPage, fmt.Sprintf("%s: %v", http.StatusText(status), pageErr))
}

func writePage(w http.ResponseWriter, title string, body *template.Template, data interface{}) {
	fmt.Fprintf(w, htmlPageStart, title)
	if err := body.Execute(w, data); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s: %v", http.StatusText(http.StatusInternalServerError), err)
		return
	}
	fmt.Fprintln(w, htmlPageEnd)
}

func helpHandler(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Now().Sub(start)) }()
	help, err := template.New("helpPage").Parse(helpPage)
	if err != nil {
		writeErrorPage(w, err, http.StatusInternalServerError)
		return
	}
	writePage(w, "Step Registry Help Page", help, nil)
}

func mainPageHandler(agent load.RegistryAgent, templateString string, jobList *Jobs, w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Now().Sub(start)) }()

	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	refs, chains, wfs, docs := agent.GetRegistryComponents()
	page := getBaseTemplate(wfs, chains, docs)
	page, err := page.Parse(templateString)
	if err != nil {
		writeErrorPage(w, err, http.StatusInternalServerError)
		return
	}
	comps := struct {
		References registry.ReferenceByName
		Chains     registry.ChainByName
		Workflows  registry.WorkflowByName
		Jobs       *Jobs
	}{
		References: refs,
		Chains:     chains,
		Workflows:  wfs,
		Jobs:       jobList,
	}
	writePage(w, "Step Registry Help Page", page, comps)
}

func WebRegHandler(regAgent load.RegistryAgent, confAgent load.ConfigAgent, jobAgent *prowConfig.Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		trimmedPath := strings.TrimPrefix(req.URL.Path, req.URL.Host)
		// remove leading slash
		trimmedPath = strings.TrimPrefix(trimmedPath, "/")
		// remove trailing slash
		trimmedPath = strings.TrimSuffix(trimmedPath, "/")
		splitURI := strings.Split(trimmedPath, "/")
		if len(splitURI) == 1 {
			switch splitURI[0] {
			case "":
				jobs := getAllMultiStageTests(confAgent, jobAgent)
				mainPageHandler(regAgent, mainPage, jobs, w, req)
			case "jobs":
				jobs := getAllMultiStageTests(confAgent, jobAgent)
				mainPageHandler(regAgent, jobListPage, jobs, w, req)
			case "references":
				mainPageHandler(regAgent, referenceListPage, nil, w, req)
			case "chains":
				mainPageHandler(regAgent, chainListPage, nil, w, req)
			case "workflows":
				mainPageHandler(regAgent, workflowListPage, nil, w, req)
			case "search":
				searchHandler(confAgent, jobAgent, w, req)
			case "help":
				helpHandler(w, req)
			default:
				writeErrorPage(w, errors.New("Invalid path"), http.StatusNotImplemented)
			}
			return
		}
		if len(splitURI) == 2 {
			if splitURI[0] == "registry" {
				refs, chains, workflows, _ := regAgent.GetRegistryComponents()
				if _, ok := refs[splitURI[1]]; ok {
					referenceHandler(regAgent, w, req)
					return
				}
				if _, ok := chains[splitURI[1]]; ok {
					chainHandler(regAgent, w, req)
					return
				}
				if _, ok := workflows[splitURI[1]]; ok {
					workflowHandler(regAgent, w, req)
					return
				}
				writeErrorPage(w, fmt.Errorf("Registry element %s not found", splitURI[1]), http.StatusNotFound)
				return
			} else if splitURI[0] == "job" {
				jobHandler(regAgent, confAgent, w, req)
				return
			}
		}
		writeErrorPage(w, errors.New("Invalid path"), http.StatusNotImplemented)
	}
}

func syntaxBash(source string) (string, error) {
	var output bytes.Buffer
	lexer := lexers.Get("bash")
	style := styles.Get("dracula")
	formatter := html.New(html.Standalone(false))
	iterator, err := lexer.Tokenise(nil, source)
	if err != nil {
		return "", fmt.Errorf("failed to tokenise source: %w", err)
	}
	err = formatter.Format(&output, style, iterator)
	return output.String(), err
}

func referenceHandler(agent load.RegistryAgent, w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Now().Sub(start)) }()
	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	name := path.Base(req.URL.Path)

	page, err := template.New("referencePage").Funcs(
		template.FuncMap{
			"syntaxedSource": func(source string) template.HTML {
				formatted, err := syntaxBash(source)
				if err != nil {
					logrus.Errorf("Failed to format source file: %v", err)
					return template.HTML(source)
				}
				return template.HTML(formatted)
			},
		},
	).Parse(referencePage)
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %v", err), http.StatusInternalServerError)
		return
	}
	refs, _, _, docs := agent.GetRegistryComponents()
	ref := api.RegistryReference{
		LiteralTestStep: api.LiteralTestStep{
			As:       name,
			Commands: refs[name].Commands,
			From:     refs[name].From,
		},
		Documentation: docs[name],
	}
	writePage(w, "Registry Reference Help Page", page, ref)
}

func chainHandler(agent load.RegistryAgent, w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Now().Sub(start)) }()
	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	name := path.Base(req.URL.Path)

	_, chains, wfs, docs := agent.GetRegistryComponents()
	page := getBaseTemplate(wfs, chains, docs)
	page, err := page.Parse(chainPage)
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %v", err), http.StatusInternalServerError)
		return
	}
	chain := api.RegistryChain{
		As:            name,
		Documentation: docs[name],
		Steps:         chains[name],
	}
	writePage(w, "Registry Chain Help Page", page, chain)
}

func workflowHandler(agent load.RegistryAgent, w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Now().Sub(start)) }()
	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	name := path.Base(req.URL.Path)

	_, chains, workflows, docs := agent.GetRegistryComponents()
	page := getBaseTemplate(workflows, chains, docs)
	page, err := page.Parse(workflowJobPage)
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %v", err), http.StatusInternalServerError)
		return
	}
	workflow := workflowJob{
		RegistryWorkflow: api.RegistryWorkflow{
			As:            name,
			Documentation: docs[name],
			Steps:         workflows[name],
		},
		Type: workflowType}
	writePage(w, "Registry Workflow Help Page", page, workflow)
}

func findConfigForJob(jobName string, configs load.FilenameToConfig) (api.MultiStageTestConfiguration, error) {
	splitJobName := strings.Split(jobName, "-")
	var filename, testname string
	var config api.ReleaseBuildConfiguration
	for i := 3; i < len(splitJobName); i++ {
		filename = strings.Join(splitJobName[:i], "-")
		filename = strings.Join([]string{filename, "yaml"}, ".")
		if foundConfig, ok := configs[filename]; ok {
			config = foundConfig
			testname = strings.Join(splitJobName[i:], "-")
		}
	}
	if testname == "" {
		return api.MultiStageTestConfiguration{}, fmt.Errorf("Config not found for job %s", jobName)
	}
	for _, test := range config.Tests {
		if test.As == testname {
			if test.MultiStageTestConfiguration != nil {
				return *test.MultiStageTestConfiguration, nil
			}
			return api.MultiStageTestConfiguration{}, fmt.Errorf("Provided job %s is not a multi stage type test", jobName)
		}
	}
	return api.MultiStageTestConfiguration{}, fmt.Errorf("Could not find job %s. Job either does not exist or is not a multi stage test", jobName)
}

func jobHandler(regAgent load.RegistryAgent, confAgent load.ConfigAgent, w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Now().Sub(start)) }()
	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	name := path.Base(req.URL.Path)
	config, err := findConfigForJob(name, confAgent.GetAll())
	if err != nil {
		writeErrorPage(w, err, http.StatusNotFound)
		return
	}
	_, chains, workflows, docs := regAgent.GetRegistryComponents()
	workflow, docs := jobToWorkflow(name, config, workflows, docs)
	updatedWorkflows := make(registry.WorkflowByName)
	for k, v := range workflows {
		updatedWorkflows[k] = v
	}
	updatedWorkflows[name] = workflow.Steps
	page := getBaseTemplate(updatedWorkflows, chains, docs)
	page, err = page.Parse(workflowJobPage)
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %v", err), http.StatusInternalServerError)
		return
	}
	writePage(w, "Job Test Workflow Help Page", page, workflow)
}

// addJob adds a test to the specified org, repo, and branch in the Jobs struct in alphabetical order
func (j *Jobs) addJob(orgName, repoName, branchName, testName string) {
	orgIndex := 0
	orgExists := false
	for _, currOrg := range j.Orgs {
		if diff := strings.Compare(currOrg.Name, orgName); diff == 0 {
			orgExists = true
			break
		} else if diff > 0 {
			break
		}
		orgIndex++
	}
	if !orgExists {
		newOrg := Org{Name: orgName}
		j.Orgs = append(j.Orgs[:orgIndex], append([]Org{newOrg}, j.Orgs[orgIndex:]...)...)
	}
	repoIndex := 0
	repoExists := false
	for _, currRepo := range j.Orgs[orgIndex].Repos {
		if diff := strings.Compare(currRepo.Name, repoName); diff == 0 {
			repoExists = true
			break
		} else if diff > 0 {
			break
		}
		repoIndex++
	}
	if !repoExists {
		newRepo := Repo{Name: repoName}
		repos := j.Orgs[orgIndex].Repos
		j.Orgs[orgIndex].Repos = append(repos[:repoIndex], append([]Repo{newRepo}, repos[repoIndex:]...)...)
	}
	branchIndex := 0
	branchExists := false
	for _, currBranch := range j.Orgs[orgIndex].Repos[repoIndex].Branches {
		if diff := strings.Compare(currBranch.Name, branchName); diff == 0 {
			branchExists = true
			break
		} else if diff > 0 {
			break
		}
		branchIndex++
	}
	if !branchExists {
		newBranch := Branch{Name: branchName}
		branches := j.Orgs[orgIndex].Repos[repoIndex].Branches
		j.Orgs[orgIndex].Repos[repoIndex].Branches = append(branches[:branchIndex], append([]Branch{newBranch}, branches[branchIndex:]...)...)
	}
	// a single test shouldn't be added multiple times, but that case should be handled correctly just in case
	testIndex := 0
	testExists := false
	for _, currTestName := range j.Orgs[orgIndex].Repos[repoIndex].Branches[branchIndex].Tests {
		if diff := strings.Compare(currTestName, testName); diff == 0 {
			testExists = true
			break
		} else if diff > 0 {
			break
		}
		testIndex++
	}
	if !testExists {
		tests := j.Orgs[orgIndex].Repos[repoIndex].Branches[branchIndex].Tests
		j.Orgs[orgIndex].Repos[repoIndex].Branches[branchIndex].Tests = append(tests[:testIndex], append([]string{testName}, tests[testIndex:]...)...)
	}
}

// getAllMultiStageTests return a map that has the config name in org-repo-branch format as the key and the test names for multi stage jobs as the value
func getAllMultiStageTests(confAgent load.ConfigAgent, jobAgent *prowConfig.Agent) *Jobs {
	jobs := &Jobs{}
	configs := confAgent.GetAll()
	allRepos := jobAgent.Config().AllRepos
	for filename, releaseConfig := range configs {
		name := strings.TrimSuffix(filename, ".yaml")
		var org, repo, branch string
		for orgRepo := range allRepos {
			dashed := strings.ReplaceAll(orgRepo, "/", "-")
			if strings.HasPrefix(name, dashed) {
				split := strings.Split(orgRepo, "/")
				if len(split) != 2 {
					// TODO: handle error? currently just ignoring
					continue
				}
				org = split[0]
				repo = split[1]
				branchVariant := strings.TrimPrefix(name, fmt.Sprint(dashed, "-"))
				splitBV := strings.Split(branchVariant, "__")
				branch = splitBV[0]
				if len(splitBV) == 2 {
					// TODO: how should we handle variants?
				}
				break
			}
		}
		for _, test := range releaseConfig.Tests {
			if test.MultiStageTestConfiguration != nil {
				jobs.addJob(org, repo, branch, test.As)
			}
		}
	}
	return jobs
}

func searchHandler(confAgent load.ConfigAgent, jobAgent *prowConfig.Agent, w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Now().Sub(start)) }()
	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	searchTerm := req.URL.Query().Get("job")
	matches := getAllMultiStageTests(confAgent, jobAgent)
	if searchTerm != "" {
		matches = searchJobs(matches, searchTerm)
	}
	page := getBaseTemplate(nil, nil, nil)
	page, err := page.Parse(jobSearchPage)
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %v", err), http.StatusInternalServerError)
		return
	}
	writePage(w, "Job Search Page", page, matches)
}

func searchJobs(jobs *Jobs, search string) *Jobs {
	search = strings.TrimPrefix(search, "pull-ci-")
	search = strings.TrimPrefix(search, "branch-ci-")
	matches := &Jobs{}
	for _, org := range jobs.Orgs {
		for _, repo := range org.Repos {
			for _, branch := range repo.Branches {
				for _, test := range branch.Tests {
					fullJobName := fmt.Sprintf("%s-%s-%s-%s", org.Name, repo.Name, branch.Name, test)
					if strings.Contains(fullJobName, search) {
						matches.addJob(org.Name, repo.Name, branch.Name, test)
					}
				}
			}
		}
	}
	return matches
}
