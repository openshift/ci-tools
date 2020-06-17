package backporter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"text/template"

	"k8s.io/test-infra/prow/bugzilla"
)

const (
	//BugIDQuery stores the query for bug ID
	BugIDQuery = "ID"
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
  <a class="navbar-brand" href="/">Bugzilla Backporter</a>
  <button class="navbar-toggler" type="button" data-toggle="collapse" data-target="#navbarSupportedContent" aria-controls="navbarSupportedContent" aria-expanded="false" aria-label="Toggle navigation">
    <span class="navbar-toggler-icon"></span>
  </button>

  <div class="collapse navbar-collapse" id="navbarSupportedContent">
    <form class="form-inline my-2 my-lg-0" role="search" action="/getclones" method="get">
      <input class="form-control mr-sm-2" type="search" placeholder="Bug ID" aria-label="Search" name="ID">
      <button class="btn btn-outline-success my-2 my-sm-0" type="submit">Find Clones</button>
    </form>
  </div>
</nav>
<div class="container">
`

const htmlPageEnd = `
</div>
<footer>
<p class="small">Source code for this page located on <a href="https://github.com/openshift/ci-tools">GitHub</a></p>
</footer>
</body>
</html>
`

const emptyTemplateConstructor = `
{{.}}
`

const clonesTemplateConstructor = `
	<h2 id="bugid"> <a href = "#bugid"> {{.Bug.ID}}: {{.Bug.Summary}} </a> | Status: {{.Bug.Status}} </h2>
	<p> Target Release: {{ .Bug.TargetRelease }} </p>
	{{ if .PRs }}
		<p> GitHub PR: 
		{{ if .PRs }}
			{{ range $index, $pr := .PRs }}
				{{ if $index}}|{{end}} 
				<a href={{ $pr.Type.URL }}/{{ $pr.Org}}/{{ $pr.Repo}}/pull/{{ $pr.Num}}>{{ $pr.Org}}/{{ $pr.Repo}}#{{ $pr.Num}}</a>
			{{ end }}
		{{ end }}
		</p>
	{{ else }}
		<p> No linked PRs! </p>
	{{ end }}
	{{ if ne .Parent.ID .Bug.ID}}
		<p> Cloned From: <a href = /getclones?ID={{.Parent.ID}}> Bug {{.Parent.ID}}: {{.Parent.Summary}}</a> | Status: {{.Parent.Status}}
	{{ else }}
		<p> Cloned From: This is the original! </p>
	{{ end }}
	<h4 id="clones"> <a href ="#clones"> Clones</a> </h4>
	<table class="table">
		<thead>
			<tr>
				<th title="Targeted version to release fix" class="info">Target Release</th>
				<th title="ID of the cloned bug" class="info">Bug ID</th>
				<th title="Status of the cloned bug" class="info">Status</th>
				<th title="PR associated with this bug" class="info">PR</th>
			</tr>
		</thead>
		<tbody>
		{{ if .Clones }}
			{{ range $clone := .Clones }}
				<tr>
					<td style="vertical-align: middle;">{{ $clone.TargetRelease }}</td>
					<td style="vertical-align: middle;"><a href = /getclones?ID={{$clone.ID}}>{{ $clone.ID }}</a></td>
					<td style="vertical-align: middle;">{{ $clone.Status }}</td>
					<td style="vertical-align: middle;">
						{{range $index, $pr := $clone.PRs }}
							{{ if $index}},{{end}}
							<a href = {{ $pr.Type.URL }}/{{$pr.Org}}/{{$pr.Repo}}/pull/{{$pr.Num}} target="_blank"> {{$pr.Org}}/{{$pr.Repo}}#{{$pr.Num}}</a>
						{{end}}
					</td>
				</tr>
			{{ end }}
		{{ else }}
			<tr> <td colspan=4 style="text-align:center;"> No clones found! </td></tr>
		{{ end }}
		</tbody>
	</table>`

var (
	clonesTemplate = template.Must(template.New("clones").Parse(clonesTemplateConstructor))
	emptyTemplate  = template.Must(template.New("empty").Parse(emptyTemplateConstructor))
)

// HandlerFuncWithErrorReturn allows returning errors to be logged
type HandlerFuncWithErrorReturn func(http.ResponseWriter, *http.Request) error

type wrapper struct {
	Bug    *bugzilla.Bug
	Clones []*bugzilla.Bug
	Parent *bugzilla.Bug
	PRs    []bugzilla.ExternalBug
}

//Writes an HTML page, prepends header in htmlPageStart and appends header from htmlPageEnd around tConstructor.
func writePage(w http.ResponseWriter, title string, body *template.Template, data interface{}) error {
	fmt.Fprintf(w, htmlPageStart, title)

	if err := body.Execute(w, data); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s: %v", http.StatusText(http.StatusInternalServerError), err)
		return err
	}
	fmt.Fprint(w, htmlPageEnd)
	return nil
}

// GetLandingHandler will return a simple bug search page
func GetLandingHandler() HandlerFuncWithErrorReturn {
	return func(w http.ResponseWriter, req *http.Request) error {
		writePage(w, "Home", emptyTemplate, nil)
		return nil
	}
}

// GetBugHandler returns a function which populates the response with the details of the bug
// Returns bug details in JSON format
func GetBugHandler(client bugzilla.Client) HandlerFuncWithErrorReturn {
	return func(w http.ResponseWriter, r *http.Request) error {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusNotImplemented)
			w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
			return fmt.Errorf("Not a GET request")
		}
		bugIDStr := r.URL.Query().Get(BugIDQuery)
		bugID, err := strconv.Atoi(bugIDStr)
		if bugIDStr == "" || err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "%s query missing or incorrect", BugIDQuery)
			return err
		}
		bugInfo, err := client.GetBug(bugID)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Bug ID not found"))
			return err
		}
		jsonBugInfo, err := json.MarshalIndent(*bugInfo, "", "  ")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "failed to marshal bugInfo to JSON: %v", err)
			// logger.WithError(err).Errorf("failed to marshal bugInfo to JSON")
			return err
		}
		w.WriteHeader(http.StatusOK)
		w.Write(jsonBugInfo)
		return nil
	}
}

// GetClonesHandler returns an HTML page with detais about the bug and its clones
func GetClonesHandler(client bugzilla.Client) HandlerFuncWithErrorReturn {
	return func(w http.ResponseWriter, req *http.Request) error {
		bugIDStr := req.URL.Query().Get(BugIDQuery)
		bugID, err := strconv.Atoi(bugIDStr)
		if bugIDStr == "" || err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writePage(w, "Error!", emptyTemplate, fmt.Sprintf("%s - query incorrect", BugIDQuery))
			return err
		}
		bug, err := client.GetBug(bugID)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			writePage(w, "Not Found", emptyTemplate, fmt.Sprintf("Bug#%d not found", bugID))
			return err
		}
		prs, err := client.GetExternalBugPRsOnBug(bugID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writePage(w, "Error!", emptyTemplate, fmt.Sprintf("Bug#%d - error occured while retreiving list of PRs : %v", bugID, err))
			return err
		}
		parent, err := client.GetRootForClone(bug)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writePage(w, "Error!", emptyTemplate, fmt.Sprintf("Bug#%d Details of parent could not be retrieved : %v", bugID, err))
			return err
		}

		clones, err := client.GetAllClones(bug)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writePage(w, "Not Found", emptyTemplate, err.Error())
			return err
		}
		for _, clone := range clones {
			clonePRs, err := client.GetExternalBugPRsOnBug(clone.ID)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				writePage(w, "Error!", emptyTemplate, fmt.Sprintf("Bug#%d - error occured while retreiving list of PRs : %v", clone.ID, err))
				return err
			}
			clone.PRs = clonePRs
		}
		wrpr := wrapper{
			Bug:    bug,
			Clones: clones,
			Parent: parent,
			PRs:    prs,
		}

		writePage(w, "Clones", clonesTemplate, wrpr)
		return nil
	}
}
