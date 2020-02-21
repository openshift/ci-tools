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

	"github.com/alecthomas/chroma"
	"github.com/alecthomas/chroma/formatters/html"
	"github.com/alecthomas/chroma/lexers"
	"github.com/alecthomas/chroma/styles"
	"github.com/sirupsen/logrus"
	prowConfig "k8s.io/test-infra/prow/config"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/registry"
)

const htmlPageStart = `
<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8"><title>%s</title>
<link rel="stylesheet" href="https://stackpath.bootstrapcdn.com/bootstrap/4.1.3/css/bootstrap.min.css" integrity="sha384-MCw98/SFnGE8fJT3GXwEOngsV7Zt27NXFoaoApmYm81iuXoPkFOJwJ8ERdknLPMO" crossorigin="anonymous">
<script src="https://code.jquery.com/jquery-3.3.1.slim.min.js" integrity="sha384-q8i/X+965DzO0rT7abK41JStQIAqVgRVzpbzo5smXKp4YfRvH+8abtTE1Pi6jizo" crossorigin="anonymous"></script>
<script src="https://stackpath.bootstrapcdn.com/bootstrap/4.1.3/js/bootstrap.min.js" integrity="sha384-ChfqqxuZUCnJSK3+MXmPNIyE6ZbWh2IMqE241rYiqJxyMiZ6OW/JmZQ5stwEULTy" crossorigin="anonymous"></script>
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

pre {
	border: 10px solid transparent;
}
h1, h2, p {
	padding-top: 10px;
}
h1 a:link,
h2 a:link,
h3 a:link,
h4 a:link,
h5 a:link {
  color: inherit;
  text-decoration: none;
}
h1 a:hover,
h2 a:hover,
h3 a:hover,
h4 a:hover,
h5 a:hover {
  text-decoration: underline;
}
h1 a:visited,
h2 a:visited,
h3 a:visited,
h4 a:visited,
h5 a:visited {
  color: inherit;
  text-decoration: none;
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
/* From https://www.w3schools.com/howto/howto_css_fixed_sidebar.asp */
/* The sidebar menu */
.sidenav {
  width: 250px;
  position: fixed;
  z-index: 1;
  top: 20px;
  left: 10px;
  background: #eee;
  overflow-x: hidden;
  padding: 8px 0;
}

.sidenav a {
  padding: 6px 8px 6px 16px;
  text-decoration: none;
  font-size: 1.2rem;
  color: #2196F3;
  display: block;
}

.sidenav a:hover {
  color: #064579;
}
</style>
</head>
<body>
<nav class="navbar navbar-expand-lg navbar-light bg-light">
  <a class="navbar-brand" href="/">Openshift CI Step Registry</a>
  <button class="navbar-toggler" type="button" data-toggle="collapse" data-target="#navbarSupportedContent" aria-controls="navbarSupportedContent" aria-expanded="false" aria-label="Toggle navigation">
    <span class="navbar-toggler-icon"></span>
  </button>

  <div class="collapse navbar-collapse" id="navbarSupportedContent">
    <ul class="navbar-nav mr-auto">
      <li class="nav-item">
        <a class="nav-link" href="/">Home <span class="sr-only">(current)</span></a>
      </li>
      <li class="nav-item">
        <a class="nav-link" href="/search">Jobs</a>
      </li>
      <li class="nav-item dropdown">
        <a class="nav-link dropdown-toggle" href="#" id="navbarDropdown" role="button" data-toggle="dropdown" aria-haspopup="true" aria-expanded="false">
          Help
        </a>
        <div class="dropdown-menu" aria-labelledby="navbarDropdown">
          <a class="dropdown-item" href="/help">Getting Started</a>
          <a class="dropdown-item" href="/help/adding-components">Adding and Changing Content</a>
          <a class="dropdown-item" href="/help/examples">Examples</a>
        </div>
      </li>
    </ul>
    <form class="form-inline my-2 my-lg-0" role="search" action="/search" method="get">
      <input class="form-control mr-sm-2" type="search" placeholder="Prow Job" aria-label="Search" name="job">
      <button class="btn btn-outline-success my-2 my-sm-0" type="submit">Search Jobs</button>
    </form>
  </div>
</nav>
<div class="container">
`

