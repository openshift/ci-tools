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
	htmlformatter "github.com/alecthomas/chroma/formatters/html"
	"github.com/alecthomas/chroma/lexers"
	"github.com/alecthomas/chroma/styles"
	"github.com/russross/blackfriday/v2"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/repoowners"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/html"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/registry"
	registryserver "github.com/openshift/ci-tools/pkg/registry/server"
)

var ciOperatorRefRendered []byte
var baseTemplate *template.Template

func init() {
	if renderedString, err := syntaxYAML(ciOperatorReferenceYaml); err != nil {
		panic(fmt.Sprintf("Failed to render the ci-operator config as yaml: %v", err))
	} else {
		ciOperatorRefRendered = []byte("<style>body {background-color: #282a36;}</style>" + renderedString)
	}

	var err error
	if baseTemplate, err = getBaseTemplate(); err != nil {
		panic(fmt.Sprintf("Failed to parse the base template: %v", err))
	}
}

const (
	TestQuery = "test"
)

const bodyStart = `
<nav class="navbar navbar-expand-lg navbar-light bg-light">
  <a class="navbar-brand" href="/">Openshift CI Step Registry</a>
  <button class="navbar-toggler" type="button" data-toggle="collapse" data-target="#navbarSupportedContent" aria-controls="navbarSupportedContent" aria-expanded="false" aria-label="Toggle navigation">
    <span class="navbar-toggler-icon"></span>
  </button>

  <div class="collapse navbar-collapse" id="navbarSupportedContent">
    <ul class="navbar-nav mr-auto">
      <li class="nav-item dropdown">
        <a id="nav-home-dropdown" class="nav-link dropdown-toggle" href="/" role="button" data-toggle="dropdown" aria-haspopup="true" aria-expanded="false">
          Home <span class="sr-only">(current)</span>
        </a>
        <div class="dropdown-menu" aria-labelledby="nav-home-dropdown">
          <a class="dropdown-item" href="/#workflows">Workflows</a>
          <a class="dropdown-item" href="/#chains">Chains</a>
          <a class="dropdown-item" href="/#steps">Steps</a>
        </div>
      </li>
      <li class="nav-item">
        <a class="nav-link" href="/search">Jobs</a>
      </li>
      <li class="nav-item">
        <a class="nav-link" href="http://docs.ci.openshift.org">Help</a>
      </li>
      <li class="nav-item">
        <a class="nav-link" href="/ci-operator-reference">CI-Operator Reference</a>
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

const bodyEnd = `
<p class="small">Source code for this page located on <a href="https://github.com/openshift/ci-tools">GitHub</a></p>
</div>`

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
<h3 id="image"><a href="#image">Container image used for this step:</a> <span style="font-family:monospace">{{ fromImage .Reference.From .Reference.FromImage }}</span></h3>
<p id="image">{{ fromImageDescription .Reference.From .Reference.FromImage }}<d/p>
<h3 id="environment"><a href="#environment">Environment</a></h3>
{{ template "stepEnvironment" .Reference }}
<h3 id="source"><a href="#source">Source Code</a></h3>
{{ syntaxedSource .Reference.Commands }}
<h3 id="properties"><a href="#properties">Properties</a></h3>
{{ template "referenceProperties" .Reference }}
<h3 id="github"><p><a href="#github">GitHub Link:</a></h3></p>{{ githubLink .Metadata.Path }}
{{ ownersBlock .Metadata.Owners }}
`

