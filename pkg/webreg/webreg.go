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
	"k8s.io/test-infra/prow/repoowners"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/registry"
)

const (
	OrgQuery     = "org"
	RepoQuery    = "repo"
	BranchQuery  = "branch"
	VariantQuery = "variant"
	TestQuery    = "test"
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
          <a class="dropdown-item" href="/help/ci-operator">CI Operator Overview</a>
          <a class="dropdown-item" href="/help/leases">Leases and Quota</a>
          <a class="dropdown-item" href="/help/private-repositories">Private Repositories</a>
          <a class="dropdown-item" href="/help/adding-components">Adding and Changing Content</a>
          <a class="dropdown-item" href="/help/release">Contributing to <code>openshift/release</code></a>
          <a class="dropdown-item" href="/help/operators">OLM Operator Support</a>
          <a class="dropdown-item" href="/help/examples">Examples</a>
          <a class="dropdown-item" href="/help/links">Useful links</a>
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
</html>`

const errPage = `
{{ . }}
`

const mainPage = `
{{ template "workflowTable" .Workflows }}
{{ template "chainTable" .Chains }}
{{ template "referenceTable" .References}}
`

const referencePage = `
<h2 id="title"><a href="#title">Step:</a> <nobr style="font-family:monospace">{{ .Reference.As }}</nobr></h2>
<p id="documentation">{{ .Reference.Documentation }}</p>
<h2 id="image"><a href="#image">Container image used for this step:</a> <span style="font-family:monospace">{{ .Reference.From }}</span></h2>
<h2 id="source"><a href="#source">Source Code</a></h2>
{{ syntaxedSource .Reference.Commands }}
<h2 id="github"><a href="#github">GitHub Link:</a></h2>{{ githubLink .Metadata.Path }}
{{ ownersBlock .Metadata.Owners }}
`

const chainPage = `
<h2 id="title"><a href="#title">Chains:</a> <nobr style="font-family:monospace">{{ .Chain.As }}</nobr></h2>
<p id="documentation">{{ .Chain.Documentation }}</p>
<h2 id="steps" title="Step run by the chain, in runtime order"><a href="#steps">Steps</a></h2>
{{ template "stepTable" .Chain.Steps}}
<h2 id="graph" title="Visual representation of steps run by this chain"><a href="#graph">Step Graph</a></h2>
{{ chainGraph .Chain.As }}
<h2 id="github"><a href="#github">GitHub Link:</a></h2>{{ githubLink .Metadata.Path }}
{{ ownersBlock .Metadata.Owners }}
`

// workflowJobPage defines the template for both jobs and workflows
const workflowJobPage = `
{{ $type := .Workflow.Type }}
<h2 id="title"><a href="#title">{{ $type }}:</a> <nobr style="font-family:monospace">{{ .Workflow.As }}</nobr></h2>
{{ if .Workflow.Documentation }}
	<p id="documentation">{{ .Workflow.Documentation }}</p>
{{ end }}
{{ if .Workflow.Steps.ClusterProfile }}
	<h2 id="cluster_profile"><a href="#cluster_profile">Cluster Profile:</a> <span style="font-family:monospace">{{ .Workflow.Steps.ClusterProfile }}</span></h2>
{{ end }}
<h2 id="pre" title="Steps run by this {{ toLower $type }} to set up and configure the tests, in runtime order"><a href="#pre">Pre Steps</a></h2>
{{ template "stepTable" .Workflow.Steps.Pre }}
<h2 id="test" title="Steps in the {{ toLower $type }} that run actual tests, in runtime order"><a href="#test">Test Steps</a></h2>
{{ template "stepTable" .Workflow.Steps.Test }}
<h2 id="post" title="Steps run by this {{ toLower $type }} to clean up and teardown test resources, in runtime order"><a href="#post">Post Steps</a></h2>
{{ template "stepTable" .Workflow.Steps.Post }}
<h2 id="graph" title="Visual representation of steps run by this {{ toLower $type }}"><a href="#graph">Step Graph</a></h2>
{{ workflowGraph .Workflow.As .Workflow.Type }}
{{ if eq $type "Workflow" }}
<h2 id="github"><a href="#github">GitHub Link:</a></h2>{{ githubLink .Metadata.Path }}
{{ ownersBlock .Metadata.Owners }}
{{ end }}
`

const jobSearchPage = `
{{ template "jobTable" . }}
`

const templateDefinitions = `
{{ define "nameWithLink" }}
	<nobr><a href="/{{ .Type }}/{{ .Name }}" style="font-family:monospace">{{ .Name }}</a></nobr>
{{ end }}

{{ define "nameWithLinkReference" }}
	<nobr><a href="/reference/{{ . }}" style="font-family:monospace">{{ . }}</a></nobr>
{{ end }}

{{ define "nameWithLinkChain" }}
	<nobr><a href="/chain/{{ . }}" style="font-family:monospace">{{ . }}</a></nobr>
{{ end }}

{{ define "nameWithLinkWorkflow" }}
	<nobr><a href="/workflow/{{ . }}" style="font-family:monospace">{{ . }}</a></nobr>
{{ end }}

{{ define "stepTable" }}
{{ if not . }}
	<p>No test steps configured.</p>
{{ else }}
	<table class="table">
	<thead>
		<tr>
			<th title="The name of the step or chain" class="info">Name</th>
			<th title="The documentation for the step or chain" class="info">Description</th>
		</tr>
	</thead>
	<tbody>
		{{ range $index, $step := . }}
			<tr>
				{{ $nameAndType := testStepNameAndType $step }}
				{{ $doc := docsForName $nameAndType.Name }}
				{{ if not $step.LiteralTestStep }}
					<td>{{ template "nameWithLink" $nameAndType }}</td>
				{{ else }}
					<td>{{ $nameAndType.Name }}</td>
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
		{{ $nameAndType := testStepNameAndType $step }}
		<li>{{ template "nameWithLink" $nameAndType }}</li>
	{{ end }}
	</ul>
{{ end }}

{{ define "workflowTable" }}
	<h2 id="workflows"><a href="#workflows">Workflows</a></h2>
	<p>Workflows are the highest level registry components, defining a test from start to finish.</p>
	<table class="table">
		<thead>
			<tr>
				<th title="The name of the workflow and what the workflow is supposed to do" class="info">Name and Description</th>
				<th title="The registry components used during the Pre, Test, and Post sections of the workflow" class="info">Steps</th>
			</tr>
		</thead>
		<tbody>
			{{ range $name, $config := . }}
				<tr>
					<td><b>Name:</b> {{ template "nameWithLinkWorkflow" $name }}<p>
						<b>Description:</b><br>{{ docsForName $name }}
					</td>
					<td>{{ if gt (len $config.Pre) 0 }}<b>Pre:</b>{{ template "stepList" $config.Pre }}{{ end }}
					    {{ if gt (len $config.Test) 0 }}<b>Test:</b>{{ template "stepList" $config.Test }}{{ end }}
						{{ if gt (len $config.Post) 0 }}<b>Post:</b>{{ template "stepList" $config.Post }}{{ end }}
					</td>
				</tr>
			{{ end }}
		</tbody>
	</table>
{{ end }}

{{ define "chainTable" }}
	<h2 id="chains"><a href="#chains">Chains</a></h2>
	<p>Chains are registry components that allow users to string together multiple registry components under one name. These components can be steps and other chains.</p>
	<table class="table">
		<thead>
			<tr>
				<th title="The name of the chain" class="info">Name</th>
				<th title="What the chain is supposed to do" class="info">Description</th>
				<th title="The components (steps and other chains) that the chain runs (in order)" class="info">Steps</th>
			</tr>
		</thead>
		<tbody>
			{{ range $name, $config := . }}
				<tr>
					<td>{{ template "nameWithLinkChain" $name }}</td>
					<td>{{ docsForName $name }}</td>
					<td>{{ template "stepList" $config.Steps }}</td>
				</tr>
			{{ end }}
		</tbody>
	</table>
{{ end }}

{{ define "referenceTable" }}
	<h2 id="steps"><a href="#steps">Steps</a></h2>
	<p>Steps are the lowest level registry components, defining a command to run and a container to run the command in.</p>
	<table class="table">
		<thead>
			<tr>
				<th title="The name of the step" class="info">Name</th>
				<th title="The documentation for the step" class="info">Description</th>
			</tr>
		</thead>
		<tbody>
			{{ range $name, $config := . }}
				<tr>
					<td>{{ template "nameWithLinkReference" $name }}</td>
					<td>{{ docsForName $name }}</td>
				</tr>
			{{ end }}
		</tbody>
	</table>
{{ end }}

{{ define "jobTable" }}
    <h2 id="jobs"><a href="#jobs">Jobs</a></h2>
	<table class="table">
	{{ $containsVariant := .ContainsVariant }}
		<thead>
			<tr>
				<th title="GitHub organization that the job is from" class="info">Org</th>
				<th title="GitHub repo that the job is from" class="info">Repo</th>
				<th title="GitHub branch that the job is from" class="info">Branch</th>
				{{ if $containsVariant }}
					<th title="Variant of the ci-operator config" class="info">Variant</th>
				{{ end }}
				<th title="The multistage tests in the configuration" class="info">Tests</th>
			</tr>
		</thead>
		<tbody>
			{{ range $index, $org := .Orgs }}
				<tr>
					<td rowspan="{{ (orgSpan $org $containsVariant) }}" style="vertical-align: middle;">{{ $org.Name }}</td>
				</tr>
				{{ range $index, $repo := $org.Repos }}
					<tr>
						<td rowspan="{{ (repoSpan $repo $containsVariant) }}" style="vertical-align: middle;">{{ $repo.Name }}</td>
					</tr>
					{{ range $index, $branch := $repo.Branches }}
						{{ $branchLen := len $branch.Variants }}
						{{ if $containsVariant }}
							{{ $branchLen = inc $branchLen}}
						{{ end }}
						{{ if gt (len $branch.Tests) 0 }}
							{{ $branchLen = inc $branchLen}}
						{{ end }}
						<tr>
							<td rowspan="{{ $branchLen }}" style="vertical-align: middle;">{{ $branch.Name }}</td>
							{{ if gt (len $branch.Tests) 0 }}
								{{ if $containsVariant }}
						</tr>
						<tr>
							<td style="vertical-align: middle;"></td>
								{{ end }} <!-- if $containsVariant -->
							<td>
								<ul>
								{{ range $index, $test := $branch.Tests }}
									<li><nobr><a href="/job?org={{$org.Name}}&repo={{$repo.Name}}&branch={{$branch.Name}}&test={{$test}}" style="font-family:monospace">{{$test}}</a></nobr></li>
								{{ end }}
								</ul>
							</td>
							{{ end }} <!-- if gt (len $branch.Tests) 0 -->
						</tr>
						{{ range $index, $variant := $branch.Variants }}
							<tr>
								<td style="vertical-align: middle;">{{ $variant.Name }}</td>
								<td>
								<ul>
									{{ range $index, $test := $variant.Tests }}
										<li><nobr><a href="/job?org={{$org.Name}}&repo={{$repo.Name}}&branch={{$branch.Name}}&test={{$test}}&variant={{$variant.Name}}" style="font-family:monospace">{{$test}}</a></nobr></li>
									{{ end }}
								</ul>
								</td>
							</tr>
						{{ end }}
					{{ end }}
				{{ end }}
			{{ end }}
		</tbody>
	</table>
{{ end }}
`

const optionalOperatorOverviewPage = `<h2 id="title"><a href="#title">Testing Operators Built With The Operator SDK and Deployed Through OLM</a></h2>

<p>
<code>ci-operator</code> supports building, deploying, and testing operator
bundles, whether the operator repository uses the Operator SDK or not. This
document outlines how to configure <code>ci-operator</code> to build bundle and
index images and use those in end-to-end tests.
</p>

<p>
Consult the <code>ci-operator</code> <a href="/help/ci-operator">overview</a> and
the step environment <a href="/help">reference</a> for detailed descriptions of the
broader test infrastructure that an operator test is defined in.
</p>

<h3 id="operator-artifacts"><a href="#operator-artifacts">Building Artifacts for OLM Operators</a></h3>

<p>
Multiple different images are involved in installing and testing
candidate versions of OLM-delivered operators: operand, operator, bundle, and
index images. Operand and operator images are built normally using the
<code>images</code> stanza in <a href="/help/ci-operator#images"><code>ci-operator</code> configuration</a>.
OLM uses bundle and index images to install the desired version of an operator.
<code>ci-operator</code> can build ephemeral versions of these images suitable
for installation and testing, but not for production.
</p>

<h4 id="bundles"><a href="#bundles">Building Operator Bundles</a></h4>

<p>
Configuring <code>ci-operator</code> to build operator bundles from a
repository is as simple as adding a new <code>operator</code> stanza,
specifying the bundles built from the repository, and what sorts of
container image pull specification substitutions are necessary during bundle
build time. Substitutions allow for the operator manifests to refer to images
that were built from the repository during the test or imported from other
sources. The following example builds an operator and then a bundle. While building
the bundle, the operator's pull specification in manifests are replaced with the
operator version built during the test:
</p>

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "optionalOperatorBundleConfig") }}

<p>
When configuring a bundle build, two options are available:
</p>

<ul>
  <li><code>dockerfile_path</code>: a path to the Dockerfile that builds the bundle image, defaulting to <code>bundle.Dockerfile</code></li>
  <li><code>context_dir</code>: base directory for the bundle image build, defaulting to the root of the source tree</li>
</ul>

<p>
The <code>operator.bundles</code> stanza is a list, so it is possible to build
multiple bundle images from one repository.
</p>

<h4 id="index"><a href="#index">Building an Index</a></h4>

<p>
When <code>ci-operator</code> builds at least one operator bundle from a
repository, it will also automatically build an ephemeral index image to package
those bundles. Test workloads should consume the bundles via this index
image. The index image is named <code>ci-index</code> and can be exposed to test
steps via the <a href="/help/ci-operator#literal-references"><code>dependencies</code></a> feature.
</p>

<p>
The ephemeral index is built from scratch and only the bundles built in the
current <code>ci-operator</code> run will be added to it, nothing else. The
bundles are added to the index using the <code>semver</code> mode, which means
that the <code>spec.version</code> stanza in the CSV must be a valid semantic
version. Also, if the CSV has a <code>spec.replaces</code> stanza, it is
ignored, because the index will not contain a bundle with the replaced version.
</p>

<h4 id="ci-index-jobs"><a href="#ci-index-jobs">Validating Bundle and Index Builds</a></h4>

<p>
Similarly to how the job generator automatically creates a <code>pull-ci-$ORG-$REPO-$BRANCH-images</code>
job to test image builds when <code>ci-operator</code> configuration has an
<code>images</code> stanza, it will also make a separate job that builds the
configured bundle and index images. This job, named <code>pull-ci-$ORG-$REPO-$BRANCH-ci-index</code>,
is created only when an <code>operator</code> stanza is present.
</p>