const htmlPageEnd = `
<p class="small">Source code for this page located on <a href="https://github.com/openshift/ci-tools">GitHub</a></p>
</div>
</body>
</html>
`

const errPage = `
{{ . }}
`

const mainPage = `
{{ template "workflowTable" .Workflows }}
{{ template "chainTable" .Chains }}
{{ template "referenceTable" .References}}
`

const referencePage = `
<h2 id="title"><a href="#title">Reference: <nobr style="font-family:monospace">{{ .As }}</nobr></a></h2>
<p id="documentation">{{ .Documentation }}</p>
<h2 id="image"><a href="#image">Container image used for this step: <span style="font-family:monospace">{{ .From }}</span></a></h2>
<h2 id="source"><a href="#source">Source Code</a></h2>
{{ syntaxedSource .Commands }}
`

const chainPage = `
<h2 id="title"><a href="#title">Chains: <nobr style="font-family:monospace">{{ .As }}</nobr></a></h2>
<p id="documentation">{{ .Documentation }}</p>
<h2 id="steps" title="Step run by the chain, in runtime order"><a href="#steps">Steps</a></h2>
{{ template "stepTable" .Steps}}
<h2 id="graph" title="Visual representation of steps run by this chain"><a href="#graph">Step Graph</a></h2>
{{ chainGraph .As }}
`

// workflowJobPage defines the template for both jobs and workflows
const workflowJobPage = `
{{ $type := .Type }}
<h2 id="title"><a href="#title">{{ $type }}: <nobr style="font-family:monospace">{{ .As }}</nobr></a></h2>
{{ if .Documentation }}
	<p id="documentation">{{ .Documentation }}</p>
{{ end }}
{{ if .Steps.ClusterProfile }}
	<h2 id="cluster_profile"><a href="#cluster_profile">Cluster Profile: <span style="font-family:monospace">{{ .Steps.ClusterProfile }}</span></a></h2>
{{ end }}
<h2 id="pre" title="Steps run by this {{ toLower $type }} to set up and configure the tests, in runtime order"><a href="#pre">Pre Steps</a></h2>
{{ template "stepTable" .Steps.Pre }}
<h2 id="test" title="Steps in the {{ toLower $type }} that run actual tests, in runtime order"><a href="#test">Test Steps</a></h2>
{{ template "stepTable" .Steps.Test }}
<h2 id="post" title="Steps run by this {{ toLower $type }} to clean up and teardown test resources, in runtime order"><a href="#post">Post Steps</a></h2>
{{ template "stepTable" .Steps.Post }}
<h2 id="graph" title="Visual representation of steps run by this {{ toLower $type }}"><a href="#graph">Step Graph</a></h2>
{{ workflowGraph .As }}
`

const jobSearchPage = `
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
	<h2 id="workflows"><a href="#workflows">Workflows</a></h2>
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
	<h2 id="chains"><a href="#chains">Chains</a></h2>
	<p>Chains are registry components that allow users to string together multiple test steps under one name. These steps can be references and other chains.</p>
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
	<h2 id="references"><a href="#references">References</a></h2>
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
    <h2 id="jobs"><a href="#jobs">Jobs</a></h2>
	<table class="table">
		<thead>
			<tr>
				<th title="GitHub organization that the job is from" class="info">Org</th>
				<th title="GitHub repo that the job is from" class="info">Repo</th>
				<th title="GitHub branch that the job is from" class="info">Branch</th>
				<th title="The multistage tests in the configuration" class="info">Tests</th>
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

const gettingStartedPage = `
<h2 id="title"><a href="#title">What is the Multistage Test and the Test Step Registry?</a></h2>