const chainPage = `
<h2 id="title"><a href="#title">Chain:</a> <nobr style="font-family:monospace">{{ .Chain.As }}</nobr></h2>
<p id="documentation">{{ .Chain.Documentation }}</p>
<h3 id="steps" title="Step run by the chain, in runtime order"><a href="#steps">Steps</a></h3>
{{ template "stepTable" .Chain.Steps}}
<h3 id="dependencies" title="Dependencies of steps involved in this chain"><a href="#dependencies">Dependencies</a></h3>
{{ $depTable := "chain" }}
{{ template "dependencyTable" .Chain.As }}
<h3 id="environment" title="Environmental variables consumed through this chain"><a href="#environment">Environment</a></h3>
{{ template "refEnvironment" .Chain.As }}
<h3 id="graph" title="Visual representation of steps run by this chain"><a href="#graph">Step Graph</a></h3>
{{ chainGraph .Chain.As }}
<h3 id="github"><a href="#github">GitHub Link:</a></h3>{{ githubLink .Metadata.Path }}
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
	<h3 id="cluster_profile"><a href="#cluster_profile">Cluster Profile:</a> <span style="font-family:monospace">{{ .Workflow.Steps.ClusterProfile }}</span></h3>
{{ end }}
<h3 id="pre" title="Steps run by this {{ toLower $type }} to set up and configure the tests, in runtime order"><a href="#pre">Pre Steps</a></h3>
{{ template "stepTable" .Workflow.Steps.Pre }}
<h3 id="test" title="Steps in the {{ toLower $type }} that run actual tests, in runtime order"><a href="#test">Test Steps</a></h3>
{{ template "stepTable" .Workflow.Steps.Test }}
<h3 id="post" title="Steps run by this {{ toLower $type }} to clean up and teardown test resources, in runtime order"><a href="#post">Post Steps</a></h3>
{{ template "stepTable" .Workflow.Steps.Post }}
<h3 id="dependencies" title="Dependencies of this {{ toLower $type }}"><a href="#dependencies">Dependencies</a></h3>
{{ $depTable := "workflow" }}
{{ template "dependencyTable" .Workflow.As }}
<h3 id="environment" title="Environmental variables consumed through this workflow"><a href="#environment">Environment</a></h3>
{{ template "refEnvironment" .Workflow.As }}
<h3 id="graph" title="Visual representation of steps run by this {{ toLower $type }}"><a href="#graph">Step Graph</a></h3>
{{ workflowGraph .Workflow.As .Workflow.Type }}
{{ if eq $type "Workflow" }}
<h3 id="github"><a href="#github">GitHub Link:</a></h3>{{ githubLink .Metadata.Path }}
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

{{ define "referenceProperties" }}
  <table class="table">
  <thead>
  <tr>
    <th title="Property name" class="info">Property</th>
    <th title="Property name" class="info">Value</th>
    <th title="Property name" class="info">Description</th>
  </tr>
  </thead>
  <tbody>
  {{ if .Timeout }}
    <tr>
      <td>Step timeout<sup>[<a href="https://docs.ci.openshift.org/architecture/timeouts/#step-registry-test-process-timeouts">?</a>]</sup></td>
      <td>{{ .Timeout.String }}</td>
      <td>Limits the execution time of the step.</td>
    </tr>
  {{ end }}
  {{ if .GracePeriod }}
    <tr>
      <td>Termination grace period<sup>[<a href="https://docs.ci.openshift.org/architecture/timeouts/#step-registry-test-process-timeouts">?</a>]</sup></td>
      <td>{{ .GracePeriod.String }}</td>
      <td>Period of time until SIGKILL signal is sent to the test pod (after SIGTERM signal is sent).</td>
    </tr>
  {{ end }}
  {{ if .Resources }}
    {{ range $name, $value := .Resources.Requests }}
     <tr>
       <td>Resource requests ({{ $name }})</td>
       <td>{{ $value }}</td>
       <td>Used in <span style="font-family:monospace">.resources.requests</span> of the pod running this step.</td>
     </tr>
    {{ end }}
    {{ range $name, $value := .Resources.Limits }}
     <tr>
       <td>Resource limits ({{ $name }})</td>
       <td>{{ $value }}</td>
       <td>Used in <span style="font-family:monospace">.resources.limits</span> of the pod running this step.</td>
     </tr>
    {{ end }}
  {{ end }}
  {{ if .OptionalOnSuccess }}
    <tr>
      <td>Optional on success<sup>[<a href="https://docs.ci.openshift.org/architecture/step-registry/#skipping-post-steps-on-success">?</a>]</sup></td>
      <td>{{ .OptionalOnSuccess}}</td>
      <td>Allows the step to be skipped if all steps in <span style="font-family:monospace">pre</span> and <span style="font-family:monospace">test</span> phases succeeded.</td>
    </tr>
  {{ end }}
  {{ if .BestEffort }}
    <tr>
      <td>Best effort<sup>[<a href="https://docs.ci.openshift.org/architecture/step-registry/#marking-post-steps-best-effort">?</a>]</sup></td>
      <td>{{ .BestEffort }}</td>
      <td>This step's failure will not cause whole job to fail if the step is run in <span style="font-family:monospace">post</span> phase.</td>
    </tr>
  {{ end }}
  {{ if .Cli }}
    <tr>
      <td>Inject <span style="font-family:monospace">oc</span> CLI<sup>[<a href="https://docs.ci.openshift.org/architecture/step-registry/#sharing-data-between-steps">?</a>]</sup></td>
      <td>{{ .Cli }}</td>
      <td>The <span style="font-family:monospace">oc</span> CLI sourced from the specified release is injected into this step's' image.</td>
    </tr>
  {{ end }}
  </tbody>
  </table>
{{ end  }}

{{ define "dependencyTable" }}
  {{ $data := getDependencies . }}

  {{ if eq 0 ( len ($data.Items)) }}
    No step in this {{ $data.Type }} sets dependencies.<sup>[<a href="https://docs.ci.openshift.org/architecture/ci-operator/#referring-to-images-in-tests">?</a>]</sup>
  {{ else }}
  <table class="table">
  <thead>
    <tr>
     <th title="Image on which steps in this {{ $data.Type }}">Image</th>
     <th title="Environmental variable exposing the pullspec to steps">Exposed As</th>
     {{ if ne $data.Type "chain" }}
       <th title="Whether the value is an override in the workflow">Override<sup>[<a href="https://docs.ci.openshift.org/architecture/ci-operator/#dependency-overrides">?</a>]</sup></th>
     {{ end }}
     <th title="Which steps consume the image exposed by this variable">Required By Steps</th>
    <tr>
  </thead>
  <tbody>
  {{ range $dep, $vars := $data.Items }}
    {{ $first := true }}
    {{ range $var, $line := $vars }}
    <tr>
      {{ if eq $first true }}
      <td rowspan={{ len $vars }} style="font-family:monospace">{{ $dep }}</td>
      {{ end }}
      <td><span style="font-family:monospace">{{ $var }}</span></td>
      {{ if ne $data.Type "chain" }}
      <td>{{ if $line.Override }}yes{{ else }}no{{ end }}</td>
      {{ end }}
      <td>
      {{ range $i, $step := $line.Steps }}
        <a href="/reference/{{ $step }}">{{ $step }}</a>
      {{ end }}
     </td>
    </tr>
    {{ $first = false }}
    {{ end }}
  {{ end }}
  </tbody>
  </table>
  {{ end }}
{{ end }}

{{ define "refEnvironment" }}
	{{ $data := getEnvironment . }}
    {{ if eq 0 ( len ($data.Items)) }}
      <p>This {{ $data.Type }} consumes no environmental variables except the <a href="https://docs.ci.openshift.org/architecture/step-registry/#available-environment-variables">defaults</a>.</p>
    {{ else }}
        <p>In addition to the <a href="https://docs.ci.openshift.org/architecture/step-registry/#available-environment-variables">default</a> environment, the following variables are consumed through this {{ $data.Type }}</p>
        <table class="table">
        <thead>
        <tr>
         <th title="Environmental variable" class="info">Variable Name</th>
         <th title="Content value" class="info">Variable Content</th>
		 <th title="Consumed By Steps" class="info">Consumed By Steps</th>
        </tr>
       </thead>
       <tbody>
       {{ range $name, $env := $data.Items }}
       <tr>
         <td style="font-family:monospace">{{ $name }}</td>
		 <td>
		   {{ $env.Documentation }}
		   {{ if $env.Default }}
		   {{ if gt (len $env.Default) 0 }}
			 (default: <span style="font-family:monospace">{{ $env.Default }}</span>)
		   {{ end }}
		   {{ end }}
		 </td>
		 <td>
             {{ range $i, $step := $env.Steps }}
               <a href="/reference/{{ $step }}">{{ $step }}</a>
             {{ end }}
         </td>
       </tr>
       {{ end  }}
       </tbody>
       </table>
    {{ end }}
{{ end }}

{{ define "stepEnvironment" }}
{{ if and (eq (len .Dependencies) 0) (eq (len .Environment) 0) (eq (len .Leases) 0) }}
  <p>Step exposes no environmental variables except the <a href="https://docs.ci.openshift.org/architecture/step-registry/#available-environment-variables">defaults</a>.</p>
{{ else }}
    <p>In addition to the <a href="https://docs.ci.openshift.org/architecture/step-registry/#available-environment-variables">default</a> environment, the step exposes the following:</p>
    <table class="table">
    <thead>
    <tr>
     <th title="Environmental variable" class="info">Variable Name</th>
     <th title="Content type" class="info">Type</th>
     <th title="Content value" class="info">Variable Content</th>
    </tr>
   </thead>
   <tbody>
   {{ range $idx, $dep := .Dependencies }}
   <tr>
     <td style="font-family:monospace">{{ $dep.Env }}</td>
     <td>Dependency<sup>[<a href="https://docs.ci.openshift.org/architecture/ci-operator/#referring-to-images-in-tests">?</a>]</sup></td>
     <td>Pull specification for <span style="font-family:monospace">{{ $dep.Name }}</span> image</td>
   </tr>
   {{ end  }}
   {{ range $idx, $env := .Environment }}
   <tr>
     <td style="font-family:monospace">{{ $env.Name }}</td>
     <td>Parameter<sup>[<a href="https://docs.ci.openshift.org/architecture/step-registry/#parameters">?</a>]</sup></td>
     <td>
       {{ $env.Documentation | markdown}}
       {{ if $env.Default }}
       {{ if gt (len $env.Default) 0 }}
         (default: <span style="font-family:monospace">{{ $env.Default }}</span>)
       {{ end }}
       {{ end }}
     </td>
   </tr>
   {{ end }}
   {{ range $idx, $lease := .Leases }}
   <tr>
     <td style="font-family:monospace">{{ $lease.Env }}</td>
     <td>Lease<sup>[<a href="https://docs.ci.openshift.org/architecture/step-registry/#explicit-lease-configuration">?</a>]</sup></td>
     <td>
       {{ if gt $lease.Count 1 }}
         Names of {{ $lease.Count }} acquired leases of type <span style="font-family:monospace">{{ $lease.ResourceType }}</span>, separated by space
       {{ else }}
         Name of the acquired lease of type <span style="font-family:monospace">{{ $lease.ResourceType }}</span>
       {{ end }}
     </td>
   </tr>
   {{ end }}
   </tbody>
   </table>
{{ end }}
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
	link := fmt.Sprintf("https://github.com/openshift/release/blob/main/ci-operator/step-registry/%s", path)
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
	builder.WriteString("<h3 id=\"owners\"><a href=\"#owners\">Owners:</a></h3>")
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

func getBaseTemplate() (*template.Template, error) {
	base := template.New("baseTemplate").Funcs(
		template.FuncMap{
			// These three are placeholders to be overwritten by the handlers
			// that actually care about this data (see set{Docs,ChainGraph,WorkflowGraph) functions
			"docsForName":     func(string) string { return "" },
			"workflowGraph":   func(_, _ string) string { return "" },
			"chainGraph":      func(string) string { return "" },
			"getDependencies": func(string) dependencyData { return dependencyData{} },
			"getEnvironment": func(string) environmentData {
				return environmentData{}
			},

			"testStepNameAndType": getTestStepNameAndType,
			"noescape": func(str string) template.HTML {
				return template.HTML(str)
			},
			"toLower":  strings.ToLower,
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
	return base.Funcs(template.FuncMap{"markdown": markDowner}).Parse(templateDefinitions)
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
	_ = html.WritePage(w, title, bodyStart, bodyEnd, body, data)
}

func setDocs(t *template.Template, docs map[string]string) *template.Template {
	return t.Funcs(
		template.FuncMap{
			"docsForName": func(name string) string {
				return docs[name]
			},
		})
}

func setWorkflowGraph(t *template.Template, chains registry.ChainByName, workflows registry.WorkflowByName) *template.Template {
	return t.Funcs(
		template.FuncMap{
			"workflowGraph": func(as string, wfType string) template.HTML {
				svg, err := WorkflowGraph(as, workflows, chains, wfType)
				if err != nil {
					return template.HTML(err.Error())
				}
				return template.HTML(svg)
			},
		})
}

type environmentLine struct {
	Documentation string
	Default       *string
	Steps         []string
}

type environmentData struct {
	Items map[string]environmentLine
	Type  string
}

func getEnvironmentDataItems(worklist []api.TestStep, registryRefs registry.ReferenceByName, registryChains registry.ChainByName) map[string]environmentLine {
	data := map[string]environmentLine{}

	add := func(name, documentation, step string, defaultVal *string) {
		if _, ok := data[name]; !ok {
			data[name] = environmentLine{
				Documentation: documentation,
				Default:       defaultVal,
			}
		}

		line := data[name]
		line.Steps = append(line.Steps, step)
		data[name] = line
	}

	seenChains := sets.New[string]()

	for len(worklist) != 0 {
		step := worklist[0]
		worklist = worklist[1:]
		switch {
		case step.Reference != nil:
			ref, ok := registryRefs[*step.Reference]
			if !ok {
				logrus.WithField("step-name", *step.Reference).Error("failed to resolve step environment, step not found in registry")
				continue
			}
			for _, env := range ref.Environment {
				add(env.Name, env.Documentation, ref.As, env.Default)
			}
		case step.Chain != nil:
			chainName := *step.Chain
			if !seenChains.Has(chainName) {
				seenChains.Insert(chainName)
				chain, ok := registryChains[chainName]
				if !ok {
					logrus.WithField("chain-name", chainName).Error("failed to resolve chain environment, chain not found in registry")
				}
				worklist = append(worklist, chain.Steps...)
			}
		case step.LiteralTestStep != nil:
			for _, env := range step.Environment {
				add(env.Name, env.Documentation, step.As, env.Default)
			}
		}
	}

	return data
}

type dependencyLine struct {
	Steps    []string
	Override bool
}
type dependencyVars map[string]dependencyLine

// release:latest --> ENV_VAR_1 -> { override=false steps=step1,step2 }
//
//	\-> ENV_VAR_2 -> { override=true steps=step3 }
type dependencyData struct {
	Items map[string]dependencyVars
	Type  string
}

func getDependencyDataItems(worklist []api.TestStep, registryRefs registry.ReferenceByName, registryChains registry.ChainByName, overrides api.TestDependencies) map[string]dependencyVars {
	var data map[string]dependencyVars
	add := func(image, variable, step string) {
		override, isOverride := overrides[variable]
		if isOverride {
			image = override
		}

		if data == nil {
			data = map[string]dependencyVars{}
		}

		if _, ok := data[image]; !ok {
			data[image] = dependencyVars{}
		}
		if _, ok := data[image][variable]; !ok {
			data[image][variable] = dependencyLine{Override: isOverride}
		}
		line := data[image][variable]
		line.Steps = append(line.Steps, step)
		data[image][variable] = line
	}

	seenChains := sets.New[string]()
	for len(worklist) != 0 {
		step := worklist[0]
		worklist = worklist[1:]
		switch {
		case step.Reference != nil:
			ref, ok := registryRefs[*step.Reference]
			if !ok {
				logrus.WithField("step-name", *step.Reference).Error("failed to resolve step dependencies, step not found in registry")
				continue
			}
			for _, dep := range ref.Dependencies {
				add(dep.Name, dep.Env, ref.As)
			}
		case step.Chain != nil:
			chainName := *step.Chain
			if !seenChains.Has(chainName) {
				seenChains.Insert(chainName)
				chain, ok := registryChains[chainName]
				if !ok {
					logrus.WithField("chain-name", chainName).Error("failed to resolve chain dependencies, chain not found in registry")
				}
				worklist = append(worklist, chain.Steps...)
			}
		case step.LiteralTestStep != nil:
			for _, dep := range step.Dependencies {
				add(dep.Name, dep.Env, step.As)
			}
		}
	}

	return data
}

func setWorkflowDependencies(t *template.Template, refs registry.ReferenceByName, chains registry.ChainByName, workflows registry.WorkflowByName) *template.Template {
	return t.Funcs(
		template.FuncMap{
			"getDependencies": func(as string) dependencyData {
				ret := dependencyData{
					Type: "workflow",
				}

				workflow, ok := workflows[as]
				if !ok {
					logrus.WithField("workflow-name", as).Error("failed to resolve workflow steps: workflow not found in registry")
					return ret
				}

				var worklist []api.TestStep
				for _, steps := range [][]api.TestStep{workflow.Pre, workflow.Test, workflow.Post} {
					worklist = append(worklist, steps...)
				}

				ret.Items = getDependencyDataItems(worklist, refs, chains, workflow.Dependencies)
				return ret
			},
		})
}

func setChainDependencies(t *template.Template, refs registry.ReferenceByName, chains registry.ChainByName) *template.Template {
	return t.Funcs(
		template.FuncMap{
			"getDependencies": func(as string) dependencyData {
				ret := dependencyData{
					Type: "chain",
				}

				chain, ok := chains[as]
				if !ok {
					logrus.WithField("chain-name", as).Error("failed to resolve chain steps: step not found in registry")
					return ret
				}

				ret.Items = getDependencyDataItems(chain.Steps, refs, chains, nil)
				return ret
			},
		})
}

func setChainEnvironment(t *template.Template, refs registry.ReferenceByName, chains registry.ChainByName) *template.Template {
	return t.Funcs(
		template.FuncMap{
			"getEnvironment": func(as string) environmentData {
				ret := environmentData{
					Type: "chain",
				}

				chain, ok := chains[as]
				if !ok {
					logrus.WithField("chain-name", as).Error("failed to resolve chain steps: step not found in registry")
					return ret
				}

				ret.Items = getEnvironmentDataItems(chain.Steps, refs, chains)
				return ret
			},
		})
}

func setWorkflowEnvironment(t *template.Template, refs registry.ReferenceByName, chains registry.ChainByName, workflows registry.WorkflowByName) *template.Template {
	return t.Funcs(
		template.FuncMap{
			"getEnvironment": func(as string) environmentData {
				ret := environmentData{
					Type: "workflow",
				}

				workflow, ok := workflows[as]
				if !ok {
					logrus.WithField("workflow-name", as).Error("failed to resolve workflow steps: workflow not found in registry")
					return ret
				}

				var worklist []api.TestStep
				for _, steps := range [][]api.TestStep{workflow.Pre, workflow.Test, workflow.Post} {
					worklist = append(worklist, steps...)
				}

				ret.Items = getEnvironmentDataItems(worklist, refs, chains)
				return ret
			},
		})
}

func setChainGraph(t *template.Template, chains registry.ChainByName) *template.Template {
	return t.Funcs(
		template.FuncMap{
			"chainGraph": func(as string) template.HTML {
				svg, err := ChainGraph(as, chains)
				if err != nil {
					return template.HTML(err.Error())
				}
				return template.HTML(svg)
			},
		})
}

func mainPageHandler(agent agents.RegistryAgent, templateString string, w http.ResponseWriter, _ *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Since(start)) }()

	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	refs, chains, workflows, docs, _ := agent.GetRegistryComponents()
	page, err := baseTemplate.Clone()
	if err != nil {
		writeErrorPage(w, err, http.StatusInternalServerError)
		return
	}
	page = setDocs(page, docs)
	page = setWorkflowGraph(page, chains, workflows)
	page = setChainGraph(page, chains)
	if page, err = page.Parse(templateString); err != nil {
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
		Workflows:  workflows,
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
		if len(splitURI) == 1 {
			switch splitURI[0] {
			case "":
				mainPageHandler(regAgent, mainPage, w, req)
			case "search":
				searchHandler(confAgent, w, req)
			case "job":
				jobHandler(regAgent, confAgent, w, req)
			case "ci-operator-reference":
				ciOpConfigRefHandler(w)
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

// multiLineHighlightJS allows to append -$NUMBER to one of the line anchors
// (e.G. `#line18-20`) and will then highlight all the lines.
const multiLineHighlightJS = `<script>
function getLinesToHighlight() {
  let result = [];
  let url = window.location.href;
  let anchorSplitUrl = url.split("#");
  // No results
  if (anchorSplitUrl.length < 2) {
    return result;
  }

  sourceDestSplit = anchorSplitUrl[1].split("-");
  result.push(sourceDestSplit[0]);

  // Only a start
  if (sourceDestSplit.length === 1) {
    return result;
  }

  let identifierNumberRe = /([a-z]*)(\d+)/;
  sourceMatches = identifierNumberRe.exec(sourceDestSplit[0]);
  if (sourceMatches.length != 3) {
    return result;
  }
  destMatches = identifierNumberRe.exec(sourceDestSplit[1]);
  if (destMatches.length != 3) {
    return result;
  }
  prefix = sourceMatches[1];
  startNumber = sourceMatches[2];
  endNumber = destMatches[2];

  result.pop();
  for (i = startNumber; i <= endNumber; i++){
    result.push(prefix + i);
  }

  return result;
}

function highlightLines() {

  toHighlight = getLinesToHighlight();
  if (toHighlight.length < 1){
    return;
  }

  // Prevent hotlooping, the location replacements triggers the script again.
  // * Case one: We redirected to set the anchor, local storage allows us to
  //   differentiate that from a user action.
  if (toHighlight.length < 2 && localStorage["highlightLines_redirect"] === true) {
    localStorage["highlightLines_redirect"] = false;
    return;
  };
  // * Case two: Local storage tells us a different incarnation of us already took care
  //   We can not reset the state store, because all mutations call us again. Our previous
  //   instance will do this after a sleep.
  if (window["localStorage"]["highlightLines"] === toHighlight.toString()){
    return;
  }

  // Reset everything, won't work if we don't have at least one line to highlight
  allLines = document.getElementsByClassName(document.getElementById(toHighlight[0]).className);
  for (line of allLines){
    element = document.getElementById(line.id);
    element.style.color = "";
    element.style.backgroundColor = "";
  }

  let color = "";
  let backgroundColor = "";
  originalURL = window.location.href;
  toHighlight.forEach(function(id){
    if (color === ""){
      localStorage["highlightLines_redirect"] = true;
      location.replace("#" + id);
      element = document.getElementById(id);
      targetClass = document.querySelector("." + element.className + ":target");
      style = getComputedStyle(targetClass);
      color = style.color;
      backgroundColor = style.backgroundColor;
    };

    element = document.getElementById(id);
    element.style.color = color;
    element.style.backgroundColor = backgroundColor;
  });

  window["localStorage"]["highlightLines"] = toHighlight.toString();
  location.replace(originalURL);

  // Sleep one second, then reset the state store
	window.setTimeout(function() {
    window.localStorage.highlightLines = null;
  }, 1000);
}

highlightLines();
window.onhashchange = highlightLines;
</script>
`