<h3 id="tests"><a href="#tests">Running Tests</a></h3>

<p>
Once <code>ci-operator</code> builds the operator bundle and index, they are
available to be used as a <code>CatalogSource</code> by OLM for deploying and
testing the operator. The index image is called <code>ci-index</code> and can
be exposed to multi-stage test workloads via the <a href="/help/ci-operator#literal-references">
<code>dependencies</code> feature</a>:
</p>

Step configuration example:
{{ yamlSyntax (index . "optionalOperatorIndexConsumerStep") }}

<p>
Any test workflow involving such step will require <code>ci-operator</code> to
build the index image before it executes the workflow. The <code>OO_INDEX</code>
environmental variable set for the step will contain the pull specification of
the index image.
</p>

<h3 id="oo-steps">Step Registry Content for Operators</h3>

<p>
The step registry contains several generic steps and workflows that implement the
common operations involving operators. We encourage operator repositories to
consider using (and possibly improving) these shared steps and workflows over
implementing their own from scratch.
</p>

<h4>Simple Operator Installation</h4>

<p>
The <code>optional-operators-ci-$CLOUD</code> (<a href="/workflow/optional-operators-ci-aws">aws</a>
, <a href="/workflow/optional-operators-ci-gcp">gcp</a>, <a href="/workflow/optional-operators-ci-azure">azure</a>)
family of workflows take the following steps to set up the test environment:
</p>

<ul>
  <li>deploy an ephemeral OpenShift cluster to test against</li>
  <li>create a <code>Namespace</code> to install into</li>
  <li>create an <code>OperatorGroup</code> and <code>CatalogSource</code> (referring to built index) to configure OLM</li>
  <li>create a <code>Subscription</code> for the operator under test</li>
  <li>wait for the operator under test to install and deploy</li>
</ul>

<p>
These workflows enhance the general installation workflows (like
<a href="/workflow/ipi-aws">ipi-aws</a>) with an additional
<a href="/reference/optional-operators-ci-subscribe">optional-operators-ci-subscribe</a>
step. Tests using these workflows need to provide the following parameters:
</p>

<table class="table">
  <tr>
    <th style="white-space: nowrap">Parameter</th>
    <th>Description</th>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>OO_PACKAGE</code></td>
    <td>The name of the operator package to be installed.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>OO_CHANNEL</code></td>
    <td>The name of the operator channel to track.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>OO_INSTALL_NAMESPACE</code></td>
    <td>The namespace into which the operator and catalog will be installed. Special, default value <code>!create</code> means that a new namespace will be created.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>OO_TARGET_NAMESPACES</code></td>
    <td>A comma-separated list of namespaces the operator will target. Special,
      default value <code>!all</code> means that all namespaces will be targeted.
      If no <code>OperatorGroup</code> exists in <code>$OO_INSTALL_NAMESPACE</code>,
      a new one will be created with its target namespaces set to <code>$OO_TARGET_NAMESPACES</code>.
      Otherwise, the existing <code>OperatorGroup</code>'s target namespace set
      will be replaced. The special value <code>!install</code> will set the
      target namespace to the operator's installation namespace.</td>
  </tr>
</table>

<p>
The combination of <code>OO_INSTALL_NAMESPACE</code> and <code>OO_TARGET_NAMESPACES</code>
values determines the <code>InstallMode</code> when installing the operator. The
default <code>InstallMode</code> is <code>AllNamespaces</code> (the operator will
be installed into a newly created namespace of a random name, targeting all
namespaces).
</p>

<p>
A user-provided test can expect to have <code>${KUBECONFIG}</code> set, with
administrative privileges, and for the operator under test to be fully deployed
at the time that the test begins. The following example runs a test in this manner:
</p>

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "optionalOperatorTestConfig") }}
`

const optionalOperatorBundleConfig = `base_images:
  ubi:               # imports the UBI base image for building
    namespace: "ocp"
    name: "ubi"
    tag: "8"
  operand:           # imports the latest operand image
    namespace: "ocp"
    name: "operand"
    tag: "latest"
images:
- from: "ubi"
  to: "tested-operator"
operator:
  bundles: # entries create bundle images from Dockerfiles and an index containing all bundles
  - dockerfile_path: "path/to/Dockerfile" # defaults to bundle.Dockerfile
    context_dir: "path/"                  # defaults to .
  substitutions:
  # replace references to the operand with the imported version (base_images stanza)
  - pullspec: "quay.io/openshift/operand:1.3"
    with: "stable:operand"
  # replace references to the operator with the built version (images stanza)
  - pullspec: "quay.io/openshift/tested-operator:1.3"
    with: "pipeline:tested-operator"
`

const optionalOperatorIndexConsumerStep = `ref:
  as: "step-consuming-ci-index"
  from: "cli"
  commands: "step-consuming-ci-index.sh"
  dependencies:
  - env: "OO_INDEX"
    name: "ci-index"
  documentation: ...
`

const optionalOperatorTestConfig = `tests:
- as: "operator-e2e"
  steps:
    workflow: "optional-operators-ci-aws"
    cluster_profile: "aws"
    env:
      OO_CHANNEL: "1.2.0"
      OO_INSTALL_NAMESPACE: "kubevirt-hyperconverged"
      OO_PACKAGE: "kubevirt-hyperconverged"
      OO_TARGET_NAMESPACES: '!install'
    test:
    - as: "e2e"
      from: "src"               # the end-to-end tests run in the source repository
      commands: "make test-e2e" # the commands to run end-to-end tests
      resources:
        requests:
          cpu: 100m
          memory: 200Mi
`

const ciOperatorOverviewPage = `<h2 id="title"><a href="#title">What is <code>ci-operator</code> and how does it work?</a></h2>

<p>
<code>ci-operator</code> is a highly opinionated test workflow execution engine
that knows about how OpenShift is built, released and installed. <code>ci-operator</code>
hides the complexity of assembling an ephemeral OpenShift 4.x release payload,
thereby allowing authors of end-to-end test suites to focus on the content of
their tests and not the infrastructure required for cluster setup and installation.
</p>

<p>
<code>ci-operator</code> allows for components that make up an OpenShift
release to be tested together by allowing each component repository to
test with the latest published versions of all other components. An
integration stream of container images is maintained with the latest
tested versions of every component. A test for any one component snapshots
that stream, replaces any images that are being tested with newer versions,
and creates an ephemeral release payload to support installing an OpenShift
cluster to run end-to-end tests.
</p>

<p>
In addition to giving first-class support for testing OpenShift components,
<code>ci-operator</code> expects to run in an OpenShift cluster and uses
OpenShift features like <code>Builds</code> and <code>ImageStreams</code>
extensively, thereby exemplifying a complex OpenShift user workflow and
making use of the platform itself. Each test with a unique set of inputs
will have a <code>Namespace</code> provisioned to hold the OpenShift objects
that implement the test workflow.
</p>

<p>
<code>ci-operator</code> needs to understand a few important characteristics of
any repository it runs tests for. This document will begin by walking through
those characteristics and how they are exposed in the configuration. With an
understanding of those building blocks, then, the internal workflow of
<code>ci-operator</code> will be presented.
</p>

<h3 id="configuration"><a href="#configuration">Configuring <code>ci-operator</code>: Defining A Repository</a></h3>

<p>
At a high level, when a repository author writes a <code>ci-operator</code>
configuration file, they are describing how a repository produces output
artifacts, how those artifacts fit into the larger OpenShift release and
how those artifacts should be tested. The following examples will describe
the configuration file as well as walk through how <code>ci-operator</code>
creates OpenShift objects to fulfill their intent.
</p>

<h4 id="inputs"><a href="#inputs">Configuring Inputs</a></h4>

<p>
When <code>ci-operator</code> runs tests to verify proposed changes in a pull
request to a component repository, it must first build the output artifacts
from the repository. In order to generate these builds, <code>ci-operator</code>
needs to know the inputs from which they will be created. A number of inputs
can be configured; the following example provides both:
</p>
<ul>
  <li><code>base_images</code>: provides a mapping of named <code>ImageStreamTags</code> which will be available for use in container image builds</li>
  <li><code>build_root</code>: defines the <code>ImageStreamTag</code> in which dependencies exist for building executables and non-image artifacts</li>
</ul>

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "ciOperatorInputConfig") }}

<p>
As <code>ci-operator</code> is an OpenShift-native tool, all image references
take the form of an <code>ImageStreamTag</code> on the build farm cluster, not
just a valid pull-spec for an image. <code>ci-operator</code> will import these
<code>ImageStreamTags</code> into the <code>Namespace</code> created for the
test workflow; snapshotting the current state of inputs to allow for reproducible
builds.
</p>

<p>
If an image that is required for building is not yet present on the cluster,
either:
</p>

<ul>
  <li>
    The correct <code>ImageStream</code> should be declared and committed to
    the <code>openshift/release</code> repository <a
    href="https://github.com/openshift/release/tree/master/core-services/supplemental-ci-images">here.</a></li>
  </li>
  <li>
	The image referenced in <code>base_images</code> has to be accessible. The
	simplest RBAC rule to achieve this is to allow the
	<code>system:authenticated</code> role to <code>get</code>
	<code>imagestreams/layers</code> in the namespace that contains the
	<code>ImageStream</code>.
  </li>
</ul>

<h4 id="buildroot"><a href="#buildroot">Build Root Image</a></h4>
<p>
The build root image must contain all dependencies for building executables and
non-image artifacts. Additionally, <code>ci-operator</code> requires this image
to include a <code>git</code> executable in <code>$PATH</code>. Most repositories
will want to use an image already present in the cluster, using the <code>image_stream_tag</code>
stanza like described in <a href="#inputs">Configuring Inputs</a>.
</p>

<p>
Alternatively, a project can be configured to build a build root image using
a <code>Dockerfile</code> in the repository:
</p>

{{ yamlSyntax (index . "ciOperatorProjectImageBuildroot") }}

<p>
In this case, the <code>Dockerfile</code> will <b>always</b> be obtained from
current <code>HEAD</code> of the given branch, even if ci-operator runs in the
context of a PR that updates that <code>Dockerfile</code>.
</p>

<p>
A third option is to configure the <code>build_root</code> in your repo
alongside the code instead of inside the <code>ci-operator</code> config. The main advantage
of this is that it allows to atomically change both code and the <code>build_root</code>.
To do so, set the <code>from_repository: true</code> in your <code>ci-operator</code> config:
</p>

{{ yamlSyntax (index . "ciOperatorBuildRootFromRepo") }}

<p>
Afterwards, create a file named <code>.ci-operator.yaml</code> in your repository
that contains the imagestream you want to use for your <code>build_root</code>:
</p>

{{ yamlSyntax (index . "ciOperatorBuildRootInRepo" ) }}

<h4 id="artifacts"><a href="#artifacts">Building Artifacts</a></h4>

<p>
Starting <code>FROM</code> the image described as the <code>build_root</code>,
<code>ci-operator</code> will clone the repository under test and compile
artifacts, committing them as image layers that may be referenced in derivative
builds. The commands which are run to compile artifacts are configured with
<code>binary_build_commands</code> and are run in the root of the cloned
repository. A a separate set of commands, <code>test_binary_build_commands</code>,
can be configured for building artifacts to support test execution. The following
<code>ImageStreamTags</code> are created in the test's <code>Namespace</code>:
</p>
<ul>
  <li><code>pipeline:root</code>: imports or builds the <code>build_root</code> image</li>
  <li><code>pipeline:src</code>: clones the code under test <code>FROM pipeline:root</code></li>
  <li><code>pipeline:bin</code>: runs commands in the cloned repository to build artifacts <code>FROM pipeline:src</code></li>
  <li><code>pipeline:test-bin</code>: runs a separate set of commands in the cloned repository to build test artifacts <code>FROM pipeline:src</code></li>
</ul>

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "ciOperatorPipelineConfig") }}

<p>
The content created with these OpenShift <code>Builds</code> is addressable
in the <code>ci-operator</code> configuration simply with the tag. For instance,
the <code>pipeline:bin</code> image can be referenced as <code>bin</code> when
the content in that image is needed in derivative <code>Builds</code>.
</p>

<h4 id="images"><a href="#images">Building Container Images</a></h4>

<p>
Once container images exist with output artifacts for a repository, additional
output container images may be built that make use of those artifacts. Commonly,
the desired output container image will contain only the executables for a
component and not any of the build-time dependencies. Furthermore, most teams
will need to publish their output container images through the automated release
pipeline, which requires that the images are built in Red Hat's production image
build system, OSBS. In order to create an output container image without build-time
dependencies in a manner which is compatible with OSBS, the simplest approach is a
multi-stage <code>Dockerfile</code> build.
</p>

<p>
The standard pattern for a multi-stage <code>Dockerfile</code> is to run a compilation
in a builder image and copy the resulting artifacts into a separate output image base.
For instance, a repository could add this <code>Dockerfile</code> to their source:
<p>

<code>Dockerfile</code>:
{{ dockerfileSyntax (index . "multistageDockerfile") }}

<p>
While such a <code>Dockerfile</code> could simply be built by <code>ci-operator</code>,
a number of optimizations can be configured to speed up the process -- especially if
multiple output images share artifacts. An output container image build is configured
for <code>ci-operator</code> with the <code>images</code> stanza in the configuration.
Any entry in the <code>images</code> stanza can be configured with native OpenShift
<code>Builds</code> options; the full list can be viewed <a href="https://godoc.org/github.com/openshift/ci-tools/pkg/api#ProjectDirectoryImageBuildInputs">here.</a>
In the following example, an output container image is built where the <code>builder</code>
image is replaced with the image layers containing built artifacts in <code>pipeline:bin</code>
and the output image base is replaced with the appropriate entry from <code>base_images</code>.
<p>

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "ciOperatorImageConfig") }}

<p>
By making use of the previously compiled artifacts in the intermediate <code>pipeline:bin</code>
image, this repository is able to cache the Go build. If multiple output images exist that
rely on a previously built artifact, this caching effect can reduce build times dramatically.
</p>

<h4 id="promotion"><a href="#promotion">Publishing Container Images</a></h4>

<p>
Once <code>ci-operator</code> has built output container images for a repository,
it can publish them to an integration <code>ImageStream</code> so that other
repositories can consume them. For instance, every image that makes up the
OpenShift release payload is incrementally updated in an integration <code>ImageStream</code>.
This allows release payloads to be created incorporating the latest tested version
of every component. In order to publish images to an integration <code>ImageStream</code>,
add the <code>promotion</code> stanza to <code>ci-operator</code> configuration.
</p>

<p>
The <code>promotion</code> stanza declares which container images are published
and defines the integration <code>ImageStream</code> where they will be available.
By default, all container images declared in the <code>images</code> block of a
<code>ci-operator</code> configuration are published when a <code>promotion</code>
stanza is present to define the integration <code>ImageStream</code>. Promotion can
also be configured to include other images by setting <code>additional_images</code>
and to exclude images using <code>excluded_images</code>. For instance, this example
publishes the following images:
</p>
<ul>
  <li>the <code>pipeline:src</code> tag, published as <code>ocp/4.5:repo-scripts</code> containing the latest version of the repository</li>
  <li>the <code>stable:component</code> tag, published as <code>ocp/4.5:mycomponent</code> containing the output component itself</li>
