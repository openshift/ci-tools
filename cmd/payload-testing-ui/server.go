package main

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	prpqv1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/html"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

const (
	aggregatorPrefix = "aggregator-"

	runsURL   = "/runs/"
	docURL    = "https://docs.ci.openshift.org/release-oversight/payload-testing/"
	bodyStart = `
<nav class="navbar navbar-expand-lg navbar-light bg-light">
<a class="navbar-brand" href=` + runsURL + `>Pull Request Payload Qualification Runs</a>
<button class="navbar-toggler" type="button" data-toggle="collapse" data-target="#navbarSupportedContent" aria-controls="navbarSupportedContent" aria-expanded="false" aria-label="Toggle navigation">
	<span class="navbar-toggler-icon"></span>
</button>
<div class="collapse navbar-collapse" id="navbarSupportedContent">
	<ul class="navbar-nav mr-auto">
	<li class="nav-item">
		<a class="nav-link" href=` + docURL + ` target="_blank">Documentation</a>
	</li>
	<li class="nav-item">
	<a class="nav-link" href=` + runsURL + `>Runs</a>
	</li>
	</ul>
</div>
</nav>
<div class="container">`
	pageEnd = `
  <p class="small">Source code for this page located on <a href="https://github.com/openshift/ci-tools">GitHub</a></p>
</div>`
	runsListTitle    = "Pull Request Payload Qualification Runs"
	runsListTemplate = `
<h1>Pull Request Payload Qualification Runs</h1>
{{ len .Items }} run(s)
<table class="table">
	<thead>
		<tr>
			<th title="The name of the Pull Request Payload Qualification Run" class="info">Name</th>
			<th title="The repository of each pull request" class="info">Repositories</th>
			<th title="The number and name of each pull request" class="info">Pull Requests</th>
		</tr>
	</thead>
	<tbody>
		{{ range .Items }}
		<tr>
			<td>
			{{ with .ObjectMeta }}
				{{ with $url := printf "%s/%s" .Namespace .Name }}
				<a class="text-nowrap" href="` + runsURL + `{{ $url }}">{{ $url }}</a>
				{{ end }}
			{{ end }}
			</td>
			<td>
				<ul>
			{{ range $i, $pullRequest := .Spec.PullRequests }}
				<li style="list-style:none; padding:">{{ repoLink $pullRequest.Org $pullRequest.Repo }}</li>
			{{ end }}
				</ul>
			</td>
			<td>
				{{ range $i, $pullRequest := .Spec.PullRequests }}
					{{ if .PullRequest }}
						<li style="list-style:none; padding:">{{ prLink . }} by {{ authorLink $pullRequest.PullRequest.Author }}</li>
					{{ else }}
						<li style="list-style:none; padding:">{{ refLink . .BaseRef }} ({{ shaLink . .BaseSHA }})</li>
					{{ end }}
				{{ end }}
			</td>
		</tr>
		{{ end }}
	</tbody>
</table>
`
	runTitle    = "Pull Request Payload Qualification Run - %s"
	runTemplate = `
<h1>{{ .ObjectMeta.Namespace }}/{{ .ObjectMeta.Name }}</h1>

Created: {{ .ObjectMeta.CreationTimestamp }} 

{{ with .Spec }}

<h2>Sources</h2>
<ul>
  {{ range $i, $pullRequest := .PullRequests }}
    {{ if .PullRequest }}
		{{ prLink . }} by {{ authorLink .PullRequest.Author }}
		<li style="list-style:none; padding:">
		  <ul>
			<li>Repository: {{ repoLink .Org .Repo }}</li>
			<li>SHA: <tt>{{ shaLink . .PullRequest.SHA }}</tt></li>
			<li>
				Base: <tt>{{ refLink . .BaseRef }}</tt> (<tt>{{ shaLink . .BaseSHA }}</tt>)
			</li>
		  </ul>
		</li>
    {{ else }}
		Sourced From:
		<li style="list-style:none; padding:">
		  <ul>
			<li>Repository: {{ repoLink .Org .Repo }}</li>
			<li>
				Base: <tt>{{ refLink . .BaseRef }}</tt> (<tt>{{ shaLink . .BaseSHA }}</tt>)
			</li>
		  </ul>
		</li>
    {{ end }}
  {{ end }}
</ul>

{{ with .Jobs }}

<h2>Release controller configuration</h2>
{{ with .ReleaseControllerConfig }}
<ul>
  <li>OCP version: {{ .OCP }}</li>
  <li>Release: {{ .Release }}</li>
  <li>Specifier: {{ .Specifier }}</li>
  {{ with .Revision }}<li>Revision: {{ . }}</li>{{ end }}
</ul>
{{ configLink . }}
{{ end }}

<h2>Jobs</h2>
<ul>
  {{ range $i, $job := .Jobs }}
  <li>
    <tt>
      {{ with $status := jobStatus $i }}
        <span class="{{ jobClass $status }}">{{ jobText $job }}</span>:
        {{ if $status.Status.URL }}
          <a href="{{ $status.Status.URL }}">{{ $status.ProwJob }}</a>
        {{ else }}
          {{ $status.ProwJob }}
        {{ end }}
      {{ else }}
        {{ jobText $job }}
      {{ end }}
    </tt>
  </li>
  {{ end }}
</ul>

{{ end }}{{/* with .Jobs */}}

{{ end }}{{/* with .Spec */}}

<h2>Status</h2> {{ with .Status }}

<ul>
{{ range .Conditions }}
  <li>
    <span {{ if ne .Status "True" }}class="text-danger"{{ end }}>
      {{ .LastTransitionTime }}: {{ .Type }}: {{ .Reason }}: {{ .Message }}
    </span>
  </li>
{{ end }}
</ul>

{{ end }}{{/* with .Status */}}
`
)