func syntax(source string, lexer chroma.Lexer) (string, error) {
	var output bytes.Buffer
	style := styles.Get("dracula")
	// highlighted lines based on linking currently require WithClasses to be used
	formatter := htmlformatter.New(htmlformatter.Standalone(false), htmlformatter.LinkableLineNumbers(true, "line"), htmlformatter.WithLineNumbers(true), htmlformatter.LineNumbersInTable(true), htmlformatter.WithClasses(true))
	iterator, err := lexer.Tokenise(nil, source)
	if err != nil {
		return "", fmt.Errorf("failed to tokenise source: %w", err)
	}
	output.WriteString("<style>")
	if err := formatter.WriteCSS(&output, style); err != nil {
		return "", fmt.Errorf("failed to write css: %w", err)
	}
	output.WriteString("</style>")
	if err := formatter.Format(&output, style, iterator); err != nil {
		return "", err
	}
	output.WriteString(multiLineHighlightJS)
	return output.String(), nil
}

func syntaxYAML(source string) (string, error) {
	return syntax(source, lexers.Get("yaml"))
}

func syntaxBash(source string) (string, error) {
	return syntax(source, lexers.Get("bash"))
}

func fromImage(name string, reference *api.ImageStreamTagReference) string {
	if reference != nil {
		return reference.ISTagName()
	}
	return name
}