<p>
The multistage test style in the <code>ci-operator</code> is a modular test design that
allows users to create new tests by combining smaller, individual test steps.
These individual steps can be put into a shared registry that other tests can
access. This results in test workflows that are easier to maintain and
upgrade as multiple test workflows can share steps and don’t have to each be
updated individually to fix bugs or add new features. It also reduces the
chances of a mistake when copying a feature from one test workflow to
another.
</p>

<p>
To understand how the multistage tests and registry work, we must first talk
about the 3 components of the test registry and how to use those components
to create a test:
<ul>
  <li>
    <a href="#reference">Reference</a>: A reference is the lowest level
    component in the test step registry. It describes an individual test
    step.
  </li>
  <li>
	<a href="#chain">Chain</a>: A chain is a registry component that
	specifies multiple steps to be run. Any item of the chain can be either a
	reference or another chain.
  </li>
  <li>
    <a href="#workflow">Workflow</a>: A workflow is the highest level
    component of the step registry. It contains three chains:
    <code>pre</code>, <code>test</code>, <code>post</code>.
  </li>
</ul>
</p>

<h3 id="reference"><a href="#reference">Reference:</a></h3>
<p>
A reference is the lowest level component in the test step registry. A
reference defines a base container image for a step, the filename of the
shell script to run inside the container, the resource requests and limits
for the container, and documentation for the reference. Example of a
reference:
</p>

{{ yamlSyntax (index . "refExample") }}

<p>
Note: the shell script file must follow the <a href="#layout">naming convention</a> described later
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
A reference may be referred to in chains, workflows, and <code>ci-operator</code> configs.
</p>

<h3 id="chain"><a href="#chain">Chain:</a></h3>
<p>
A chain is a registry component that specifies multiple steps to be run.
Steps are run in the order that they are written. Steps specified by a chain
can be either references and other chains. If any step inside a chain fails
to execute, the chain stops and the following steps are not run. Example of a
chain:
</p>

{{ yamlSyntax (index . "chainExample") }}

<h3 id="workflow"><a href="#workflow">Workflow:</a></h3>
<p>
A workflow is the highest level component of the step registry. It is almost
identical to the syntax of the <code>ci-operator</code> configuration for multistage tests and
defines an entire test from start to finish. It has 4 basic components: a
<code>cluster_profile</code> string (eg: <code>aws</code>, <code>azure4</code>,
<code>gcp</code>), and 3 chains: <code>pre</code>, <code>test</code>, and
<code>post</code>. The <code>pre</code> chain is intended to be used to set
up a testing environment (such as creating a test cluster), the
<code>test</code> chain is intended to contain all tests that a job wants to
run, and the <code>post</code> chain is intended to be used to clean up any
resources created/used by the test. If a step in <code>pre</code> or
<code>test</code> fails, all pending <code>pre</code> and <code>test</code>
steps are skipped and all <code>post</code> steps are run to ensure that
resources are properly cleaned up. This is an example of a workflow configuration:
</p>

{{ yamlSyntax (index . "workflowExample") }}

<h3 id="config"><a href="#config"><code>ci-operator</code> Test Configuration:</a></h3>
<p>
The <code>ci-operator</code> test configuration syntax for multistage tests is very similar to
the registry workflow syntax. The main differences are that the <code>ci-operator</code>
configuration does not have a <code>documentation</code> field, and the <code>ci-operator</code>
configuration can specify a workflow to use. Also, the <code>cluster_profile</code>,
<code>pre</code>, <code>test</code>, and <code>post</code> fields are under a
<code>steps</code> field instead of <code>workflow</code>. Here is an example
of the <code>tests</code> section of a <code>ci-operator</code> configuration using the
multistage test design:
</p>

{{ yamlSyntax (index . "configExample1") }}

Example of a <code>ci-operator</code> configuration that overrides a workflow field.

{{ yamlSyntax (index . "configExample2") }}