type server struct {
	client           ctrlruntimeclient.Client
	ctx              context.Context
	namespace        string
	runsListTemplate *template.Template
}

func prLink(pr *prpqv1.PullRequestUnderTest) template.HTML {
	org := template.HTMLEscapeString(pr.Org)
	repo := template.HTMLEscapeString(pr.Repo)
	title := template.HTMLEscapeString(pr.PullRequest.Title)
	n := pr.PullRequest.Number
	ret := fmt.Sprintf(`<a href="http://github.com/%s/%s/pull/%d">#%d: %s</a>`,
		org, repo, n, n, title)
	return template.HTML(ret)
}

func authorLink(a string) template.HTML {
	a = template.HTMLEscapeString(a)
	ret := fmt.Sprintf(`<a href="https://github.com/%s">%s</a>`, a, a)
	return template.HTML(ret)
}

func repoLink(org string, repo string) template.HTML {
	org = template.HTMLEscapeString(org)
	repo = template.HTMLEscapeString(repo)
	ret := fmt.Sprintf(`<a href="https://github.com/%s/%s">%s/%s</a>`, org, repo, org, repo)
	return template.HTML(ret)
}

func refLink(pr *prpqv1.PullRequestUnderTest, r string) template.HTML {
	r = template.HTMLEscapeString(r)
	org := template.HTMLEscapeString(pr.Org)
	repo := template.HTMLEscapeString(pr.Repo)
	ret := fmt.Sprintf(`<a href="https://github.com/%s/%s/tree/%s">%s</a>`, org, repo, r, r)
	return template.HTML(ret)
}

func shaLink(pr *prpqv1.PullRequestUnderTest, h string) template.HTML {
	h = template.HTMLEscapeString(h)
	org := template.HTMLEscapeString(pr.Org)
	repo := template.HTMLEscapeString(pr.Repo)
	ret := fmt.Sprintf(`<a href="https://github.com/%s/%s/commit/%s">%s</a>`, org, repo, h, h)
	return template.HTML(ret)
}

func newServer(client ctrlruntimeclient.Client, ctx context.Context, namespace string) (server, error) {
	runsListTemplate, err := template.New("runsListTemplate").Funcs(template.FuncMap{
		"prLink":     prLink,
		"authorLink": authorLink,
		"repoLink":   repoLink,
		"refLink":    refLink,
		"shaLink":    shaLink,
	}).Parse(runsListTemplate)

	if err != nil {
		return server{}, err
	}
	return server{
		client:           client,
		ctx:              ctx,
		namespace:        namespace,
		runsListTemplate: runsListTemplate,
	}, nil
}

func (s *server) RunsList() http.HandlerFunc {
	return methodWrapper("GET", func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.URL.Path, runsURL) == "" {
			s.runList(w)
		} else {
			s.runDetails(w, r)
		}
	})
}

func (s *server) runList(w http.ResponseWriter) {
	var l prpqv1.PullRequestPayloadQualificationRunList
	opt := ctrlruntimeclient.ListOptions{Namespace: s.namespace}
	if err := s.client.List(s.ctx, &l, &opt); err != nil {
		logrus.WithError(err).Error("failed to list runs")
		writeStatus(w, http.StatusInternalServerError)
		return
	}
	if err := html.WritePage(w, runsListTitle, bodyStart, pageEnd, s.runsListTemplate, l); err != nil {
		logrus.WithError(err).Error("failed to write page")
		writeStatus(w, http.StatusNotImplemented)
	}
}