</ul>

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "ciOperatorPromotionConfig") }}

<h4 id="release"><a href="#release">Describing Inclusion in an OpenShift Release</a></h4>

<p>
<code>ci-operator</code> gives first-class support to repositories which need to
run end-to-end tests in the context of an OpenShift cluster. <code>ci-operator</code>
supports two mechanisms for testing in the context of an OpenShift release. First, it
is possible to use the container images built as part of the test to build an ephemeral
release payload, allowing repositories that build parts of OpenShift to test versions
that include components under test. Second, it is possible to reference existing release
payloads that have already been created, in order to validate those releases or for
repositories to test their functionality against published versions of OpenShift.
</p>

<h5 id="ephemeral-release"><a href="#ephemeral-release">Testing With an Ephemeral OpenShift Release</a></h5>

<p>
The <code>tag_specification</code> configuration option enables a repository to declare
which version of OpenShift it is a part of by specifying the images that will be used to
create an ephemeral OpenShift release payload for testing. Most commonly, the same integration
<code>ImageStream</code> is specified for <code>tag_specification</code> as is for
<code>promotion</code>.
</p>

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "ciOperatorTagSpecificationConfig") }}

<p>
When <code>ci-operator</code> begins to test a repository, it will snapshot the current
state of the integration <code>ImageStream</code>, importing all tags into the test
<code>Namespace</code>. Any output image tags built from the repository under test
overwrite those that are imported from the integration <code>ImageStream</code>. An
ephemeral release payload is built from the resulting <code>ImageStream</code>,
containing the latest published versions of all components and the proposed version
of the component under test.
</p>

<h5 id="existing-release"><a href="#existing-release">Testing With an Existing OpenShift Release</a></h5>

<p>
The <code>releases</code> configuration option allows specification of an existing
version of OpenShift that a component will be tested on. Three types of releases
may be referenced: candidate release payloads from a release controller, pre-release
payloads that have yet to be published to Cincinnati, and official releases as
customers would see them.
</p>

<p>
Releases may be named, with two names holding special meaning. In ordinary end-to-end
tests, the <code>latest</code> release describes the version that will be installed
before tests are run. For upgrade end-to-end tests, the <code>initial</code> release
describes the version of OpenShift which is initially installed, after which an upgrade
is executed to the <code>latest</code> release, after which tests are run. The full pull
specification for a release payload is provided to test steps with the <code>${RELEASE_IMAGE_&lt;name&gt;}</code>
environment variable. The following example exposes a the following release payload to tests:
</p>

<ul>
  <li>the <code>release:initial</code> tag, holding a release candidate for OKD 4.3, exposed as <code>${RELEASE_IMAGE_INITIAL}</code></li>
  <li>the <code>release:latest</code> tag, holding an officially-released payload for OCP 4.4, exposed as <code>${RELEASE_IMAGE_LATEST}</code></li>
  <li>the <code>release:previous</code> tag, holding a previous release candidate for OCP 4.5, exposed as <code>${RELEASE_IMAGE_PREVIOUS}</code></li>
  <li>the <code>release:custom</code> tag, holding the latest pre-release payload for OCP 4.4, exposed as <code>${RELEASE_IMAGE_CUSTOM}</code></li>
</ul>

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "ciOperatorReleaseConfig") }}

<h4 id="tests"><a href="#tests">Declaring Tests</a></h4>

<p>
Tests as executed by <code>ci-operator</code> run a set of commands inside of a container;
this is implemented by scheduling a <code>Pod</code> under the hood. <code>ci-operator</code>
can be configured to run one of two types of tests: simple, single-stage container
tests and longer, multi-stage container tests. A single-stage test will schedule one
<code>Pod</code> and execute the commands specified. Note that the default working
directory for any container image in the <code>pipeline</code> <code>ImageStream</code>
is the root of the cloned repository under test. The following example uses this
approach to run static verification of source code:
</p>

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "ciOperatorContainerTestConfig") }}

</p>
The second approach to describing tests allows for multiple containers to be chained
together and describes a more complicated execution flow between them. This multi-stage
test approach is best suited for end-to-end test suites that require full OpenShift
test clusters to be brought up and torn down. Learn more about this type of test
at the <a href="./">getting started overview</a>.
<p>

<h4 id="test-types"><a href="#test-types">Types of Tests</a></h4>
<h5 id="presubmit"><a href="#presubmit">Pre-submit Tests</a></h5>
<p>
By default, any entry declared in the <code>tests</code> stanza of a <code>ci-operator</code>
configuration file will be a <i>pre-submit</i> test: these tests run before code is
submitted (merged) into the target repository. Pre-submit tests are useful
to give feedback to a developer on the content of their pull request and to gate
merges to the central repository. These tests will fire when a pull request is opened,
when the contents of a pull request are changed, or on demand when a user requests
them.
</p>

<h5 id="postsubmit"><a href="#postsubmit">Post-submit Tests</a></h5>
<p>
When a repository configures <code>ci-operator</code> to build images and publish
them (by declaring container image builds with <code>images</code> and the destination
for them to be published with <code>promotion</code>), a <i>post-submit</i> test will
exist. A post-submit test executes after code is merged to the target repository;
this sort of test type is a good fit for publication of new artifacts after changes to
source code.
</p>
<p>
Adding a custom postsubmit to a repository via the ci-operator config is
supported. To do so, add the <code>postsubmit</code> field to a ci-operator
test config and set it to <code>true</code>. The following example configures
a ci-operator test to run as a postsubmit:
</p>
<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "ciOperatorPostsubmitTestConfig") }}

<p>
One important thing to note is that, unlike presubmit jobs, the postsubmit
tests are configured to not be rehearsable. This means that when the test is
being added or modified by a PR in the <code>openshift/release</code> repo,
the job will not be automatically run against the change in the PR. This is
done to prevent accidental publication of artifacts by rehearsals.
</p>

<h5 id="periodic"><a href="#periodic">Periodic Tests</a></h5>
<p>
A repository may be interested in validating the health of the latest source code,
but not at every moment that the code changes. In these cases, a <i>periodic</i>
test may be configured to run on the latest source code on a schedule. The following
example sets the <code>cron</code> field on an entry in the <code>tests</code> list
to configure that test to run on a schedule, instead of as a pre-submit:
</p>

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "ciOperatorPeriodicTestConfig") }}

<p>
Note that the build farms used to execute jobs run on UTC time, so time-of-day based
<code>cron</code> schedules must be set with that in mind.
</p>

<h3 id="image-references"><a href="#image-references">Referencing Images</a></h3>
<p>
As <code>ci-operator</code> is OpenShift-native, all images used in a test workflow
are stored as <code>ImageStreamTags</code>. The following <code>ImageStreams</code>
will exist in the <code>Namespace</code> executing a test workflow:
</p>

<table>
  <tr>
    <th style="white-space: nowrap"><code>ImageStream</code></th>
    <th>Description</th>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>pipeline</code></td>
    <td>Input images described with <code>base_images</code> and <code>build_root</code> as well as images holding built artifacts (such as <code>src</code> or <code>bin</code>) and output images as defined in <code>images</code>.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>release</code></td>
    <td>Tags of this <code>ImageStreams</code> hold OpenShift release payload images for installing and upgrading ephemeral OpenShift clusters for testing; a tag will be present for every named release configured in <code>releases</code>. If a <code>tag_specification</code> is provided, two tags will be present, <code>:initial</code> and <code>:latest</code>.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>stable-&lt;name&gt;</code></td>
    <td>Images composing the <code>release:name</code> release payload, present when <code>&lt;name&gt;<code> is configured in <code>releases<code>.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>stable</code></td>
    <td>Same as above, but for the <code>release:latest</code> release payload. Appropriate tags are overridden using the container images built during the test.</td>
  </tr>
</table>

<h4 id="config-references"><a href="#config-references">Referring to Images in <code>ci-operator</code> Configuration</a></h4>
<p>
Inside of any <code>ci-operator</code> configuration file all images must be
referenced as an <code>ImageStreamTag</code> (<code>stream:tag</code>), but
may be referenced simply with the tag name. When an image is referenced with
a tag name, the tag will be resolved on the <code>pipeline</code> <code>ImageStream</code>,
if possible, falling back to the <code>stable</code> <code>ImageStream</code>
if not. For example, an image referenced as <code>installer</code> will use
<code>pipeline:installer</code> if that tag is present, falling back to
<code>stable:installer</code> if not. The following configuration fields
use this defaulting mechanism:
</p>

<ul>
  <li><code>images[*].from</code>: configuring the base <code>FROM</code> which an image builds</li>
  <li><code>promotion.additional_images</code>: configuring which images are published</li>
  <li><code>promotion.excluded_images</code>: configuring which images are not published</li>
  <li><code>tests[*].container.from</code>: configuring the container image in which a single-stage test runs</li>
  <li><code>tests[*].steps.{pre,test,post}[*].from</code>: configuring the container image which some part of a multi-stage test runs</li>
</ul>

<h4 id="literal-references"><a href="#literal-references">Referring to Images in Tests</a></h4>
<p>
<code>ci-operator</code> will run every part of a test as soon as possible, including
imports of external releases, builds of container images and test workflow steps. If a
workflow step runs in a container image that's imported or built in an earlier part of
a test, <code>ci-operator</code> will wait to schedule that test step until the image is
present. In some cases, however, it is necessary for a test command to refer to an image
that was built during the test workflow but not run inside of that container image itself.
In this case, the default scheduling algorithm needs to know that the step requires a
valid reference to exist before running.
</p>

<p>
Test workloads can declare that they require fully resolved pull specification
as a digest for any image from the <code>pipeline</code>,
<code>stable-&lt;name&gt;</code> or <code>release</code>
<code>ImageStreams</code>.  Multi-stage tests may opt into having these
environment variables present by declaring <code>dependencies</code> in the
<code>ci-operator</code> configuration for the test.  For instance, the example
test below will be able to access the following environment variables:
</p>

<ul>
  <li><code>${MACHINE_CONFIG_OPERATOR}</code>: exposing the pull specification of the <code>stable:machine-config-operator</code> <code>ImageStreamTag</code></li>
  <li><code>${BINARIES}</code>: exposing the pull specification of the <code>pipeline:bin</code> <code>ImageStreamTag</code></li>
  <li><code>${LATEST_RELEASE}</code>: exposing the pull specification of the <code>release:latest</code> payload <code>ImageStreamTag</code></li>
</ul>

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "ciOperatorContainerTestWithDependenciesConfig") }}

<h5 id="dependency-overrides"><a href="#dependency-overrides">Dependency Overrides</a></h5>
<p>
Dependencies can be defined at the workflows and test level in the registry,
overwriting the source for the pull specification that will populate an environment
variable in a step. These definitions will be propagated from the top-level definition
to individual steps. The following example overrides the content of the <code>${DEP}</code>
environment variable in the <code>test</code> step to point to the pull specification of
<code>pipeline:src</code> instead of the original <code>pipeline:bin</code>.
</p>

{{ yamlSyntax (index . "depsPropagation") }}
`

const ciOperatorInputConfig = `base_images:
  base: # provides the OpenShift universal base image for other builds to use when they reference "base"
    name: "4.5"
    namespace: "ocp"
    tag: "base"
  cli: # provides an image with the OpenShift CLI for other builds to use when they reference "cli"
    name: "4.5"
    namespace: "ocp"
    tag: "cli"
build_root: # declares that the release:golang-1.13 image has the build-time dependencies
  image_stream_tag:
    name: "release"
    namespace: "openshift"
    tag: "golang-1.13"
`

const ciOperatorPipelineConfig = `binary_build_commands: "go build ./cmd/..."         # these commands are run to build "pipeline:bin"
test_binary_build_commands: "go test -c -o mytests" # these commands are run to build "pipeline:test-bin"`

const multistageDockerfile = `# this image is replaced by the build system to provide repository source code
FROM registry.svc.ci.openshift.org/ocp/builder:golang-1.13 AS builder
# the repository's source code will be available under $GOPATH of /go
WORKDIR /go/src/github.com/myorg/myrepo
# this COPY bring the repository's source code from the build context into an image layer
COPY . .
# this matches the binary_build_commands but runs against the build cache
RUN go build ./cmd/...

# this is the production output image base and matches the "base" build_root
FROM registry.svc.ci.openshift.org/openshift/origin-v4.5:base
# inject the built artifact into the output
COPY --from=builder /go/src/github.com/myorg/myrepo/mybinary /usr/bin/
`

const ciOperatorImageConfig = `images:
- dockerfile_path: "Dockerfile" # this is a relative path from the root of the repository to the multi-stage Dockerfile
  from: "base" # a reference to the named base_image, used to replace the output FROM in the Dockerfile
  inputs:
    bin: # declares that the "bin" tag is used as the builder image when overwriting that FROM instruction
      as:
      - "registry.svc.ci.openshift.org/ocp/builder:golang-1.13"
  to: "mycomponent" # names the output container image "mycomponent"
- dockerfile_path: "tests/Dockerfile"
  from: "test-bin" # base the build off of the built test binaries
  inputs:
    cli:
      paths:
      - destination_dir: "."
        source_path: "/go/bin/oc" # inject the OpenShift clients into the build context directory
  to: "mytests" # names the output container image "mytests"
`

const ciOperatorPromotionConfig = `promotion:
  additional_images:
    repo-scripts: "src"    # promotes "src" as "repo-scripts"
  excluded_images:
  - "mytests" # does not promote the test image
  namespace: "ocp"
  name: "4.5"
`

const ciOperatorTagSpecificationConfig = `tag_specification:
  cluster: "https://api.ci.openshift.org"
  namespace: "ocp"
  name: "4.5"
`

const ciOperatorReleaseConfig = `releases:
  initial:           # describes the 'initial' release
    candidate:       # references a candidate release payload
      product: okd
      version: "4.3"
  latest:
    release:          # references a version released to customers
      channel: stable # configures the release channel to search
      version: "4.4"
  previous:
    candidate:
      product: ocp
      architecture: amd64
      stream: nightly     # specifies a candidate release stream
      version: "4.5"
      relative: 1         # resolves to the Nth latest payload in this stream
  custom:
    prerelease:       # references a version that may be published to customers, but is not yet
      product: ocp
      version_bounds: # bounds the version for the release chosen
        lower: "4.4.0"
        upper: "4.5.0-0"
`