<p>
In this example, the <code>ci-operator</code> configuration simply specifies the desired cluster
profile and the <code>origin-e2e</code> workflow shown in the example for the
<code>Workflow</code> section above.
</p>

<p>
Since the <code>ci-operator</code> configuration and workflows share the same fields, it is
possible to override fields specified in a workflow. In cases where both the
workflow and a <code>ci-operator</code> configuration specify the same field, the <code>ci-operator</code> configuration’s
field has priority (i.e. the value from the <code>ci-operator</code> configuration is used).
</p>

<h3 id="layout"><a href="#layout">Registry Layout and Naming Convention:</a></h3>
<p>
To prevent naming collisions between all the registry components, the step
registry has a very strict naming scheme and directory layout. First, all
components have a prefix determined by the directory structure, similar to
how the <code>ci-operator</code> configs do. The prefix is the relative directory path
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

const refExample = `ref:
  as: ipi-conf                   # name of the reference
  from: centos:7                 # image to run the commands in
  commands: ipi-conf-commands.sh # script file containing the command(s) to be run
  resources:
    requests:
      cpu: 1000m
      memory: 100Mi
  documentation: |-
	The IPI configure step generates the install-config.yaml file based on the cluster profile and optional input files.`
const chainExample = `chain:
  as: ipi-deprovision                # name of this chain
  steps:
  - chain: gather                    # a chain being used as a step in another chain
  - ref: ipi-deprovision-deprovision # a reference being used as a step in a chain
  documentation: |-
    The IPI deprovision step chain contains all the individual steps necessary to deprovision an OpenShift cluster.`
const workflowExample = `workflow:
  as: origin-e2e             # name of workflow
  steps:
    pre:                     # "pre" chain used to set up test environment
    - ref: ipi-conf
    - chain: ipi-install
    test:                    # "test" chain containing actual tests to be run
    - ref: origin-e2e-test
    post:                    # "post" chain containing cleanup steps
    - chain: ipi-deprovision
  documentation: |-
	The Origin E2E workflow executes the common end-to-end test suite.`
const configExample1 = `tests:
- as: e2e-steps # test name
  steps:
    cluster_profile: aws
    workflow: origin-e2e`
const configExample2 = `tests:
- as: e2e-steps # test name
  steps:
    cluster_profile: aws
	workflow: origin-e2e
	test:                     # this chain will be run for "test" instead of the one in the origin-e2e workflow
	  ref: origin-e2e-minimal`