func (s *server) runDetails(w http.ResponseWriter, r *http.Request) {
	key := keyFromPath(strings.TrimPrefix(r.URL.Path, runsURL))
	if key.Name == "" {
		logrus.Debugf("run %q not found", key.Name)
		writeStatus(w, http.StatusNotFound)
		return
	}
	var run prpqv1.PullRequestPayloadQualificationRun
	if err := s.client.Get(s.ctx, key, &run); err != nil {
		if kerrors.IsNotFound(err) {
			logrus.Debugf("run %q not found", key.Name)
			writeStatus(w, http.StatusNotFound)
		} else {
			logrus.WithError(err).Errorf("failed to get run %q", key.Name)
			writeStatus(w, http.StatusInternalServerError)
		}
		return
	}
	title := fmt.Sprintf(runTitle, run.ObjectMeta.Name)
	status := make([]*prpqv1.PullRequestPayloadJobStatus, 0, len(run.Spec.Jobs.Jobs))
	for _, j := range run.Spec.Jobs.Jobs {
		name := j.JobName(jobconfig.PeriodicPrefix)
		var match *prpqv1.PullRequestPayloadJobStatus
		for i, s := range run.Status.Jobs {
			if strings.TrimPrefix(s.ReleaseJobName, aggregatorPrefix) == name {
				match = &run.Status.Jobs[i]
				break
			}
		}
		status = append(status, match)
	}
	tmpl := template.New("runTemplate")
	tmpl.Funcs(template.FuncMap{
		"prLink":     prLink,
		"authorLink": authorLink,
		"repoLink":   repoLink,
		"refLink":    refLink,
		"shaLink":    shaLink,
		"configLink": func(config *prpqv1.ReleaseControllerConfig) template.HTML {
			ocp := template.HTMLEscapeString(config.OCP)
			release := template.HTMLEscapeString(config.Release)
			suffix := ""
			if release == "ci" {
				suffix = "-ci"
			}
			ret := fmt.Sprintf(`
			<h2>Release controller</h2>
			<ul>
				<li><a href="https://amd64.ocp.releases.ci.openshift.org/#%s.0-0.%s">Release status</a></li>
				<li><a href="https://github.com/openshift/release/blob/main/core-services/release-controller/_releases/release-ocp-%s%s.json">Configuration</a></li>
			</ul>`, ocp, release, ocp, suffix)
			return template.HTML(ret)
		},
		"jobStatus": func(i int) *prpqv1.PullRequestPayloadJobStatus {
			return status[i]
		},
		"jobClass": func(s *prpqv1.PullRequestPayloadJobStatus) string {
			switch s.Status.State {
			case prowv1.SuccessState:
				return "text-success"
			case prowv1.FailureState:
				return "text-danger"
			case prowv1.AbortedState:
				return "text-warning"
			default:
				return ""
			}
		},
		"jobText": func(s *prpqv1.ReleaseJobSpec) string {
			return s.JobName(jobconfig.PeriodicPrefix)
		},
	})
	if _, err := tmpl.Parse(runTemplate); err != nil {
		logrus.WithError(err).Errorf("failed to parse template")
		writeStatus(w, http.StatusInternalServerError)
		return
	}
	if err := html.WritePage(w, title, bodyStart, pageEnd, tmpl, &run); err != nil {
		logrus.WithError(err).Errorf("failed to write page")
		writeStatus(w, http.StatusInternalServerError)
		return
	}
}

func keyFromPath(path string) ctrlruntimeclient.ObjectKey {
	i := strings.Index(path, "/")
	if i == -1 {
		return ctrlruntimeclient.ObjectKey{}
	}
	return ctrlruntimeclient.ObjectKey{
		Namespace: path[:i],
		Name:      path[i+1:],
	}
}

func writeStatus(w http.ResponseWriter, s int) {
	t := http.StatusText(s)
	w.WriteHeader(s)
	if _, err := w.Write([]byte(t)); err != nil {
		logrus.WithError(err).Errorf("failed to write %q response", t)
	}
}

func methodWrapper(m string, f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != m {
			writeStatus(w, http.StatusNotImplemented)
			return
		}
		f(w, r)
	}
}