const (
	fromDocumentation      = "https://docs.ci.openshift.org/architecture/step-registry/#referencing-another-configured-image"
	fromImageDocumentation = "https://docs.ci.openshift.org/architecture/step-registry/#referencing-a-literal-image"
)

func fromImageDescription(name string, reference *api.ImageStreamTagReference) template.HTML {
	prefix := fmt.Sprintf("<span style=\"font-family:monospace\">%s</span> resolves to an ", fromImage(name, reference))
	if reference != nil {
		return template.HTML(fmt.Sprintf("%s image imported from the specified imagestream tag on the build farm (<a href=\"%s\">documentation</a>).", prefix, fromImageDocumentation))
	}
	return template.HTML(fmt.Sprintf("%s image built or imported by the ci-operator configuration (<a href=\"%s\">documentation</a>).", prefix, fromDocumentation))
}

func referenceHandler(agent agents.RegistryAgent, w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Since(start)) }()
	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	name := path.Base(req.URL.Path)
	page, err := baseTemplate.Clone()
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %w", err), http.StatusInternalServerError)
		return
	}
	page, err = page.Funcs(
		template.FuncMap{
			"syntaxedSource": func(source string) template.HTML {
				formatted, err := syntaxBash(source)
				if err != nil {
					logrus.Errorf("Failed to format source file: %v", err)
					return template.HTML(source)
				}
				return template.HTML(formatted)
			},
			"githubLink":           githubLink,
			"ownersBlock":          ownersBlock,
			"fromImage":            fromImage,
			"fromImageDescription": fromImageDescription,
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
				As:                name,
				Commands:          refs[name].Commands,
				From:              refs[name].From,
				FromImage:         refs[name].FromImage,
				Dependencies:      refs[name].Dependencies,
				Environment:       refs[name].Environment,
				Leases:            refs[name].Leases,
				Timeout:           refs[name].Timeout,
				GracePeriod:       refs[name].GracePeriod,
				Resources:         refs[name].Resources,
				OptionalOnSuccess: refs[name].OptionalOnSuccess,
				BestEffort:        refs[name].BestEffort,
				Cli:               refs[name].Cli,
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

	refs, chains, _, docs, metadata := agent.GetRegistryComponents()
	page, err := baseTemplate.Clone()
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %w", err), http.StatusInternalServerError)
		return
	}
	page = setDocs(page, docs)
	page = setChainGraph(page, chains)
	page = setChainDependencies(page, refs, chains)
	page = setChainEnvironment(page, refs, chains)
	if page, err = page.Parse(chainPage); err != nil {
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

	refs, chains, workflows, docs, metadata := agent.GetRegistryComponents()
	page, err := baseTemplate.Clone()
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %w", err), http.StatusInternalServerError)
		return
	}

	page = setDocs(page, docs)
	page = setWorkflowGraph(page, chains, workflows)
	page = setChainGraph(page, chains)
	page = setWorkflowDependencies(page, refs, chains, workflows)
	page = setWorkflowEnvironment(page, refs, chains, workflows)

	if page, err = page.Parse(workflowJobPage); err != nil {
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

func jobHandler(regAgent agents.RegistryAgent, confAgent agents.ConfigAgent, w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { logrus.Infof("rendered in %s", time.Since(start)) }()
	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	metadata, err := registryserver.MetadataFromQuery(w, r)
	if err != nil {
		return
	}
	test := r.URL.Query().Get(TestQuery)
	if test == "" {
		registryserver.MissingQuery(w, TestQuery)
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

	page, err := baseTemplate.Clone()
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %w", err), http.StatusInternalServerError)
		return
	}
	page = setDocs(page, docs)
	page = setWorkflowGraph(page, chains, workflows)
	page = setChainGraph(page, chains)

	if page, err = page.Parse(workflowJobPage); err != nil {
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
	page, err := baseTemplate.Clone()
	if err != nil {
		writeErrorPage(w, fmt.Errorf("Failed to render page: %w", err), http.StatusInternalServerError)
		return
	}

	if page, err = page.Parse(jobSearchPage); err != nil {
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

func ciOpConfigRefHandler(w http.ResponseWriter) {
	if _, err := w.Write(ciOperatorRefRendered); err != nil {
		logrus.WithError(err).Error("Failed to write ci-operator config")
	}
}

func markDowner(args ...interface{}) template.HTML {
	s := blackfriday.Run([]byte(fmt.Sprintf("%s", args...)))
	return template.HTML(s)
}