const addingComponentPage = `
<h2>Adding and Changing Step Registry Content</h2>

<h3 id="adding-content"><a href="#adding-contnet">Adding Content</a></h3>
<p>
Adding a new component (reference, chain, or workflow) to the registry is
quite simple. Descriptions of each of the components as well as the naming
scheme and directory layout is available at the <a href="/help/getting-started">
Getting Started</a> page. To add a new component, add the new files into the
<code>ci-operator/step-registry</code> directory in
<code>openshift/release</code> following the naming scheme along with an
<code>OWNERS</code> file for the new component and open a PR.
</p>

Prow will automatically run a few tests on registry components.
<ul>
  <li>Verify that all required fields are supplied</li>
  <li>Verify that the naming scheme for all components is correct</li>
  <li>Verify that there are no cyclic dependencies (infinite loops) in chains</li>
  <li>Run shellcheck on all shell files used by references, failing on errors</li>
</ul>

<p>
If a new test is added that uses the new component as well,
<code>pj-rehearse</code> will test the new job with the new component.
</p>

<h3 id="changing-content"><a href="#changing-contnet">Changing Content</a></h3>
<p>
To change registry content, make the changes in
<code>openshift/release</code> and open a new PR. Prow will run all of the
same checks on the registry listed in the above “Adding Content” section and
run rehearsals for all jobs that use the changed registry component. The
component will require approval and an lgtm from one of the people listed in
the <code>OWNERS</code> file for the component, located in the same directory
as the component.
</p>
`
const examplesPage = `
<h2 id="examples"><a href="#examples">Available Examples</a></h2>
<ul>
  <li><a href="#aws">How do I add a job that runs the standard e2e tests on AWS?</a></li>
  <li><a href="#image">How do I use an image from another repo in my repo’s tests?</a></li>
</ul>

<h3 id="aws"><a href="#aws">How do I add a job that runs the standard e2e tests on AWS?</a></h3>
<p>
Use the <code>origin-e2e</code> workflow and set <code>cluster_profile</code>
to <code>aws</code>.
</p>
Example:
{{ yamlSyntax (index . "awsExample") }}

<h3 id="image"><a href="#image">How do I use an image from another repo in my repo’s tests?</a></h3>
<p>
In order to use an image from one repository in the tests of another, it is necessary
to first publish the image from the producer repository and import it in the consumer
repository. Generally, a central <code>ImageStream</code> is used for continuous
integration; a repository opts into using an integration stream with the <code>tag_specification</code>
field in the <code>ci-operator</code> configuration and opts into publishing to the
stream with the <code>promotion</code> field.
</p>

<h4 id="image-publication"><a href="#image-publication">Publishing an Image For Reuse</a></h3>
<p>
When configuring <code>ci-operator</code> for a repository, the <code>promotion</code>
stanza declares which container images are published and defines the integration
<code>ImageStream</code> where they will be available. By default, all container images
declared in the <code>images</code> block of a <code>ci-operator</code> configuration
are published when a <code>promotion</code> stanza is present to define the integration
<code>ImageStream</code>. Promotion can be futhermore configured to include other images,
as well. In the following <code>ci-operator</code> configuration, the following images
are promoted for reuse by other repositories to the <code>ocp/4.4</code> integration
<code>ImageStream</code>:
</p>
<ul>
  <li>the <code>pipeline:src</code> tag, published as <code>ocp/4.4:repo-scripts</code> containing the latest version of the repository to allow for executing helper scripts</li>
  <li>the <code>pipeline:test-bin</code> tag, published as <code>ocp/4.4:repo-tests</code> containing built test binaries to allow for running the repository's tests</li>
  <li>the <code>stable:component</code> tag, published as <code>ocp/4.4:component</code> containing the component itself to allow for deployments and installations in end-to-end scenarios</li>
</ul>

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "imagePromotionConfig") }}

<h4 id="image-consumption"><a href="#image-consumption">Consuming an Image</a></h3>
<p>
Once a repository is publishing an image for reuse by others, downstream users can
configure <code>ci-operator</code> to use that image in tests by including it as a
<code>base_image</code> or as part of the <code>tag_specification</code>. In general,
images will be available as part of the <code>tag_specification</code> and explicitly
including them as a <code>base_image</code> will only be necessary if the promoting
repository is exposing them to a non-standard <code>ImageStream</code>. Regardless of
which workflow is used to consume the image, the resulting tag will be available under
the <code>stable</code> <code>ImageStream</code>. The following <code>ci-operator</code>
configuration imports a number of images:
</p>
<ul>
  <li>the <code>stable:custom-scripts</code> tag, published as <code>myregistry.com/project/custom-scripts:latest</code></li>
  <li>the <code>stable:component</code> and <code>:repo-{scripts|tests}</code> tags, by virtue of them being published under <code>ocp/4.4</code> and brought in with the <code>tag_specification</code></li>
</ul>

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "imageConsumptionConfig") }}

<p>
Once the image has been configured to be an input for the repository's tests in the
<code>ci-operator</code> configuration, either explicitly as a <code>base_image</code>
or implicitly as part of the <code>tag_specification</code>, it can be used in tests
in one of two ways. A registry reference step can be written to execute the shared tests
in any <code>ci-operator</code> configuration, or a literal test step can be added just to one
repository's configuration to run the shared tests. Two examples follow which add an
execution of shared end-to-end tests using these two approaches. Both examples assume
that we have the <code>ipi</code> workflow available to use.
</p>

<h5 id="adding-step"><a href="#adding-step">Adding a Reusable Test Step:</a></h4>

<p>
Full directions for adding a new resuable test step can be found in the overview for
<a href="./adding-components#adding-content">new registry content</a>. An example of the process
is provided here. First, make directory for the test step in the registry:
<code>ci-operator/step-registry/org/repo/e2e</code>.
</p>

Then, declare a reusable step: <code>ci-operator/step-registry/org/repo/e2e/org-repo-e2e-ref.yaml</code>
{{ yamlSyntax (index . "imageExampleRef") }}

Finally, populate a command file for the step: <code>ci-operator/step-registry/org/repo/e2e/org-repo-e2e-commands.sh</code>
{{ bashSyntax (index . "imageExampleCommands") }}

Now the test step is ready for use by any repository. To make use of it, update
<code>ci-operator</code> configuration for a separate repository under
<code>ci-operator/config/org/other/org-other-master.yaml</code>:
{{ yamlSyntax (index . "imageExampleConfig") }}

<h5 id="adding-literal"><a href="#adding-literal">Adding a Literal Test Step</a></h4>
<p>
It is possible to directly declare a test step in the
<code>ci-operator</code> configuration without adding a new registry component.
However, this is usually not recommended for most use cases as commands must
be inlined (making multilined scripts difficult to handle) and the steps are
not reusable by other tests:
</p>
<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "imageExampleLiteral") }}
`