const ciOperatorContainerTestConfig = `tests:
- as: "vet"                 # names this test "vet"
  commands: "go vet ./..."  # declares which commands to run
  container:
    from: "src"             # runs the commands in "pipeline:src"
`
const ciOperatorContainerTestWithDependenciesConfig = `tests:
- as: "vet"
  steps:
    test:
    - as: "vet"
      from: "src"
      commands: "test-script.sh ${BINARIES} ${MACHINE_CONFIG_OPERATOR} ${LATEST_RELEASE}"
      resources:
        requests:
          cpu: 100m
          memory: 100Mi
      dependencies:
      - name: "machine-config-operator"
        env: "MACHINE_CONFIG_OPERATOR"
      - name: "bin"
        env: "BINARIES"
      - name: "release:latest"
        env: "LATEST_RELEASE"
`
const depsPropagation = `tests:
- as: "example"
  steps:
    dependencies:
      DEP: "pipeline:src" # the override for the definition of ${DEP}
    test:
    - as: "test"
      commands: "make test"
      from: "src"
      resources:
        requests:
          cpu: 100m
          memory: 100Mi
      dependencies:
      - name: "pipeline:bin" # the original definition of ${DEP}
        env: "DEP"
`

const ciOperatorPostsubmitTestConfig = `tests:
- as: "upload-results"               # names this test "upload-results"
  commands: "make upload-results"    # declares which commands to run
  container:
    from: "bin"                      # runs the commands in "pipeline:bin"
  postsubmit: true                   # schedule the job to be run as a postsubmit
`

const ciOperatorPeriodicTestConfig = `tests:
- as: "sanity"               # names this test "sanity"
  commands: "go test ./..."  # declares which commands to run
  container:
    from: "src"              # runs the commands in "pipeline:src"
  cron: "0 */6 * * *"        # schedule a run on the hour, every six hours
`

const ciOperatorProjectImageBuildroot = `build_root:
  project_image:
    dockerfile_path: images/build-root/Dockerfile # Dockerfile for building the build root image
`

const ciOperatorBuildRootFromRepo = `build_root:
  from_repository: true
`

const ciOperatorBuildRootInRepo = `build_root_image:
  namespace: openshift
  name: release
  tag: golang-1.15
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
about the three components of the test registry and how to use those components
to create a test:
<ul>
  <li>
    <a href="#step">Step</a>: A step is the lowest level
    component in the test step registry. It describes an individual test
    step.
  </li>
  <li>
	<a href="#chain">Chain</a>: A chain is a registry component that
	specifies multiple steps to be run. Any item of the chain can be either a
	step or another chain.
  </li>
  <li>
    <a href="#workflow">Workflow</a>: A workflow is the highest level
    component of the step registry. It contains three chains:
    <code>pre</code>, <code>test</code>, <code>post</code>.
  </li>
</ul>
</p>

<h3 id="step"><a href="#step">Step</a></h3>
<p>
A step is the lowest level component in the test registry. A
step defines a base container image, the filename of the
shell script to run inside the container, the resource requests and limits
for the container, and documentation for the step. Example of a
step:
</p>

{{ yamlSyntax (index . "refExample") }}

<p>
A step may be referred to in chains, workflows, and <code>ci-operator</code> configs.
</p>

<h4 id="step-image"><a href="#step-image">Configuring the Container Image For a Step</a></h4>

<p>
The container image used to run a test step can be configured in one of two
ways: by referencing an image tag otherwise present in the configuration or
by explicitly referencing an image tag present on the build farm.
</p>

<h5 id="step-from"><a href="#step-from">Referencing Another Configured Image</a></h5>

<p>
A step may execute in a container image already present in the <code>ci-operator</code>
configuration file by identifying the tag with the <code>from</code> configuration
field. Steps should use this mechanism to determine the container image they run in
when that image will vary with the code under test. For example, the container image
could have contents from the code under test (like <code>src</code>); similarly, the
image may need to contain a component matching the version of OpenShift used in the
test (like <code>installer</code>). When using this configuration option, ensure that
the tag is already present in one of the following places:
</p>

<ul>
  <li>
    <a href="https://github.com/openshift/ci-tools/blob/master/ARCHITECTURE.md#build-graph-traversal">
      a pipeline image
    </a>
  </li>
  <li>
    <a href="https://github.com/openshift/ci-tools/blob/master/CONFIGURATION.md#base_images">
      an external image
    </a>
  </li>
  <li>
    <a href="https://github.com/openshift/ci-tools/blob/master/CONFIGURATION.md#images">
      an image built by <code>ci-operator</code>
    </a>
  </li>
  <li>
    <a href="https://github.com/openshift/ci-tools/blob/master/CONFIGURATION.md#tag_specification">
      an image imported from a release <code>ImageStream</code>
    </a>
  </li>
</ul>

<p>
Note that static validation for this field is limited because the set of images
originating from the release <code>ImageStream</code> is only known at runtime.
</p>

<h5 id="step-from-image"><a href="#step-from-image">Referencing a Literal Image</a></h5>

<p>
A step may also be configured to use an available <code>ImageStreamTag</code> on
the build farm where the test is executed by specifying the details for the tag with
the <code>from_image</code> configuration field. A step should use this option when
the version of the container image to be used does not vary with the code under test
or the version of OpenShift being tested. Using the <code>from_image</code> field is
synonymous with importing the image as a <code>base_image</code> and referencing the
tag with the <code>from</code> field, but allows the step definition to be entirely
self-contained. The following example of a step configuration uses this option:
</p>

{{ yamlSyntax (index . "refFromImageExample") }}

<h4 id="step-commands"><a href="#step-commands"><code>commands</code></a></h4>

<p>
The commands file must contain shell script in a shell language supported by
the <code>shellcheck</code> program used to validate the commands. However,
regardless of the shell language used for the commands, the web UI will
syntax highlight all commands as bash.
</p>

<p>
Note: the shell script file must follow the <a href="#layout">naming convention</a> described later
in this help page.
</p>

<h4 id="execution"><a href="#execution">Step Execution Environment</a></h4>
<p>
While a step simply defines a set of commands to run in a container image,
by virtue of executing within a <code>ci-operator</code> workflow, the commands
have a number of special considerations for their execution environment.
The commands can expect a set of environment variables to exist that inform
them of the context in which they run. Commands in steps can communicate to
other steps via a shared directory in their filesystem.
</p>

<h5 id="env"><a href="#env">Available Environment Variables</a></h5>
<p>
The following environment variables will be available to commands in a step:
</p>

<table class="table">
  <tr>
    <th style="white-space: nowrap">Variable</th>
    <th>Definition</th>
    <th>When is it Present?</th>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>${OPENSHIFT_CI}</code></td>
    <td>Set to <code>"true"</code>, should be used to detect that a script is running in a <code>ci-operator</code> environment.</td>
    <td>Always.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>${SHARED_DIR}</code></td>
    <td>Directory on the step's filesystem where files shared between steps can be read and written.</td>
    <td>Always.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>${ARTIFACT_DIR}</code></td>
    <td>Directory on the step's filesystem where files should be placed to persist them in the job's artifacts.</td>
    <td>Always.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>${CLUSTER_PROFILE_DIR}</code></td>
    <td>Directory on the step's filesystem where credentials and configuration from the cluster profile are stored.</td>
    <td>When the test as defined in a <code>ci-operator</code> configuration file sets a <code>cluster_profile</code>.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>${KUBECONFIG}</code></td>
    <td>Path to <code>system:admin</code> credentials for the ephemeral OpenShift cluster under test.</td>
    <td>After an ephemeral cluster has been installed.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>${RELEASE_IMAGE_INITIAL}</code></td>
    <td>Image pull specification for the initial release payload snapshot when the test began to run.</td>
    <td>Always.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>${RELEASE_IMAGE_LATEST}</code></td>
    <td>Image pull specification for the ephemeral release payload used to install the ephemeral OpenShift cluster.</td>
    <td>Always.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>${LEASED_RESOURCE}</code></td>
    <td>The name of the resource leased to grant access to cloud quota. See <a href="./help/leases">the documentation</a>.</td>
    <td>When the test requires a lease.</td>
  </tr>
</table>

<p>
In addition to these variables, commands will also have a number of other
environment variables available to them from
<a href="https://github.com/kubernetes/test-infra/blob/master/prow/jobs.md#job-environment-variables">Prow</a>
as well as from
<a href="https://github.com/openshift/ci-tools/blob/master/TEMPLATES.md#parameters-available-to-templates"><code>ci-operator</code></a>.
If a job is using these variables, however, it may be an indication that
some level of encapsulation has been broken and that a more straightforward
approach exists to achieve the same outcome.
</p>

<p>
<a href="#parameters">Parameters</a> declared by steps and set by tests will
also be available as environment variables.
</p>

<h5 id="data"><a href="#data">Sharing Data Between Steps</a></h5>
<p>
Steps can communicate between each other by using a shared directory on their
filesystems. This directory is available for test processes via
<code>${SHARED_DIR}</code>. When the process finishes executing, the contents
of that directory will be copied and will be available to following
steps. New data will overwrite previous data, absent files will be removed. The
underlying mechanism for this uses Kubernetes concepts; therefore, the total
amount of data that can be shared is capped at 1MB and only a flat file
structure is permissible: no sub-directories are supported.  Steps are more
commonly expected to communicate between each other by using state in the
OpenShift cluster under test. For instance, if a step installs some components
or changes configuration, a later step could check for that as a pre-condition
by using <code>oc</code> or the API to view the cluster's configuration.
</p>

<h5 id="kubeconfig"><a href="#kubeconfig">A Note on <code>$KUBECONFIG</code></a></h5>
<p>
In the default execution environment, commands run in steps will be given the
<code>$KUBECONFIG</code> environment variable to allow them to interact with
the ephemeral cluster that was created for testing. It is required that any
steps which execute a cluster installation publish the resulting configuration
file to <code>$SHARED_DIR/kubeconfig</code> to allow the <code>ci-operator</code>
to correctly propagate this configuration to subsequent steps.
</p>

<h5 id="artifacts"><a href="#artifacts">Exposing Artifacts</a></h5>
<p>
Steps can commit artifacts to the output of a job by placing files at the
<code>${ARTIFACT_DIR}</code>. These artifacts will be available for a job
under <code>artifacts/job-name/step-name/</code>. The logs of each container
in a step will also be present at that location.
</p>

<h5 id="credentials"><a href="#credentials">Injecting Custom Credentials</a></h5>
<p>
Steps can inject custom credentials by adding configuration that identifies
which secrets hold the credentials and where the data should be mounted in
the step. For instance, to mount the <code>my-data</code> secret into the
step's filesystem at <code>/var/run/my-data</code>, a step could be configured
in a literal <code>ci-operator</code> configuration, or in the step's configuration
in the registry in the following manner:
</p>

Registry step configuration:
{{ yamlSyntax (index . "credentialExample") }}

<p>
Note that access to read these secrets from the namespace configured must be
granted separately from the configuration being added to a step. By default,
only secrets in the <code>test-credentials</code> namespace will be available
for mounting into test steps.
</p>

<h3 id="chain"><a href="#chain">Chain</a></h3>
<p>
A chain is a registry component that specifies multiple registry components to be run.
Components are run in the order that they are written. Components specified by a chain
can be either steps and other chains. Example of a chain:
</p>

{{ yamlSyntax (index . "chainExample") }}

<h3 id="workflow"><a href="#workflow">Workflow</a></h3>
<p>
A workflow is the highest level component of the step registry. It is almost
identical to the syntax of the <code>ci-operator</code> configuration for multistage tests and
defines an entire test from start to finish. It has four basic components: a
<code>cluster_profile</code> string (eg: <code>aws</code>, <code>azure4</code>,
<code>gcp</code>), and three chains: <code>pre</code>, <code>test</code>, and
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

<h3 id="config"><a href="#config"><code>ci-operator</code> Test Configuration</a></h3>
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

Example of a <code>ci-operator</code> configuration that overrides a workflow field.

{{ yamlSyntax (index . "configExample2") }}

The configuration can also override a workflow field with a <a href="#step">full literal step</a>
(not only a reference to a shared step):

{{ yamlSyntax (index . "configExample3") }}

<h2 id="allow-skip-on-success"><a href="#allow-skip-on-success">Options to Change Control Flow</a></h2>
<p>
<code>ci-operator</code> can be configured to skip some or all <code>post</code> steps
when all <code>test</code> steps pass.
Skipping a <code>post</code> step when all tests have passed may be useful to skip
gathering artifacts and save some time at the end of the multistage test.
In order to allow steps to be skipped in a test, the <code>allow_skip_on_success</code> field must
be set in the <code>steps</code> configuration. Individual <code>post</code> steps opt
into being skipped by setting the <code>optional_on_success</code> field. This is an example:
</p>

{{ yamlSyntax (index . "configExample4") }}

<h3 id="layout"><a href="#layout">Registry Layout and Naming Convention</a></h3>
<p>
To prevent naming collisions between all the registry components, the step
registry has a very strict naming scheme and directory layout. First, all
components have a prefix determined by the directory structure, similar to
how the <code>ci-operator</code> configs do. The prefix is the relative directory path
with all &#96;<code>/</code>&#96; characters changed to
&#96;<code>-</code>&#96;. For example, a file under the
<code>ipi/install/conf</code> directory would have as prefix of
<code>ipi-install-conf</code>. If there is a workflow, chain, or step in
that directory, the <code>as</code> field for that component would need to be
the same as the prefix. Further, only one of step, chain, or workflow
can be in a subdirectory (otherwise there would be a name conflict),
</p>

<p>
After the prefix, we apply a suffix based on what the file is defining. These
are the suffixes for the four file types that exist in the registry:
<ul style="margin-bottom:0px;">
  <li>Step: <code>-ref.yaml</code></li>
  <li>Step command script: <code>-commands.sh</code></li>
  <li>Chain: <code>-chain.yaml</code></li>
  <li>Workflow: <code>-workflow.yaml</code></li>
</ul>
</p>

<p>
Continuing the example above, a step in the
<code>ipi/install/conf</code> subdirectory would have a filename of
<code>ipi-install-conf-ref.yaml</code> and the command would be
<code>ipi-install-conf-commands.sh</code>.
</p>

<p>
Other files that are allowed in the step registry but are not used for
testing are <code>OWNERS</code> files and files that end in <code>.md</code>.
</p>

<h3 id="parameters"><a href="#parameters">Parameters</a></h3>
<p>
Steps, chains, and workflows can declare parameters in their <code>env</code>
section. These can then be set to different values to generate tests that have
small variations between them. For example:
</p>

{{ yamlSyntax (index . "paramsExample") }}

<p>
A test that utilzes this step must give a value to the
<code>OPENSHIFT_TEST_SUITE</code> parameter, which will be available as an
environment variable when it is executed. Different tests can be generated by
setting different values, which can make generating simple variations easier.
More complex combinations are encouraged to use separate steps instead.
</p>

<p>
Each item in the <code>env</code> section consists of the following fields:
</p>

<ul>
  <li><code>name</code>: environment variable name</li>
  <li>
    <code>default</code> (optional): the value assigned if no other node in the
    hierarchy provides one (described below)
  </li>
  <li>
    <code>documentation</code> (optional): a textual description of the
    parameter
  </li>
</ul>

<h4 id="hierarchical-propagation">
  <a href="#hierarchical-propagation">Hierarchical Propagation</a>
</h4>
<p>
Environment variables can be added to chains and workflows in the registry.
These variables will be propagated down the hierarchy. That is: a variable in
the env section of a chain will propagate to all of its sub-chains and
sub-steps, a variable in the env section of a workflow will propagate to all of
its stages.
</p>

{{ yamlSyntax (index . "paramsPropagation") }}

<h4 id="required-parameters">
  <a href="#required-parameters">Required Parameters</a>
</h4>
<p>
Any variable that is not assigned a default value is considered required and
must be set at a higher level of the hierarchy. When the configuration is
resolved, tests that do not satisfy this requirement will generate a validation
failure.
</p>

Step definition:
{{ yamlSyntax (index . "paramsRequired") }}

<code>ci-operator</code> configuration:
{{ yamlSyntax (index . "paramsRequiredTest") }}
`

