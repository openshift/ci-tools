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

	prpqv1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/html"
)

const (
	runsURL   = "/runs/"
	bodyStart = `
<div class="container">`
	pageEnd = `
  <p class="small">Source code for this page located on <a href="https://github.com/openshift/ci-tools">GitHub</a></p>
</div>`
	runsListTitle    = "Pull Request Payload Qualification Runs"
	runsListTemplate = `
<h1>Pull Request Payload Qualification Runs</h1>
{{ len .Items }} run(s)
<ul>
{{ range .Items }}
  <li>
    {{ with .ObjectMeta }}
      {{ with $url := printf "%s/%s" .Namespace .Name }}
        <a href="` + runsURL + `{{ $url }}">{{ $url }}</a>
      {{ end }}
    {{ end }}
  </li>
{{ end }}
</ul>
`
	runTitle    = "Pull Request Payload Qualification Run - %s"
	runTemplate = `
<h1>{{ .ObjectMeta.Namespace }}/{{ .ObjectMeta.Name }}</h1>

Created: {{ .ObjectMeta.CreationTimestamp }}

{{ with .Spec }}

<h2>Pull request</h2>

{{ with .PullRequest }}
{{ prLink . }}
<ul>
    <li>Author: {{ authorLink .PullRequest.Author }}</li>
    <li>SHA: <tt>{{ shaLink . .PullRequest.SHA }}</tt></li>
  <li>
    Base: <tt>{{ refLink . .BaseRef }}</tt> (<tt>{{ shaLink . .BaseSHA }}</tt>)
  </li>
</ul>
{{ end }}{{/* with .PullRequest */}}

{{ with .Jobs }}

<h2>Release controller configuration</h2>
{{ with .ReleaseControllerConfig }}
<ul>
  <li>OCP version: {{ .OCP }}</li>
  <li>Release: {{ .Release }}</li>
  <li>Specifier: {{ .Specifier }}</li>
  {{ with .Revision }}<li>Revision: {{ . }}</li>{{ end }}
{{ end }}
</ul>

<h2>Jobs</h2>
<ul>
  {{ range $i, $job := .Jobs }}
  <li>
    <tt>
      {{ with .CIOperatorConfig -}}
        {{ .Org }}/{{ .Repo }}@{{ .Branch -}}
        {{- with .Variant }}__{{ . }}{{ end }}
      {{- end }}:{{ .Test }}
    </tt>
  </li>
  {{ end }}
</ul>

{{ end }}{{/* with .Jobs */}}

{{ end }}{{/* with .Spec */}}

<h2>Status</h2> {{ with .Status }}

Jobs:

<ul>
  {{ range .Jobs }}
  <li>
      Prow job: <a href="{{ .Status.URL }}">{{ .ProwJob }}</a><br/>
      {{ with .Status }}
      Description: {{ .Description }}<br/>
      State: {{ .State }}<br/>
      Started: {{ .StartTime }}<br/>
      Completed: {{ with .CompletionTime }}{{ . }}{{ end }}<br/>
      Pod: {{ .PodName }}<br/>
      {{ end }}
  </li>
  {{ end }}
</ul>

Conditions:
<ul>
{{ range .Conditions }}
  <li>
    Type: {{ .Type }}<br/>
    Status: {{ .Status }}<br/>
    ObservedGeneration: {{ .ObservedGeneration }}<br/>
    LastTransitionTime: {{ .LastTransitionTime }}<br/>
    Reason: {{ .Reason }}<br/>
    Message: {{ .Message }}<br/>
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

func newServer(client ctrlruntimeclient.Client, ctx context.Context, namespace string) (server, error) {
	runsListTemplate, err := template.New("runsListTemplate").Parse(runsListTemplate)
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
	tmpl := template.New("runTemplate")
	tmpl.Funcs(template.FuncMap{
		"prLink": func(pr *prpqv1.PullRequestUnderTest) template.HTML {
			org := template.HTMLEscapeString(pr.Org)
			repo := template.HTMLEscapeString(pr.Repo)
			title := template.HTMLEscapeString(pr.PullRequest.Title)
			n := pr.PullRequest.Number
			ret := fmt.Sprintf(`<a href="http://github.com/%s/%s/pull/%d">%s</a>`, org, repo, n, title)
			return template.HTML(ret)
		},
		"authorLink": func(a string) template.HTML {
			a = template.HTMLEscapeString(a)
			ret := fmt.Sprintf(`<a href="https://github.com/%s">%s</a>`, a, a)
			return template.HTML(ret)
		},
		"refLink": func(pr *prpqv1.PullRequestUnderTest, r string) template.HTML {
			r = template.HTMLEscapeString(r)
			org := template.HTMLEscapeString(pr.Org)
			repo := template.HTMLEscapeString(pr.Repo)
			ret := fmt.Sprintf(`<a href="https://github.com/%s/%s/tree/%s">%s</a>`, org, repo, r, r)
			return template.HTML(ret)
		},
		"shaLink": func(pr *prpqv1.PullRequestUnderTest, h string) template.HTML {
			h = template.HTMLEscapeString(h)
			org := template.HTMLEscapeString(pr.Org)
			repo := template.HTMLEscapeString(pr.Repo)
			ret := fmt.Sprintf(`<a href="https://github.com/%s/%s/commit/%s">%s</a>`, org, repo, h, h)
			return template.HTML(ret)
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