const awsExample = `- as: e2e-steps
  steps:
    cluster_profile: aws
    workflow: origin-e2e
`
const imagePromotionConfig = `test_binary_build_commands: go test -race -c -o e2e-tests # will create the test-bin tag
promotion:
  additional_images:
    repo-scripts: src    # promotes "src" as "repo-scripts" 
    repo-tests: test-bin # promotes "test-bin" as "repo-tests"
  namespace: ocp
  name: 4.4
images:
- from: ubi8
  to: component # promotes "component" by default
  context_dir: images/component
`
const imageConsumptionConfig = `base_images:
  custom-scripts:
    cluster: myregistry.com
    namespace: project
    name: custom-scripts
    tag: latest
tag_specification:
  namespace: ocp
  name: 4.4
`
const imageExampleRef = `ref:
  as: org-repo-e2e
  from: stable:repo-tests
  commands: org-repo-e2e-commands.sh
  resources:
    requests:
      cpu: 1000m
      memory: 100Mi
  documentation: |-
    Runs the end-to-end suite published by org/repo.
`
const imageExampleCommands = `#!/bin/bash
e2e-tests # as built by go test -c
`
const imageExampleConfig = `- as: org-repo-e2e
  steps:
    cluster_profile: aws
    workflow: ipi
    test:
    - ref: org-repo-e2e
`
const imageExampleLiteral = `- as: repo-e2e
  steps:
    cluster_profile: aws
	workflow: ipi
    test:
    - as: e2e
      from: stable:repo-tests
      commands: |-
        #!/bin/bash
        e2e-tests # as built by go test -c
      resources:
        requests:
          cpu: 1000m
          memory: 2Gi
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

func helpHandler(subPath string, w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Now().Sub(start)) }()
	helpFuncs := template.New("helpPage").Funcs(
		template.FuncMap{
			"yamlSyntax": func(source string) template.HTML {
				formatted, err := syntaxYAML(source)
				if err != nil {
					logrus.Errorf("Failed to format source file: %v", err)
					return template.HTML(source)
				}
				return template.HTML(formatted)
			},
			"bashSyntax": func(source string) template.HTML {
				formatted, err := syntaxBash(source)
				if err != nil {
					logrus.Errorf("Failed to format source file: %v", err)
					return template.HTML(source)
				}
				return template.HTML(formatted)
			},
		},
	)
	var helpTemplate *template.Template
	var err error
	data := make(map[string]string)
	switch subPath {
	case "":
		helpTemplate, err = helpFuncs.Parse(gettingStartedPage)
		data["refExample"] = refExample
		data["chainExample"] = chainExample
		data["workflowExample"] = workflowExample
		data["configExample1"] = configExample1
		data["configExample2"] = configExample2
	case "/adding-components":
		helpTemplate, err = helpFuncs.Parse(addingComponentPage)
	case "/examples":
		helpTemplate, err = helpFuncs.Parse(examplesPage)
		data["awsExample"] = awsExample
		data["imageExampleRef"] = imageExampleRef
		data["imageExampleCommands"] = imageExampleCommands
		data["imageExampleConfig"] = imageExampleConfig
		data["imageExampleLiteral"] = imageExampleLiteral
		data["imagePromotionConfig"] = imagePromotionConfig
		data["imageConsumptionConfig"] = imageConsumptionConfig
	default:
		writeErrorPage(w, errors.New("Invalid path"), http.StatusNotImplemented)
		return
	}
	if err != nil {
		writeErrorPage(w, err, http.StatusInternalServerError)
		return
	}
	writePage(w, "Step Registry Help Page", helpTemplate, data)
}

func mainPageHandler(agent agents.RegistryAgent, templateString string, w http.ResponseWriter, req *http.Request) {
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
	}{
		References: refs,
		Chains:     chains,
		Workflows:  wfs,
	}
	writePage(w, "Step Registry Help Page", page, comps)
}

func WebRegHandler(regAgent agents.RegistryAgent, confAgent agents.ConfigAgent, jobAgent *prowConfig.Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		trimmedPath := strings.TrimPrefix(req.URL.Path, req.URL.Host)
		// remove leading slash
		trimmedPath = strings.TrimPrefix(trimmedPath, "/")
		// remove trailing slash
		trimmedPath = strings.TrimSuffix(trimmedPath, "/")
		splitURI := strings.Split(trimmedPath, "/")
		if len(splitURI) >= 1 && splitURI[0] == "help" {
			helpHandler(strings.TrimPrefix(trimmedPath, "help"), w, req)
			return
		} else if len(splitURI) == 1 {
			switch splitURI[0] {
			case "":
				mainPageHandler(regAgent, mainPage, w, req)
			case "search":
				searchHandler(confAgent, jobAgent, w, req)
			default:
				writeErrorPage(w, errors.New("Invalid path"), http.StatusNotImplemented)
			}
			return
		} else if len(splitURI) == 2 {
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

func syntax(source string, lexer chroma.Lexer) (string, error) {
	var output bytes.Buffer
	style := styles.Get("dracula")
	formatter := html.New(html.Standalone(false))
	iterator, err := lexer.Tokenise(nil, source)
	if err != nil {
		return "", fmt.Errorf("failed to tokenise source: %w", err)
	}
	err = formatter.Format(&output, style, iterator)
	return output.String(), err
}

func syntaxYAML(source string) (string, error) {
	return syntax(source, lexers.Get("yaml"))
}

func syntaxBash(source string) (string, error) {
	return syntax(source, lexers.Get("bash"))
}

func referenceHandler(agent agents.RegistryAgent, w http.ResponseWriter, req *http.Request) {
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

func chainHandler(agent agents.RegistryAgent, w http.ResponseWriter, req *http.Request) {
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

func workflowHandler(agent agents.RegistryAgent, w http.ResponseWriter, req *http.Request) {
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

func findConfigForJob(jobName string, configs agents.FilenameToConfig) (api.MultiStageTestConfiguration, error) {
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

func jobHandler(regAgent agents.RegistryAgent, confAgent agents.ConfigAgent, w http.ResponseWriter, req *http.Request) {
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
func getAllMultiStageTests(confAgent agents.ConfigAgent, jobAgent *prowConfig.Agent) *Jobs {
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

func searchHandler(confAgent agents.ConfigAgent, jobAgent *prowConfig.Agent, w http.ResponseWriter, req *http.Request) {
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