const refExample = `ref:
  as: ipi-conf                   # name of the step
  from: base                     # image to run the commands in
  commands: ipi-conf-commands.sh # script file containing the command(s) to be run
  active_deadline_seconds: 7200  # optional duration in seconds that the step pod may be active before it is killed.
  termination_grace_period_seconds: 20 # optional duration in seconds the pod needs to terminate gracefully.
  resources:
    requests:
      cpu: 1000m
      memory: 100Mi
  documentation: |-
	The IPI configure step generates the install-config.yaml file based on the cluster profile and optional input files.`
const refFromImageExample = `ref:
  as: ipi-conf
  from_image: # literal image tag to run the commands in
    namespace: my-namespace
    name: test-image
    tag: latest
  commands: ipi-conf-commands.sh
  resources:
    requests:
      cpu: 1000m
      memory: 100Mi
  documentation: |-
	The IPI configure step generates the install-config.yaml file based on the cluster profile and optional input files.`
const credentialExample = `ref:
  as: step
  from: base
  commands: step-commands.sh
  resources:
    requests:
      cpu: 1000m
      memory: 100Mi
  credentials:
  - namespace: test-credentials # this entry injects the custom credential
    name: my-data
    mount_path: /var/run/my-data
  documentation: |-
	The step runs with custom credentials injected.`
const chainExample = `chain:
  as: ipi-deprovision                # name of this chain
  steps:
  - chain: gather                    # a chain being used as a step in another chain
  - ref: ipi-deprovision-deprovision # a step being used as a step in a chain
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
    - ref: origin-e2e-minimal`
const configExample3 = `tests:
- as: e2e-steps # test name
  steps:
    cluster_profile: aws
    workflow: origin-e2e
    test:                     # this chain will be run for "test" instead of the one in the origin-e2e workflow
    - as: e2e-test
      commands: make e2e
      from: src
      resources:
        requests:
          cpu: 100m
          memory: 200Mi`
const configExample4 = `tests:
- as: e2e-steps # test name
  steps:
    allow_skip_on_success: true      # allows steps to be skipped in this test
    test:
    - as: successful-test-step
      commands: echo Success
      from: os
      resources:
        requests:
          cpu: 100m
          memory: 200Mi
    post:
    - as: gather-must-gather         # this step will be skipped as the successful-test-step passes
      optional_on_success: true
      from: cli
      commands: gather-must-gather-commands.sh
      resources:
        requests:
          cpu: 300m
          memory: 300Mi`
const paramsExample = `ref:
  as: openshift-e2e-test
  from: tests
  commands: openshift-e2e-test-commands.sh
  resources:
    requests:
      cpu: "3"
      memory: 600Mi
    limits:
      memory: 4Gi
  env:
  - name: OPENSHIFT_TEST_SUITE
`
const paramsPropagation = `chain:
  as: some-chain
  steps:
  - ref: some-step # TEST_VARIABLE will propagate to this step
  - chain: other-chain # TEST_VARIABLE will propagate to all elements in this chain
  env:
  - name: TEST_VARIABLE
    default: test value
`
const paramsRequired = `ref:
  as: some-ref
  # …
  env:
  - name: REQUIRED_VARIABLE # automatically considered required
`
const paramsRequiredTest = `tests:
- as: valid
  steps:
    env:
      REQUIRED_VARIABLE: value
    test:
    - some-ref
- as: invalid
  steps:
    test:
    - some-ref
`

const addingComponentPage = `
<h2>Adding and Changing Step Registry Content</h2>

<h3 id="adding-content"><a href="#adding-content">Adding Content</a></h3>
<p>
Adding a new component (step, chain, or workflow) to the registry is
quite simple. Descriptions of each of the components as well as the naming
scheme and directory layout is available at the <a href="/help">
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
  <li>Run shellcheck on all shell files used by steps, failing on errors</li>
</ul>

<p>
If a new test is added that uses the new component as well,
<code>pj-rehearse</code> will test the new job with the new component.
</p>

<h3 id="changing-content"><a href="#changing-content">Changing Content</a></h3>
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
const releasePage = `
<h2>Contributing CI Configuration to the <code>openshift/release</code> Repository</h2>

<p>
The <a href="https://github.com/openshift/release/"><code>openshift/release</code></a>
repository holds CI configuration for OpenShift component repositories (for both
OKD and OCP) and for many repositories that interact with OpenShift, like
operators. The repository also contains manifests and configuration for various
services that together form the OpenShift CI system.
</p>

<h3 id="pull-requests"><a href="#pull-requests">Pull Requests</a></h3>

<p>
The <code>openshift/release</code> repository contains plenty of different
types of configuration with various impact and different owners. This section
provides the main guidelines for filing and merging pull requests to this
repository.
</p>

<h4 id="reviews"><a href="#reviews">Reviews and Approvals</a></h4>

<p>
This repository heavily uses Prow review and approval plugins together with code
ownership as encoded in <code>OWNERS</code> files. Although the repository's root
<code>OWNERS</code> is the DPTP team, specific content may be owned by different
people or teams. After a PR is filed, the bot assigns two reviewers who should
be suitable to review the PR and are expected to do so. These people are also
the ones to bug when a PR sits there without a review. Teams are expected to own
their CI config, including reviews, and therefore <code>OWNERS</code> file
presence is enforced for some sections of the repository.
</p>

<p>
During the PR lifetime, the bot maintains a comment that summarizes the pull
request's approval status, including the links to the <code>OWNERS</code> files
whose members need to approve the PR. Please pay attention to this comment when
asking for approvals.
<!--TODO: a screenshot would be nice?-->
</p>

<p>
Due to the pull request volume in the repository, DPTP team members review the
pull requests asynchronously when assigned by a bot. Please do not expect a PR
to be reviewed immediately. Unless urgent, do not ping about reviews via Slack.
If a PR sits unreviewed for more than a day, ping via GitHub first via a
mention. If a pull request spends some time in WIP or draft state, it is helpful
to mention the reviewers when the PR is ready for review.
</p>

<h4 id="checks"><a href="#checks">Checks</a></h4>

<h5 id="formatting-checks"><a href="#formatting-checks">Formatting and Generated Content</a></h5>

<p>
Parts of the repository content are partially or entirely managed by automation
, and there are checks in place, enforcing that the repo stays consistent with
respect to this automation. When these checks fail, they usually advise how to
run the tooling (using containers) to bring the repo to the desired state:
</p>

{{ plaintextSyntax (index . "determinizeCheckExample") }}

<p>
While there are individual <code>make</code> targets for different parts of the
repository, it is easiest to run the <code>make update</code> that runs <em>all</em>
these tools before a pull request submission:
</p>

{{ plaintextSyntax (index . "makeUpdateExample") }}

<h5 id="rehearsals"><a href="#rehearsals">Rehearsals</a></h5>

<p>
In addition to the "normal" checks executed against pull requests on <code>openshift/release</code>,
so-called <em>"rehearsals"</em> trigger whenever a pull request would affect one
or more CI jobs. Jobs affected by such PR are executed as if run against a
target component repository after the changes would be merged. This provides
pull request authors early feedback about how config changes impact CI setup.
</p>

<p>
All pull requests trigger a <code>ci/prow/pj-rehearse</code> job that inspects
the changes in the PR and detects affected jobs. It then submits these jobs for
execution, and they will report to the pull request results via the GitHub
contexts named with the <code>ci/rehearse/$org/$repo/$branch/$test</code>
pattern. Both the "driver" job (<code>ci/prow/pj-rehearse</code>) and the
individual rehearsals do not block merges. This allows merging changes to CI
configuration that affect jobs that fail for reasons unrelated to the change
(like flakes or infrastructure issues). Also, merging a failing job can be
useful when it gives correct signal so that such merge can be followed up in the
target repo with a pull request fixing the failing job.
</p>

<p>
The following changes are considered when triggering rehearsals:
</p>

<ol>
    <li>Changes to Prow jobs themselves (<code>ci-operator/jobs</code>)</li>
    <li>Changes to <code>ci-operator</code> configuration files (<code>ci-operator/config</code>)</li>
    <li>Changes to multi-stage steps (<code>ci-operator/step-registry</code>)</li>
    <li>Changes to templates (<code>ci-operator/templates</code>)</li>
    <li>Changes to cluster profiles (<code>cluster/test-deploy</code>)</li>
</ol>

<p>
The affected jobs are further filtered down so that jobs are only rehearsed when
it is safe. Only the jobs with <code>pj-rehearse.openshift.io/can-be-rehearsed: "true"</code>
label are rehearsed. All presubmits and periodics generated by <code>make jobs</code>
have this label by default. Generated postsubmits will not contain it because
generated postsubmits are used for promoting images. Handcrafted jobs can opt
to be rehearsable by including this label.
</p>

<p>
It is not possible to rerun individual rehearsal jobs. They do not react to any
trigger commands. Rerunning rehearsals must be done by rerunning the "driver"
job: <code>ci/prow/pj-rehearse</code>, which then triggers all rehearsals of
jobs currently affected by the PR, including the rehearsals that passed before.
</p>

<p>
Certain changes affect many jobs. For example, when a template or a step used
by many jobs is changed, in theory all these jobs could be affected by the change,
but it is unrealistic to rehearse them all. In some of these cases, rehearsals
<em>samples</em> from the set of affected jobs. Unfortunately, the sampled jobs
are sometimes not stable between retests, so it is possible that in a retest,
different jobs are selected for rehearsal than in the previous run. In this case,
results from the previous runs stay on the pull request and because rehearsals
cannot be individually triggered, they cannot be rid of. This is especially
inconvenient when these "stuck" jobs failed. Rehearsals do not block merges, so
these jobs do not prevent configuration changes from merging, but they can lead
to confusing situations.
</p>

<h5 id="sharding"><a href="#sharding"><code>ci-operator</code> Configuration Sharding</a></h5>

<p>
The configuration files under <code>ci-operator/config</code> need to be stored
in the CI cluster before jobs can use them. That is done using the
<a href="https://github.com/kubernetes/test-infra/tree/master/prow/plugins/updateconfig"><code>updateconfig</code></a>
Prow plugin, which maps file path globs to <code>ConfigMap</code>s.
</p>

<p>
Because of size constraints, files are distributed across several <code>ConfigMap</code>s
based on the name of the branch they target. Patterns for the most common names
already exist in the plugin configuration, but it may be necessary to
add entries when adding a file for a branch with an unusual name. The <a href="https://prow.ci.openshift.org/job-history/gs/origin-ci-test/pr-logs/directory/pull-ci-openshift-release-master-correctly-sharded-config"><code>correctly-sharded-config</code></a>
pre-submit job guarantees that each file is added to one (and only one) <code>ConfigMap</code>,
and will fail in case a new entry is necessary. To add one, edit the top-level
<code>config_updater</code> key in the <a href="https://github.com/openshift/release/blob/master/core-services/prow/02_config/_plugins.yaml">plugin configuration</a>.
Most likely, the new entry will be in the format:
</p>

{{ yamlSyntax (index . "updateconfigExample") }}

<p>
The surrounding entries that add files to <code>ci-operator-misc-configs</code>
can be used as reference. When adding a new glob, be careful that it does not
unintentionally match other files by being too generic.
</p>

<h3 id="component-maintainers"><a href="#component-maintainers">Component CI Configuration</a></h3>

<p>
As an owner of a repository for which you want to maintain CI configuration in
<code>openshift/release</code>, you mostly need to interact with the following
locations:
</p>
<ul>
    <li>
        <code>ci-operator/config/$org/$repo/$org-$repo-$branch.yaml</code>:
        contains your ci-operator definition, which describes how the images and
        tests in your repo work.
    </li>
    <li>
        <code>ci-operator/jobs/$org/$repo/$org-$repo-$branch-(presubmits|postsubmits|periodics).yaml</code>:
        contains Prow job definitions for each repository that are run on PRs,
        on merges, or periodically. In most cases, these files are generated
        from the <code>ci-operator</code> configuration, and you do not need to
        touch them. There are exceptions to this, which are described <a href="#component-jobs">below</a>.
    </li>
    <li>
        <code>core-services/prow/02_config/_{config,plugins}.yaml</code>: contains
        the configuration for Prow, including repository-specific configuration
        for automated merges, plugin enablement and more. This configuration is
        usually set up once when a repository is on-boarded, and then rarely
        needs to be changed.
    </li>
</ul>

<h4 id="new-repos"><a href="#new-repos">Adding CI Configuration for New Repositories</a></h4>

<p>
When adding CI configuration for new repositories, instead of manually modifying
the files in the locations described above or copy-pasting existing configuration
for other repos, you should use the <code>make new-repo</code> target. It walks
you through the necessary steps and generates the configuration for you:
</p>

{{ plaintextSyntax (index . "makeNewRepoExample") }}

<h4 id="component-configs"><a href="#component-configs"><code>ci-operator</code> Configuration</a></h4>

<p>
The <code>ci-operator</code> configuration files for a repository live in <code>ci-operator/config/$org/$repo</code>
directories. For details about the configuration itself, see this <a href="/help/ci-operator">document</a>.
There is a separate configuration file per branch, and the configuration files
follow the <code>$org-$repo-$branch.yaml</code> pattern:
</p>

{{ plaintextSyntax (index . "ciopConfigDirExample") }}

<p>
For the repositories involved in the <a href="https://docs.google.com/document/d/1USkRjWPVxsRZNLG5BRJnm5Q1LSk-NtBgrxl2spFRRU8/edit#heading=h.3myk8y4544sk">Centralized Release Branching and Config Management</a>,
(this includes all OCP components and some others, see the linked document
for details) the configuration for release branches for the <em>future</em>
releases are managed by automation and should not be changed or added by humans.
</p>

<h5 id="feature-branches"><a href="#feature-branches">Feature Branches</a></h5>

<p>
Any branch whose name has a prefix matching to any branch with a <code>ci-operator</code>
configuration file is considered a <em>"feature branch"</em>. Pull requests to
feature branches trigger the same CI presubmit jobs (but not postsubmits) like
configured for the base branch, without any additional configuration. This also
means that such <em>"feature branches"</em> cannot have a separate, different
<code>ci-operator</code> configuration. For example, if a repo has an <code>org-repo-release-2.0.yaml</code>
config (specifying CI config for the <code>release-2.0</code> branch of that
repository), the same CI presubmits will trigger on pull requests to a <code>release-2.0.1</code>
branch, and the repo cannot have an <code>org-repo-release-2.0.1.yaml</code>
configuration file.
</p>

<h5 id="variants"><a href="#variants">Variants</a></h5>

<p>
It is possible to have multiple <code>ci-operator</code> configuration files for
a single branch. This is useful when a component needs to be built and tested in
multiple different ways from a single branch. In that case, the additional
configuration files must follow the <code>org-repo-branch__VARIANT.yaml</code>
pattern (note the double underscore separating the branch from the variant).
</p>

<h4 id="component-jobs"><a href="#component-jobs">Prowjob Configuration</a></h4>

<p>
Most jobs are generated from the <code>ci-operator</code> configuration, so the
need to interact with actual Prowjob configuration should be quite rare.
Modifying the Prowjob configuration is discouraged unless necessary, and can
result in increased fragility and maintenance costs.
</p>

<h5 id="manual-job-changes"><a href="#manual-job-changes">Tolerated Changes to Generated Jobs</a></h5>

<p>
Generated jobs are enforced to stay in the generated form, so when you attempt
to change them, a check will fail on the pull requests, requiring the jobs to be
regenerated and changed back. However, the generator tolerates these
modifications to allow some commonly needed customizations:
</p>

<table class="table">
  <tr>
    <th style="white-space: nowrap">Field</th>
    <th>Description</th>
    <th>Presubmit</th>
    <th>Postsubmit</th>
    <th>Periodic</th>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>.always_run</code></td>
    <td>Set to <code>false</code> to disable automated triggers of the job on pull requests.</td>
    <td>✅</td>
    <td></td>
    <td></td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>.run_if_changed</code></td>
    <td>Set a regex to make the job trigger only when a pull request changes a certain path in the repository./td>
    <td>✅</td>
    <td></td>
    <td></td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>.optional</code></td>
    <td>Set to <code>true</code> to make the job not block merges.</td>
    <td>✅</td>
    <td></td>
    <td></td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>.skip_report</code></td>
    <td>Set to <code>true</code> to make the job not report its result to the pull request.</td>
    <td>✅</td>
    <td></td>
    <td></td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>.max_concurrency</code></td>
    <td>Set to limit how many instances of the job can run simultaneously.</td>
    <td>✅</td>
    <td>✅</td>
    <td>✅</td>
  </tr>
  <tr>
    <td style="white-space: nowrap"><code>.reporter_config</code></td>
    <td>Add this stanza to configure Slack alerts (see the <a href="https://github.com/clarketm/kubernetes_test-infra/blob/master/prow/cmd/crier/README.md#slack-reporter">upstream doc</a>).</td>
    <td></td>
    <td></td>
    <td>✅</td>
  </tr>
</table>

<h5 id="handcrafted-jobs"><a href="#handcrafted-jobs">Handcrafted Jobs</a></h5>

<p>
It is possible to add entirely handcrafted Prowjobs. The Prowjob configuration
files' content is a YAML list, so adding a job means adding an item to one
of these lists. Creating handcrafted jobs assumes knowledge of Prow, takes you
out of the well-supported path, and is therefore discouraged. You are expected to
maintain and fully own your handcrafted jobs.
</p>
`

const determinizeCheckExample = `...
ERROR: This check enforces Prow Job configuration YAML file format (ordering,
ERROR: linebreaks, indentation) to be consistent over the whole repository. We have
ERROR: automation in place that manipulates these configs and consistent formatting
ERROR: helps reviewing the changes the automation does.

ERROR: Run the following command to re-format the Prow jobs:
ERROR: $ make jobs
`

const makeUpdateExample = `$ make update
make jobs
docker pull registry.svc.ci.openshift.org/ci/ci-operator-prowgen:latest
docker run --rm <...> registry.svc.ci.openshift.org/ci/ci-operator-prowgen:latest <...>
docker pull registry.svc.ci.openshift.org/ci/sanitize-prow-jobs:latest
docker run --rm <...> registry.svc.ci.openshift.org/ci/sanitize-prow-jobs:latest <...>
make ci-operator-config
docker pull registry.svc.ci.openshift.org/ci/determinize-ci-operator:latest
docker run --rm -v <...> registry.svc.ci.openshift.org/ci/determinize-ci-operator:latest <...>
make prow-config
docker pull registry.svc.ci.openshift.org/ci/determinize-prow-config:latest
docker run --rm <...> registry.svc.ci.openshift.org/ci/determinize-prow-config:latest <...>
make registry-metadata
docker pull registry.svc.ci.openshift.org/ci/generate-registry-metadata:latest
<...>
docker run --rm -v <...> registry.svc.ci.openshift.org/ci/generate-registry-metadata:latest <...>
`

const ciopConfigDirExample = `$ ls -1 ci-operator/config/openshift/api/
openshift-api-master.yaml
openshift-api-release-3.11.yaml
openshift-api-release-4.1.yaml
openshift-api-release-4.2.yaml
openshift-api-release-4.3.yaml
openshift-api-release-4.4.yaml
openshift-api-release-4.5.yaml
openshift-api-release-4.6.yaml
openshift-api-release-4.7.yaml
OWNERS
`

const makeNewRepoExample = `make new-repo
docker pull registry.svc.ci.openshift.org/ci/repo-init:latest
<...>
docker run --rm -it <...> registry.svc.ci.openshift.org/ci/repo-init:latest --release-repo <...>
Welcome to the repository configuration initializer.
In order to generate a new set of configurations, some information will be necessary.

Let's start with general information about the repository...
Enter the organization for the repository: openshift
Enter the repository to initialize: new-repo-example
Enter the development branch for the repository: [default: master]

Now, let's determine how the repository builds output artifacts...
Does the repository build and promote container images?  [default: no] yes
Does the repository promote images as part of the OpenShift release?  [default: no] yes
Do any images build on top of the OpenShift base image?  [default: no] yes
Do any images build on top of the CentOS base image?  [default: no] no

Now, let's configure how the repository is compiled...
What version of Go does the repository build with? [default: 1.13] 1.15
[OPTIONAL] Enter the Go import path for the repository if it uses a vanity URL (e.g. "k8s.io/my-repo"):
[OPTIONAL] What commands are used to build binaries in the repository? (e.g. "go install ./cmd/...") make awesome
[OPTIONAL] What commands are used to build test binaries? (e.g. "go install -race ./cmd/..." or "go test -c ./test/...") make awesome-test
...
`

const examplesPage = `
<h2 id="examples"><a href="#examples">Available Examples</a></h2>
<ul>
  <li><a href="#aws">How do I add a job that runs the OpenShift end-to-end conformance suite on AWS?</a></li>
  <li><a href="#image">How do I use an image from another repo in my repo’s tests?</a></li>
</ul>

<h3 id="aws"><a href="#aws">How do I add a job that runs the OpenShift end-to-end conformance suite on AWS?</a></h3>
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
<code>ImageStream</code>. Promotion can be furthermore configured to include other images,
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
in one of two ways. A registry step can be written to execute the shared tests
in any <code>ci-operator</code> configuration, or a literal test step can be added just to one
repository's configuration to run the shared tests. Two examples follow which add an
execution of shared end-to-end tests using these two approaches. Both examples assume
that we have the <code>ipi</code> workflow available to use.
</p>

<h5 id="adding-step"><a href="#adding-step">Adding a Reusable Test Step</a></h4>

<p>
Full directions for adding a new reusable test step can be found in the overview for
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

const updateconfigExample = `config_updater:
  # …
  maps:
    # …
    ci-operator/config/path/to/files-*-branch-name*.yaml:
      clusters:
        app.ci:
        - ci
      name: ci-operator-misc-configs
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
  from: repo-tests
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
      from: repo-tests
      commands: |-
        #!/bin/bash
        e2e-tests # as built by go test -c
      resources:
        requests:
          cpu: 1000m
          memory: 2Gi
`

const privateRepositoriesPage = `
<h2 id="title"><a href="#title">Private Repositories</a></h2>

<p>
OpenShift CI supports setting up CI jobs for private repositories mainly to
allow temporary non-public development on the forks of the otherwise public
repositories. The CI jobs executed for these forks are not shown in the public
Deck instance, and all their artifacts are not public. Access to these jobs is
limited to engineers who need it.
</p>

<p>
Unfortunately, such access cannot be granted to developers of other private
repositories. Therefore, OpenShift CI only allows setting up <em>public</em> CI
jobs for private repositories -- the logs and artifacts executed for such
private repository will be public. <strong>Only set up such jobs when you are
absolutely sure your jobs would not leak any sensitive information</strong>.
</p>

<p>
To allow the CI jobs to access a private repo, drop a following file to the
directory in <code>openshift/release</code> holding the <code>ci-operator</code>
configuration for your repository (usually <code>ci-operator/config/$org/$repo</code>):
</p>

<code>.config.prowgen</code>
{{ yamlSyntax (index . "privateRepoProwgenConfigExample") }}

<h3><code>openshift-priv</code> organization</h3>

<p>
The <code>openshift-priv</code> organization holds private forks of selected
repositories. The purpose of these forks is to allow temporary non-public
development. Their presence, content, settings, and all CI configuration are
managed automatically.
</p>

<p>
<em>Automated tools manage all CI configuration for repositories in <code>openshift-priv</code>
organization. Humans should not change any CI configuration related to these
repositories. All manual changes to this configuration will be overwritten.</em>
</p>

<h4>Involved Repositories</h4>

<p>
The set of repositories that are managed automatically in <code>openshift-priv</code>
is dynamic and consists of the following two subsets:
</p>

<ol>
  <li>Repositories with existing CI configuration promoting images to the <code>ocp/4.X</code>
      namespace (same criteria like for enrollment into the centralized release
      branch management)</li>
  <li>Repositories explicitly listed in the
      <a href="https://github.com/openshift/release/blob/master/core-services/openshift-priv/_whitelist.yaml">allowlist</a></li>
</ol>

<h4>Automation Architecture</h4>

When a repository is identified to be included in <code>openshift-priv</code>
by having the appropriate promoting configuration or by being present in the
allowlist, the following jobs and tools maintain the existence, repository
settings, repository content, and all necessary CI configuration of the fork in
<code>openshift-priv</code>:

<ol>
  <li>The <a href="https://deck-internal-ci.apps.ci.l2s4.p1.openshiftapps.com/?job=periodic-auto-private-org-peribolos-sync">periodic-auto-private-org-peribolos-sync</a>
      job runs the <a href="https://github.com/openshift/ci-tools/tree/master/cmd/private-org-peribolos-sync">private-org-peribolos-sync</a>
      tool to maintain the GitHub settings for the fork. These settings are asynchronously
      consumed by the <a href="https://prow.ci.openshift.org/?job=periodic-org-sync">periodic-org-sync</a>
      job running the <a href="https://github.com/kubernetes/test-infra/tree/master/prow/cmd/peribolos">peribolos</a>
      tool to create the fork on GitHub and maintain its settings.</li>
  <li>The <a href="https://deck-internal-ci.apps.ci.l2s4.p1.openshiftapps.com/?job=periodic-openshift-release-private-org-sync">periodic-openshift-release-private-org-sync</a>
      job runs the <a href="https://github.com/openshift/ci-tools/tree/master/cmd/private-org-sync">private-org-sync</a>
      tool to synchronize the git content of the fork with the source repository.</li>
  <li>The <a href="https://prow.ci.openshift.org/?job=periodic-prow-auto-config-brancher">periodic-prow-auto-config-brancher</a>
      runs the <a href="https://github.com/openshift/ci-tools/tree/master/cmd/ci-operator-config-mirror">ci-operator-config-mirror</a>
      tool to create and maintain the CI configuration for the fork (<code>ci-operator</code>
      configuration files). The same job then generates the CI jobs from the <code>ci-operator</code>
      files. This has a caveat of not carrying over handcrafted (non-generated)
      jobs and also manual changes to the generated jobs.</li>
  <li>The <a href="https://prow.ci.openshift.org/?job=periodic-prow-auto-config-brancher">periodic-prow-auto-config-brancher</a>
      also runs the <a href="https://github.com/openshift/ci-tools/tree/master/cmd/private-prow-configs-mirror">private-prow-configs-mirror</a>
      tool to mirror the repository-specific Prow configuration, like merging
      criteria, plugin enablement, etc.</li>
</ol>
`

const privateRepoProwgenConfigExample = `private: true
expose: true
`

const quotasAndLeasesPage = `<h2 id="title"><a href="#title">How are Cloud Quota and Aggregate Concurrency Limits Handled?</a></h2>
<p>
A centralized locking system is provided to jobs in order to limit concurrent usage of shared resources like third-party
cloud APIs.
</p>

<p>
Jobs that interact with an Infrastructure-as-a-Service (IaaS) cloud provider use credentials shared across the broader
CI platform. Therefore, all jobs interacting with a specific IaaS will use API quota for these cloud providers from a
shared pool. In order to ensure that our job throughput for a provider remains within the aggregate limit imposed by
shared quota, jobs acquire leases for slices of the quota before they run and only relinquish them once all actions are
completed. This document describes the mechanism used to provide leases of quota slices to jobs, how jobs determine
which quota to ask for, how available leases can be configured and how current usage can be monitored.
</p>

<h3 id="boskos"><a href="#boskos">Introducing the <code>boskos</code> Leasing Server</a></h3>
<p>
<code>boskos</code> (βοσκός), translating as "shepherd" from Greek, is a resource management server that apportions
<i>leases</i> of <i>resources</i> to clients and manages the lifecycle of the resources. When considering the
actions of this server, two terms should be defined:
</p>

<table class="table">
  <tr>
    <th style="white-space: nowrap">Term</th>
    <th>Definition</th>
  </tr>
  <tr>
    <td style="white-space: nowrap">resource</td>
    <td>An item which may be leased to clients. Resources represent slices of the larger cloud quota.</td>
  </tr>
  <tr>
    <td style="white-space: nowrap">lease</td>
    <td>A binding between a resource and a client. When a lease is active, the underlying resource is not available for other clients.</td>
  </tr>
</table>

<p>
The process for granting a lease on a resource follows this workflow:
</p>

<ul>
  <li>a client (<i>lessee</i>) requests a lease on an available resource</li>
  <li>the server (<i>lessor</i>) grants the lease, if possible, or places the client in a FIFO queue to wait for the next available resource</li>
  <li>the client emits a heartbeat while the lease is under active use</li>
  <li>the client relinquishes the lease once it is no longer in use</li>
  <li>the server places the resource back into the available pool for future clients to request</li>
</ul>

<p>
If a client fails to emit a heartbeat for long enough while the client holds a lease, the server will forcibly
relinquish the lease and return the resource to the available pool for other clients. This mechanism
ensures that clients which crash or otherwise fail to remain responsive cannot exhaust resources by holding a
lease indefinitely.
</p>

<h3 id="admins"><a href="#admins">Directions for Cloud Administrators</a></h3>
<p>
An administrator of a cloud platform will interact with the leasing server in order to configure the aggregate limit on
jobs for the platform or inspect the current settings and usage. Care must be taken when configuring the leasing server
in order to ensure that jobs are well-behaved against the cloud provider APIs.
</p>

<h4 id="adding"><a href="#adding">Adding a New Type Of Resource</a></h4>
<p>
In order to add a new type of cloud quota to the system, changes to the <code>boskos</code> leasing server configuration
are required. The configuration is checked into source control <a href="https://github.com/openshift/release/blob/master/core-services/prow/02_config/_boskos.yaml">here.</a>
When adding a new type of quota, a new entry to the <code>resources</code> list is required, for example:
</p>

<code>boskos</code> configuration:
{{ yamlSyntax (index . "dynamicBoskosConfig") }}

<p>
If it is not clear exactly how many concurrent jobs can share the cloud provider at once, the convention is to set the
<code>min-count</code> and <code>max-count</code> to <code>1000</code>, to effectively leave jobs unlimited and allow
for investigation.
</p>

<p>
In addition to registering the volume of concurrent jobs that are allowed against a new cloud platform, it is required
that the leasing server is configured to reap leases which have not seen a recent heartbeat. This is done by adding the
name of the resource type to the <a href="https://github.com/openshift/release/blob/master/core-services/prow/03_deployment/boskos_reaper.yaml#L27">reaper's configuration.</a>
</p>

<h5 id="static"><a href="#static">Configuration for Heterogeneous Resources</a></h5>
<p>
The example configuration above will create <i>dynamic</i> resources and is most appropriate for operating against large
cloud APIs where clients act identically regardless of which slice of the quota they have leased. If the cloud provider
that is being configured has a static pool of resources and jobs are expected to act differently based on the specific
lease that they acquire, it is necessary to create a static list of resources for <code>boskos</code>:
</p>

<code>boskos</code> configuration:
{{ yamlSyntax (index . "staticBoskosConfig") }}

<p>
A test may access the name of the resource that was acquired using the <code>${LEASED_RESOURCE}</code> environment
variable.
</p>

<h4 id="inspecting"><a href="#inspecting">Viewing Lease Activity Over Time</a></h4>
<p>
In order to view the number of concurrent jobs executing against any specific cloud, or to view the states of resources
in the lease system, a <a href="https://grafana-prow-monitoring.svc.ci.openshift.org/d/628a36ebd9ef30d67e28576a5d5201fd/boskos-dashboard?orgId=1">dashboard</a>
exists.
</p>

<h3 id="job-authors"><a href="#job-authors">Directions for Job Authors</a></h3>
<p>
Job authors should generally not be concerned with the process of acquiring a lease or the mechanisms behind it. However,
a quick overview of the process is given here to explain what is happening behind the scenes. Whenever <code>ci-operator</code>
runs a test target that has a <code>cluster_profile</code> set, a lease will be acquired before the test steps are
executed. <code>ci-operator</code> will acquire the lease, present the name of the leased resource to the job in the
<code>${LEASED_RESOURCE}</code> environment variable, send heartbeats as necessary and relinquish the lease when it is
no longer needed. In order for a <code>cluster_profile</code> to be supported, the cloud administrator will need to have
set up the quota slice resources, so by the time a job author uses a <code>cluster_profile</code>, all the infrastructure
should be in place.
</p>
`

const dynamicBoskosConfig = `resources:
- type: "my-new-quota-slice"
  state: "free"
  min-count: 10 # how many concurrent jobs can run against the cloud
  max-count: 10 # set equal to min-count
`

const staticBoskosConfig = `resources:
- type: "some-static-quota-slice"
  state: "free"
  names:
  - "server01.prod.service.com" # these names should be semantically meaningful to a client
  - "server02.prod.service.com"
  - "server03.prod.service.com"
`

const linksPage = `<h2 id="clusters"><a href="#clusters">Clusters</a></h2>
<p>The clusters that currently comprise CI are:</p>
<ul>
  <li>
    <a href="https://console.svc.ci.openshift.org"><code>api.ci</code></a>:
    legacy Openshift 3.11 cluster in GCP.  Job execution is being migrated out
    of it.
  </li>
  <li>
    <a href="https://console-openshift-console.apps.ci.l2s4.p1.openshiftapps.com"><code>app.ci</code></a>:
    Openshift Dedicated 4.x cluster containing most Prow services.
  </li>
  <li>
    <a href="https://console.build01.ci.openshift.org/"><code>build01</code></a>:
    Openshift 4.x cluster in AWS that executes a growing subset of the jobs.
  </li>
  <li>
    <a href="https://console.build02.ci.openshift.org/"><code>build02</code></a>:
    Openshift 4.x cluster in GCP that executes a growing subset of the jobs.
  </li>
  <li>
    <code>vsphere</code>: external cluster used for vSphere tests, not managed
    by DPTP.
  </li>
</ul>
<p>
Except for <code>vsphere</code>, these clusters use Github OAuth
authentication: all members of the Openshift organization in Github can log in.
</p>
<h2 id="services"><a href="#services">Services</a></h2>
<p>Below is a non-exhaustive list of CI services.</p>
<ul>
  <li>
    <a href="https://prow.ci.openshift.org">prow.ci.openshift.org</a>:
    main Prow dashboard with information about jobs, pull requests, the merge
    queue, etc.
  </li>
  <li>
    <a href="https://amd64.ocp.releases.ci.openshift.org">
      amd64.ocp.releases.ci.openshift.org
    </a>: OCP AMD 64 release status page.
  </li>
  <li>
    <a href="https://ppc64le.ocp.releases.ci.openshift.org">
      ppc64le.ocp.releases.ci.openshift.org
    </a>: OCP PowerPC 64 LE release status page.
  </li>
  <li>
    <a href="https://s390x.ocp.releases.ci.openshift.org">
      s390x.ocp.releases.ci.openshift.org
    </a>: OCP S390x release status page.
  </li>
  <li>
    <a href="https://amd64.origin.releases.ci.openshift.org">
      amd64.origin.releases.ci.openshift.org
    </a>: OKD release status page.
  </li>
  <li>
    <a href="https://search.ci.openshift.org">search.ci.openshift.org</a>:
    search tool for error messages in job logs and Bugzilla bugs.
  </li>
  <li>
    <a href="https://sippy.ci.openshift.org">sippy.ci.openshift.org</a>:
    CI release health summary.
  </li>
  <li>
    <a href="https://bugs.ci.openshift.org">bugs.ci.openshift.org</a>:
    Bugzilla bug overviews, backporting and release viewer.
  </li>
</ul>
<h2 id="contact"><a href="#contact">Contact</a></h2>
<p>DPTP maintains several means of contact:</p>
<ul>
  <li>
    Slack
    <ul>
      <li>
        <code>#announce-testplatform</code>: general announcements and outages.
        Usage is limited to the DPTP team, please do not post messages there.
      </li>
      <li>
        <code>#forum-testplatform</code>: general queries and discussion for
        the test platform.  For general assistance, ping
        <code>@dptp-helpdesk</code>. For reporting an outage, ping
        <code>@dptp-triage</code>.
      </li>
      <li>
        <code>#4-dev-triage</code>: queries and discussion for CI issues that
        are not caused by the test platform.
      </li>
      <li>
        <code>#forum-release-controller</code>: queries and discussion for the
        <a href="https://github.com/openshift/release-controller">
        <code>release-controller</code></a>, responsible for generating
        Openshift release/update payloads and displaying the release status
        pages.
      </li>
    </ul>
  </li>
  <li>
    <a href="https://issues.redhat.com/projects/DPTP">Jira</a>
    <ul>
      <li>
        <a href="https://issues.redhat.com/browse/DPTP-417">Story template</a>
        for feature requests.
      </li>
      <li>
        <a href="https://issues.redhat.com/browse/DPTP-419">Bug template</a>
        for bugs and issues.
      </li>
      <li>
        <a href="https://issues.redhat.com/browse/DPTP-897">Consulting
        template</a> for long-term, asynchronous discussion.
      </li>
    </ul>
  </li>
</ul>
`

const workflowType = "Workflow"
const jobType = "Job"

// workflowJob is a struct that can define either a workflow or a job
type workflowJob struct {
	api.RegistryWorkflow
	Type string
}

type Jobs struct {
	ContainsVariant bool
	Orgs            []Org
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
	Name     string
	Tests    []string
	Variants []Variant
}

type Variant struct {
	Name  string
	Tests []string
}

func repoSpan(r Repo, containsVariant bool) int {
	if !containsVariant {
		return len(r.Branches) + 1
	}
	rowspan := 0
	for _, branch := range r.Branches {
		rowspan += len(branch.Variants) + 1
		rowspan++
	}
	return rowspan + 1
}

func orgSpan(o Org, containsVariant bool) int {
	rowspan := 0
	for _, repo := range o.Repos {
		rowspan += repoSpan(repo, containsVariant)
	}
	return rowspan + 1
}

func githubLink(path string) template.HTML {
	link := fmt.Sprintf("https://github.com/openshift/release/blob/master/ci-operator/step-registry/%s", path)
	return template.HTML(fmt.Sprintf("<a href=\"%s\">%s</a>", link, link))
}

func createGitHubUserList(items []string) string {
	var builder strings.Builder
	builder.WriteString("<ul>")
	for _, item := range items {
		builder.WriteString("\n<li><a href=\"https://github.com/")
		builder.WriteString(item)
		builder.WriteString("\">")
		builder.WriteString(item)
		builder.WriteString("</a></li>")
	}
	builder.WriteString("</ul>")
	return builder.String()
}

func ownersBlock(owners repoowners.Config) template.HTML {
	var builder strings.Builder
	builder.WriteString("<h2 id=\"owners\"><a href=\"#owners\">Owners:</a></h2>")
	if len(owners.Approvers) > 0 {
		builder.WriteString("<h4 id=\"approvers\"><a href=\"#approvers\">Approvers:</a></h4>\n")
		builder.WriteString(createGitHubUserList(owners.Approvers))
	}
	if len(owners.Reviewers) > 0 {
		builder.WriteString("<h4 id=\"reviewers\"><a href=\"#reviewers\">Reviewers:</a></h4>\n")
		builder.WriteString(createGitHubUserList(owners.Reviewers))
	}
	if len(owners.RequiredReviewers) > 0 {
		builder.WriteString("<h4 id=\"required_reviewers\"><a href=\"#required_reviewers\">Required Reviewers:</a></h4>\n")
		builder.WriteString(createGitHubUserList(owners.RequiredReviewers))
	}
	if len(owners.Labels) > 0 {
		builder.WriteString("<h4 id=\"labels\"><a href\"#labels\">Labels:</a></h4>\n")
		builder.WriteString(createGitHubUserList(owners.Labels))
	}
	return template.HTML(builder.String())
}

func getBaseTemplate(workflows registry.WorkflowByName, chains registry.ChainByName, docs map[string]string) *template.Template {
	base := template.New("baseTemplate").Funcs(
		template.FuncMap{
			"docsForName": func(name string) string {
				return docs[name]
			},
			"testStepNameAndType": getTestStepNameAndType,
			"noescape": func(str string) template.HTML {
				return template.HTML(str)
			},
			"toLower": strings.ToLower,
			"workflowGraph": func(as string, wfType string) template.HTML {
				svg, err := WorkflowGraph(as, workflows, chains, wfType)
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
			"orgSpan":  orgSpan,
			"repoSpan": repoSpan,
			"inc": func(i int) int {
				return i + 1
			},
			"doubleInc": func(i int) int {
				return i + 2
			},
			"githubLink":  githubLink,
			"ownersBlock": ownersBlock,
		},
	)
	base, err := base.Parse(templateDefinitions)
	if err != nil {
		logrus.Errorf("Failed to load step list template: %v", err)
	}
	return base
}

type stepNameAndType struct {
	Name string
	Type string
}

func getTestStepNameAndType(step api.TestStep) stepNameAndType {
	var name, typeName string
	if step.LiteralTestStep != nil {
		name = step.As
	} else if step.Reference != nil {
		name = *step.Reference
		typeName = "reference"
	} else if step.Chain != nil {
		name = *step.Chain
		typeName = "chain"
	}
	return stepNameAndType{
		Name: name,
		Type: typeName,
	}
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

func helpHandler(subPath string, w http.ResponseWriter, _ *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Since(start)) }()
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
			"dockerfileSyntax": func(source string) template.HTML {
				formatted, err := syntaxDockerfile(source)
				if err != nil {
					logrus.Errorf("Failed to format source file: %v", err)
					return template.HTML(source)
				}
				return template.HTML(formatted)
			},
			"plaintextSyntax": func(source string) template.HTML {
				formatted, err := syntaxPlaintext(source)
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
		data["refFromImageExample"] = refFromImageExample
		data["credentialExample"] = credentialExample
		data["chainExample"] = chainExample
		data["workflowExample"] = workflowExample
		data["configExample1"] = configExample1
		data["configExample2"] = configExample2
		data["configExample3"] = configExample3
		data["configExample4"] = configExample4
		data["paramsExample"] = paramsExample
		data["paramsPropagation"] = paramsPropagation
		data["paramsRequired"] = paramsRequired
		data["paramsRequiredTest"] = paramsRequiredTest
	case "/adding-components":
		helpTemplate, err = helpFuncs.Parse(addingComponentPage)
	case "/release":
		data["updateconfigExample"] = updateconfigExample
		data["determinizeCheckExample"] = determinizeCheckExample
		data["makeUpdateExample"] = makeUpdateExample
		data["ciopConfigDirExample"] = ciopConfigDirExample
		data["makeNewRepoExample"] = makeNewRepoExample
		helpTemplate, err = helpFuncs.Parse(releasePage)
	case "/private-repositories":
		data["privateRepoProwgenConfigExample"] = privateRepoProwgenConfigExample
		helpTemplate, err = helpFuncs.Parse(privateRepositoriesPage)
	case "/examples":
		helpTemplate, err = helpFuncs.Parse(examplesPage)
		data["awsExample"] = awsExample
		data["imageExampleRef"] = imageExampleRef
		data["imageExampleCommands"] = imageExampleCommands
		data["imageExampleConfig"] = imageExampleConfig
		data["imageExampleLiteral"] = imageExampleLiteral
		data["imagePromotionConfig"] = imagePromotionConfig
		data["imageConsumptionConfig"] = imageConsumptionConfig
	case "/ci-operator":
		helpTemplate, err = helpFuncs.Parse(ciOperatorOverviewPage)
		data["ciOperatorInputConfig"] = ciOperatorInputConfig
		data["ciOperatorPipelineConfig"] = ciOperatorPipelineConfig
		data["multistageDockerfile"] = multistageDockerfile
		data["ciOperatorImageConfig"] = ciOperatorImageConfig
		data["ciOperatorPromotionConfig"] = ciOperatorPromotionConfig
		data["ciOperatorTagSpecificationConfig"] = ciOperatorTagSpecificationConfig
		data["ciOperatorReleaseConfig"] = ciOperatorReleaseConfig
		data["ciOperatorContainerTestConfig"] = ciOperatorContainerTestConfig
		data["ciOperatorPostsubmitTestConfig"] = ciOperatorPostsubmitTestConfig
		data["ciOperatorPeriodicTestConfig"] = ciOperatorPeriodicTestConfig
		data["ciOperatorProjectImageBuildroot"] = ciOperatorProjectImageBuildroot
		data["ciOperatorBuildRootFromRepo"] = ciOperatorBuildRootFromRepo
		data["ciOperatorBuildRootInRepo"] = ciOperatorBuildRootInRepo
		data["ciOperatorContainerTestWithDependenciesConfig"] = ciOperatorContainerTestWithDependenciesConfig
		data["depsPropagation"] = depsPropagation
	case "/leases":
		helpTemplate, err = helpFuncs.Parse(quotasAndLeasesPage)
		data["dynamicBoskosConfig"] = dynamicBoskosConfig
		data["staticBoskosConfig"] = staticBoskosConfig
	case "/links":
		helpTemplate, err = helpFuncs.Parse(linksPage)
	case "/operators":
		helpTemplate, err = helpFuncs.Parse(optionalOperatorOverviewPage)
		data["optionalOperatorBundleConfig"] = optionalOperatorBundleConfig
		data["optionalOperatorTestConfig"] = optionalOperatorTestConfig
		data["optionalOperatorIndexConsumerStep"] = optionalOperatorIndexConsumerStep
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

func mainPageHandler(agent agents.RegistryAgent, templateString string, w http.ResponseWriter, _ *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Since(start)) }()

	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	refs, chains, wfs, docs, _ := agent.GetRegistryComponents()
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

func WebRegHandler(regAgent agents.RegistryAgent, confAgent agents.ConfigAgent) http.HandlerFunc {
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
				searchHandler(confAgent, w, req)
			case "job":
				jobHandler(regAgent, confAgent, w, req)
			default:
				writeErrorPage(w, errors.New("Invalid path"), http.StatusNotImplemented)
			}
			return
		} else if len(splitURI) == 2 {
			switch splitURI[0] {
			case "reference":
				referenceHandler(regAgent, w, req)
				return
			case "chain":
				chainHandler(regAgent, w, req)
				return
			case "workflow":
				workflowHandler(regAgent, w, req)
				return
			default:
				writeErrorPage(w, fmt.Errorf("Component type %s not found", splitURI[0]), http.StatusNotFound)
				return
			}
		}
		writeErrorPage(w, errors.New("Invalid path"), http.StatusNotImplemented)
	}
}

func syntax(source string, lexer chroma.Lexer) (string, error) {
	var output bytes.Buffer
	style := styles.Get("dracula")
	// highlighted lines based on linking currently require WithClasses to be used
	formatter := html.New(html.Standalone(false), html.LinkableLineNumbers(true, "line"), html.WithLineNumbers(true), html.WithClasses(true))
	iterator, err := lexer.Tokenise(nil, source)
	if err != nil {
		return "", fmt.Errorf("failed to tokenise source: %w", err)
	}
	output.WriteString("<style>")
	if err := formatter.WriteCSS(&output, style); err != nil {
		return "", fmt.Errorf("failed to write css: %w", err)
	}
	output.WriteString("</style>")
	err = formatter.Format(&output, style, iterator)
	return output.String(), err
}

func syntaxPlaintext(source string) (string, error) {
	return syntax(source, lexers.Get("plaintext"))
}

func syntaxYAML(source string) (string, error) {
	return syntax(source, lexers.Get("yaml"))
}

func syntaxDockerfile(source string) (string, error) {
	return syntax(source, lexers.Get("Dockerfile"))
}

func syntaxBash(source string) (string, error) {
	return syntax(source, lexers.Get("bash"))
}

func referenceHandler(agent agents.RegistryAgent, w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Since(start)) }()
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
			"githubLink":  githubLink,
			"ownersBlock": ownersBlock,
		},
	).Parse(referencePage)
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %w", err), http.StatusInternalServerError)
		return
	}
	refs, _, _, docs, metadata := agent.GetRegistryComponents()
	if _, ok := refs[name]; !ok {
		writeErrorPage(w, fmt.Errorf("Could not find reference `%s`. If you reached this page via a link provided in the logs of a failed test, the failed step may be a literal defined step, which does not exist in the step registry. Please look at the job info page for the failed test instead.", name), http.StatusNotFound)
		return
	}
	refMetadataName := fmt.Sprint(name, load.RefSuffix)
	if _, ok := metadata[refMetadataName]; !ok {
		writeErrorPage(w, fmt.Errorf("Could not find metadata for file `%s`. Please contact the Developer Productivity Test Platform.", refMetadataName), http.StatusInternalServerError)
		return
	}
	ref := struct {
		Reference api.RegistryReference
		Metadata  api.RegistryInfo
	}{
		Reference: api.RegistryReference{
			LiteralTestStep: api.LiteralTestStep{
				As:       name,
				Commands: refs[name].Commands,
				From:     refs[name].From,
			},
			Documentation: docs[name],
		},
		Metadata: metadata[refMetadataName],
	}
	writePage(w, "Registry Step Help Page", page, ref)
}

func chainHandler(agent agents.RegistryAgent, w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Since(start)) }()
	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	name := path.Base(req.URL.Path)

	_, chains, _, docs, metadata := agent.GetRegistryComponents()
	page := getBaseTemplate(nil, chains, docs)
	page, err := page.Parse(chainPage)
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %w", err), http.StatusInternalServerError)
		return
	}
	if _, ok := chains[name]; !ok {
		writeErrorPage(w, fmt.Errorf("Could not find chain %s", name), http.StatusNotFound)
		return
	}
	chainMetadataName := fmt.Sprint(name, load.ChainSuffix)
	if _, ok := metadata[chainMetadataName]; !ok {
		writeErrorPage(w, fmt.Errorf("Could not find metadata for file `%s`. Please contact the Developer Productivity Test Platform.", chainMetadataName), http.StatusInternalServerError)
		return
	}
	chain := struct {
		Chain    api.RegistryChain
		Metadata api.RegistryInfo
	}{
		Chain: api.RegistryChain{
			As:            name,
			Documentation: docs[name],
			Steps:         chains[name].Steps,
		},
		Metadata: metadata[chainMetadataName],
	}
	writePage(w, "Registry Chain Help Page", page, chain)
}

func workflowHandler(agent agents.RegistryAgent, w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Since(start)) }()
	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	name := path.Base(req.URL.Path)

	_, chains, workflows, docs, metadata := agent.GetRegistryComponents()
	page := getBaseTemplate(workflows, chains, docs)
	page, err := page.Parse(workflowJobPage)
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %w", err), http.StatusInternalServerError)
		return
	}
	if _, ok := workflows[name]; !ok {
		writeErrorPage(w, fmt.Errorf("Could not find workflow %s", name), http.StatusNotFound)
		return
	}
	workflowMetadataName := fmt.Sprint(name, load.WorkflowSuffix)
	if _, ok := metadata[workflowMetadataName]; !ok {
		writeErrorPage(w, fmt.Errorf("Could not find metadata for file `%s`. Please contact the Developer Productivity Test Platform.", workflowMetadataName), http.StatusInternalServerError)
		return
	}
	workflow := struct {
		Workflow workflowJob
		Metadata api.RegistryInfo
	}{
		Workflow: workflowJob{
			RegistryWorkflow: api.RegistryWorkflow{
				As:            name,
				Documentation: docs[name],
				Steps:         workflows[name],
			},
			Type: workflowType},
		Metadata: metadata[workflowMetadataName],
	}
	writePage(w, "Registry Workflow Help Page", page, workflow)
}

func findConfigForJob(testName string, config api.ReleaseBuildConfiguration) (api.MultiStageTestConfiguration, error) {
	for _, test := range config.Tests {
		if test.As == testName {
			if test.MultiStageTestConfiguration != nil {
				return *test.MultiStageTestConfiguration, nil
			}
			return api.MultiStageTestConfiguration{}, fmt.Errorf("Provided job %s is not a multi stage type test", testName)
		}
	}
	return api.MultiStageTestConfiguration{}, fmt.Errorf("Could not find job %s. Job either does not exist or is not a multi stage test", testName)
}

func MetadataFromQuery(w http.ResponseWriter, r *http.Request) (api.Metadata, error) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusNotImplemented)
		err := fmt.Errorf("expected GET, got %s", r.Method)
		if _, errWrite := w.Write([]byte(http.StatusText(http.StatusNotImplemented))); errWrite != nil {
			return api.Metadata{}, fmt.Errorf("%s and writing the response body failed with %w", err.Error(), errWrite)
		}
		return api.Metadata{}, err
	}
	org := r.URL.Query().Get(OrgQuery)
	if org == "" {
		missingQuery(w, OrgQuery)
		return api.Metadata{}, fmt.Errorf("missing query %s", OrgQuery)
	}
	repo := r.URL.Query().Get(RepoQuery)
	if repo == "" {
		missingQuery(w, RepoQuery)
		return api.Metadata{}, fmt.Errorf("missing query %s", RepoQuery)
	}
	branch := r.URL.Query().Get(BranchQuery)
	if branch == "" {
		missingQuery(w, BranchQuery)
		return api.Metadata{}, fmt.Errorf("missing query %s", BranchQuery)
	}
	variant := r.URL.Query().Get(VariantQuery)
	return api.Metadata{
		Org:     org,
		Repo:    repo,
		Branch:  branch,
		Variant: variant,
	}, nil
}

func missingQuery(w http.ResponseWriter, field string) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, "%s query missing or incorrect", field)
}

func jobHandler(regAgent agents.RegistryAgent, confAgent agents.ConfigAgent, w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Since(start)) }()
	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	metadata, err := MetadataFromQuery(w, r)
	if err != nil {
		return
	}
	test := r.URL.Query().Get(TestQuery)
	if test == "" {
		missingQuery(w, TestQuery)
		return
	}
	configs, err := confAgent.GetMatchingConfig(metadata)
	if err != nil {
		writeErrorPage(w, err, http.StatusNotFound)
		return
	}
	config, err := findConfigForJob(test, configs)
	if err != nil {
		writeErrorPage(w, err, http.StatusNotFound)
		return
	}
	// TODO(apavel): support jobs other than presubmits
	name := metadata.JobName("pull", test)
	_, chains, workflows, docs, _ := regAgent.GetRegistryComponents()
	jobWorkflow, docs := jobToWorkflow(name, config, workflows, docs)
	updatedWorkflows := make(registry.WorkflowByName)
	for k, v := range workflows {
		updatedWorkflows[k] = v
	}
	updatedWorkflows[name] = jobWorkflow.Steps
	page := getBaseTemplate(updatedWorkflows, chains, docs)
	page, err = page.Parse(workflowJobPage)
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %w", err), http.StatusInternalServerError)
		return
	}
	workflow := struct {
		Workflow workflowJob
		Metadata api.RegistryInfo
	}{
		Workflow: jobWorkflow,
		Metadata: api.RegistryInfo{},
	}
	writePage(w, "Job Test Workflow Help Page", page, workflow)
}

// addJob adds a test to the specified org, repo, and branch in the Jobs struct in alphabetical order
func (j *Jobs) addJob(orgName, repoName, branchName, variantName, testName string) {
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
	variantIndex := -1
	if variantName != "" {
		j.ContainsVariant = true
		variantIndex = 0
		variantExists := false
		for _, currVariant := range j.Orgs[orgIndex].Repos[repoIndex].Branches[branchIndex].Variants {
			if diff := strings.Compare(currVariant.Name, variantName); diff == 0 {
				variantExists = true
				break
			} else if diff > 0 {
				break
			}
			variantIndex++
		}
		if !variantExists {
			newVariant := Variant{Name: variantName}
			variants := j.Orgs[orgIndex].Repos[repoIndex].Branches[branchIndex].Variants
			j.Orgs[orgIndex].Repos[repoIndex].Branches[branchIndex].Variants = append(variants[:variantIndex], append([]Variant{newVariant}, variants[variantIndex:]...)...)
		}
	}
	// a single test shouldn't be added multiple times, but that case should be handled correctly just in case
	testIndex := 0
	testExists := false
	var testsArr []string
	if variantIndex == -1 {
		testsArr = j.Orgs[orgIndex].Repos[repoIndex].Branches[branchIndex].Tests
	} else {

		testsArr = j.Orgs[orgIndex].Repos[repoIndex].Branches[branchIndex].Variants[variantIndex].Tests
	}
	for _, currTestName := range testsArr {
		if diff := strings.Compare(currTestName, testName); diff == 0 {
			testExists = true
			break
		} else if diff > 0 {
			break
		}
		testIndex++
	}
	if !testExists {
		if variantIndex == -1 {
			j.Orgs[orgIndex].Repos[repoIndex].Branches[branchIndex].Tests = append(testsArr[:testIndex], append([]string{testName}, testsArr[testIndex:]...)...)
		} else {
			j.Orgs[orgIndex].Repos[repoIndex].Branches[branchIndex].Variants[variantIndex].Tests = append(testsArr[:testIndex], append([]string{testName}, testsArr[testIndex:]...)...)
		}
	}
}

// getAllMultiStageTests return a map that has the config name in org-repo-branch format as the key and the test names for multi stage jobs as the value
func getAllMultiStageTests(confAgent agents.ConfigAgent) *Jobs {
	jobs := &Jobs{}
	configs := confAgent.GetAll()
	for org, orgConfigs := range configs {
		for repo, repoConfigs := range orgConfigs {
			for _, releaseConfig := range repoConfigs {
				for _, test := range releaseConfig.Tests {
					if test.MultiStageTestConfiguration != nil {
						jobs.addJob(org, repo, releaseConfig.Metadata.Branch, releaseConfig.Metadata.Variant, test.As)
					}
				}
			}
		}
	}
	return jobs
}

func searchHandler(confAgent agents.ConfigAgent, w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Since(start)) }()
	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	searchTerm := req.URL.Query().Get("job")
	matches := getAllMultiStageTests(confAgent)
	if searchTerm != "" {
		matches = searchJobs(matches, searchTerm)
	}
	page := getBaseTemplate(nil, nil, nil)
	page, err := page.Parse(jobSearchPage)
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %w", err), http.StatusInternalServerError)
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
						matches.addJob(org.Name, repo.Name, branch.Name, "", test)
					}
				}
				for _, variant := range branch.Variants {
					for _, test := range variant.Tests {
						fullJobName := fmt.Sprintf("%s-%s-%s-%s-%s", org.Name, repo.Name, branch.Name, variant.Name, test)
						if strings.Contains(fullJobName, search) {
							matches.addJob(org.Name, repo.Name, branch.Name, variant.Name, test)
						}
					}
				}
			}
		}
	}
	return matches
}
