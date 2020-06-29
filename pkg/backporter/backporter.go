package backporter

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/bugzilla"
)

const (
	// BugIDQuery stores the query for bug ID
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
select:invalid { color: gray; }
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
    <form class="form-inline my-2 my-lg-0 needs-validation" role="search" action="/clones" method="get">
	  <input class="form-control mr-sm-2" type="text" placeholder="Bug ID" aria-label="Search" name="ID" required>
      <button class="btn btn-outline-success my-2 my-sm-0" type="submit">Find Clones</button>
    </form>
  </div>
</nav>
`

const htmlPageEnd = `
<footer>
<p class="small">Source code for this page located on <a href="https://github.com/openshift/ci-tools">GitHub</a></p>
</footer>
</body>
</html>
`

const clonesTemplateConstructor = `
{{if .NewCloneID }}
	<div class="alert alert-success alert-dismissible" id="success-banner">
	<a href="#" class="close" data-dismiss="alert" aria-label="close">&times;</a>
	<strong>Success!</strong> Clone created - <a href="/clones?ID={{ .NewCloneID }}" >Bug#{{ .NewCloneID }}</a>.
	</div>
{{ end }}
<div class="container">
	<h2> <a href = "#bugid" id="bugid" value="{{.Bug.ID}}"> {{.Bug.ID}}: {{.Bug.Summary}} </a> | Status: {{.Bug.Status}} </h2>
	<p><label> Target Release:</label> {{ .Bug.TargetRelease }} </p>
	{{ if .PRs }}
		<p> <label>GitHub PR: </label>
		{{ if .PRs }}
			{{ range $index, $pr := .PRs }}
				{{ if $index}}|{{end}} 
				<a href="{{ $pr.Type.URL }}/{{ $pr.Org}}/{{ $pr.Repo}}/pull/{{ $pr.Num}}">{{ $pr.Org}}/{{ $pr.Repo}}#{{ $pr.Num}}</a>
			{{ end }}
		{{ end }}
		</p>
	{{ else }}
		<p> No linked PRs. </p>
	{{ end }}
	{{ if ne .Parent.ID .Bug.ID}}
		<p> <label>Cloned From: </label><a href = "/clones?ID={{.Parent.ID}}"> Bug {{.Parent.ID}}: {{.Parent.Summary}}</a> | Status: {{.Parent.Status}}
	{{ else }}
		<p> <label>Cloned From: </label>This is the original. </p>
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
					<td style="vertical-align: middle;"><a href = "/clones?ID={{$clone.ID}}">{{ $clone.ID }}</a></td>
					<td style="vertical-align: middle;">{{ $clone.Status }}</td>
					<td style="vertical-align: middle;">
						{{range $index, $pr := $clone.PRs }}
							{{ if $index}},{{end}}
							<a href = "{{ $pr.Type.URL }}/{{$pr.Org}}/{{$pr.Repo}}/pull/{{$pr.Num}}" target="_blank"> {{$pr.Org}}/{{$pr.Repo}}#{{$pr.Num}}</a>
						{{end}}
					</td>
				</tr>
			{{ end }}
		{{ else }}
			<tr> <td colspan=4 style="text-align:center;"> No clones found. </td></tr>
		{{ end }}
		</tbody>
	</table>
	<form class="form-inline my-2 my-lg-0" role="search" action="/clones" method="post">
		<input type="hidden" name="ID" value="{{.Bug.ID}}">
		<select class="form-control mr-sm-2" aria-label="Search" name="release" id="target_version" required>
			<option value="" disabled selected hidden>Target Version</option>
			{{ range $release := .CloneTargets }}
				<option value="{{$release}}" id="opt_{{$release}}">{{$release}}</option>
			{{end}}
		</select>
		<button class="btn btn-outline-success my-2 my-sm-0" type="submit">Create Clone</button>
    </form>
</div>`

const errorTemplateConstructor = `
<div class="alert alert-danger" id="error-banner">
<a href="#" class="close" data-dismiss="alert" aria-label="close">&times;</a>
<strong>Error. </strong> <label id ="error-text">{{.}}</label>
</div>`

var (
	clonesTemplate = template.Must(template.New("clones").Parse(clonesTemplateConstructor))
	emptyTemplate  = template.Must(template.New("empty").Parse("{{.}}"))
	errorTemplate  = template.Must(template.New("error").Parse(errorTemplateConstructor))
)

// HandlerFuncWithErrorReturn allows returning errors to be logged
type HandlerFuncWithErrorReturn func(http.ResponseWriter, *http.Request) error

// ClonesTemplateData holds the UI data for the clones page
type ClonesTemplateData struct {
	Bug          *bugzilla.Bug          // bug details
	Clones       []*bugzilla.Bug        // List of clones for the bug
	Parent       *bugzilla.Bug          // Root bug if it is a a bug, otherwise holds itself
	PRs          []bugzilla.ExternalBug // Details of linked PR
	CloneTargets []string
	NewCloneID   int
}

// Writes an HTML page, prepends header in htmlPageStart and appends header from htmlPageEnd around tConstructor.
func writePage(w http.ResponseWriter, title string, body *template.Template, data interface{}) error {
	_, err := fmt.Fprintf(w, htmlPageStart, title)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, fprintfErr := fmt.Fprintf(w, "%s: %v", http.StatusText(http.StatusInternalServerError), err)
		if fprintfErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return fprintfErr
		}
		return err
	}
	if err := body.Execute(w, data); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, fprintfErr := fmt.Fprintf(w, "%s: %v", http.StatusText(http.StatusInternalServerError), err)
		if fprintfErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return fprintfErr
		}
		return err
	}
	_, fprintfErr := fmt.Fprint(w, htmlPageEnd)
	if fprintfErr != nil {
		http.Error(w, "Error building page.", http.StatusInternalServerError)
		return fprintfErr
	}
	return nil
}

// GetLandingHandler will return a simple bug search page
func GetLandingHandler() HandlerFuncWithErrorReturn {
	return func(w http.ResponseWriter, req *http.Request) error {
		err := writePage(w, "Home", emptyTemplate, nil)
		return err
	}
}

// GetBugHandler returns a function with bug details  in JSON format
func GetBugHandler(client bugzilla.Client) HandlerFuncWithErrorReturn {
	return func(w http.ResponseWriter, r *http.Request) error {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusBadRequest)
			_, writeErr := w.Write([]byte(http.StatusText(http.StatusBadRequest)))
			if writeErr != nil {
				http.Error(w, "Error while building page", http.StatusInternalServerError)
				return writeErr
			}
			return fmt.Errorf("Not a GET request")
		}
		bugIDStr := r.URL.Query().Get(BugIDQuery)
		if bugIDStr == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, fprintfErr := fmt.Fprintf(w, "%s query missing or incorrect", BugIDQuery)
			if fprintfErr != nil {
				return fprintfErr
			}
			return fmt.Errorf("%s query missing or incorrect", BugIDQuery)
		}
		bugID, err := strconv.Atoi(bugIDStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, fprintfErr := fmt.Fprintf(w, "%s query incorrect: %v", BugIDQuery, err)
			if fprintfErr != nil {
				return fprintfErr
			}
			return err
		}
		bugInfo, err := client.GetBug(bugID)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			_, writeErr := w.Write([]byte("Bug ID not found"))
			if writeErr != nil {
				http.Error(w, "Error while building page", http.StatusInternalServerError)
				return writeErr
			}
			return err
		}
		jsonBugInfo, err := json.MarshalIndent(*bugInfo, "", "  ")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, fprintfErr := fmt.Fprintf(w, "failed to marshal bugInfo to JSON: %v", err)
			if fprintfErr != nil {
				http.Error(w, "Error building page.", http.StatusInternalServerError)
				return fprintfErr
			}
			// logger.WithError(err).Errorf("failed to marshal bugInfo to JSON")
			return err
		}
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(jsonBugInfo)
		if err != nil {
			http.Error(w, "Error while building page", http.StatusInternalServerError)
			return err
		}
		return nil
	}
}

func getClonesTemplateData(bugID int, w http.ResponseWriter, client bugzilla.Client, allTargetVersions sets.String) (*ClonesTemplateData, error) {
	bug, err := client.GetBug(bugID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		wpErr := writePage(w, "Not Found", errorTemplate, fmt.Sprintf("Bug#%d not found", bugID))
		if wpErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return nil, wpErr
		}
		return nil, err
	}
	prs, err := client.GetExternalBugPRsOnBug(bug.ID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		wpErr := writePage(w, "Error.", errorTemplate, fmt.Sprintf("Bug#%d - error occured while retreiving list of PRs : %v", bug.ID, err))
		if wpErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return nil, wpErr
		}
		return nil, err
	}
	parent, err := client.GetRootForClone(bug)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		wpErr := writePage(w, "Error.", errorTemplate, fmt.Sprintf("Bug#%d Details of parent could not be retrieved : %v", bug.ID, err))
		if wpErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return nil, wpErr
		}
		return nil, err
	}

	clones, err := client.GetAllClones(bug)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		wpErr := writePage(w, "Not Found", errorTemplate, err.Error())
		if wpErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return nil, wpErr
		}
		return nil, err
	}
	// Target versions would be used to populate the CreateClone dropdown
	targetVersions := sets.NewString(allTargetVersions.List()...)
	// Remove target versions of the original bug
	for _, release := range bug.TargetRelease {
		_, ok := targetVersions[release]
		if ok {
			targetVersions.Delete(release)
		}
	}
	for _, clone := range clones {
		clonePRs, err := client.GetExternalBugPRsOnBug(clone.ID)
		// Remove target releases which already have clones
		for _, release := range clone.TargetRelease {
			_, ok := targetVersions[release]
			if ok {
				targetVersions.Delete(release)
			}
		}

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			wpErr := writePage(w, "Error.", errorTemplate, fmt.Sprintf("Bug#%d - error occured while retreiving list of PRs : %v", clone.ID, err))
			if wpErr != nil {
				http.Error(w, "Error building page.", http.StatusInternalServerError)
				return nil, wpErr
			}
			return nil, err
		}
		clone.PRs = clonePRs
	}
	wrpr := ClonesTemplateData{
		Bug:          bug,
		Clones:       clones,
		Parent:       parent,
		PRs:          prs,
		CloneTargets: targetVersions.List(),
	}
	return &wrpr, nil
}

// ClonesHandler acts as a router for the RESTish calls to clones
func ClonesHandler(client bugzilla.Client, allTargetVersions sets.String) HandlerFuncWithErrorReturn {
	return func(w http.ResponseWriter, r *http.Request) error {
		if r.Method == "GET" {
			return GetClonesHandler(client, allTargetVersions, w, r)
		} else if r.Method == "POST" {
			return CreateCloneHandler(client, allTargetVersions, w, r)
		}
		w.WriteHeader(http.StatusBadRequest)
		wpErr := writePage(w, "Error.", errorTemplate, fmt.Sprintf("%s - query incorrect", BugIDQuery))
		if wpErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return wpErr
		}
		return fmt.Errorf("Bad request - clones can handle only GET or POST requests")
	}
}

// GetClonesHandler returns an HTML page with detais about the bug and its clones
func GetClonesHandler(client bugzilla.Client, allTargetVersions sets.String, w http.ResponseWriter, req *http.Request) error {
	bugIDStr := req.URL.Query().Get(BugIDQuery)
	if bugIDStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		wpErr := writePage(w, "Error.", errorTemplate, fmt.Sprintf("%s - query incorrect", BugIDQuery))
		if wpErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return wpErr
		}
		return fmt.Errorf("%s - query incorrect", BugIDQuery)
	}
	bugID, err := strconv.Atoi(bugIDStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		wpErr := writePage(w, "Error.", errorTemplate, fmt.Sprintf("Unable to parse bug id: %v", err))
		if wpErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return wpErr
		}
		return err
	}

	wrpr, err := getClonesTemplateData(bugID, w, client, allTargetVersions)
	if err != nil {
		return err
	}
	wpErr := writePage(w, "Clones", clonesTemplate, wrpr)
	if wpErr != nil {
		http.Error(w, "Error building page.", http.StatusInternalServerError)
		return wpErr
	}
	return nil
}

// CreateCloneHandler will create a clone of the specified ID and return success/error
func CreateCloneHandler(client bugzilla.Client, allTargetVersions sets.String, w http.ResponseWriter, req *http.Request) error {
	// Parse the parameters passed in the POST request
	err := req.ParseForm()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		wpErr := writePage(w, "Error", errorTemplate, fmt.Sprintf("Unable to parse request : %v", err))
		if wpErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return wpErr
		}
		return err
	}
	bugID, err := strconv.Atoi(req.FormValue("ID"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		wpErr := writePage(w, "Error", errorTemplate, fmt.Sprintf("Invalid bug id. - Bug#%d : %v", bugID, err))
		if wpErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return wpErr
		}
		return err
	}
	// Get the details of the bug
	bug, err := client.GetBug(bugID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		wpErr := writePage(w, "Not Found", errorTemplate, fmt.Sprintf("Unable to fetch bug details- Bug#%d : %v", bugID, err))
		if wpErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return wpErr
		}
		return err
	}
	// Create a clone of the bug
	cloneID, cloneBugErr := client.CloneBug(bug)
	if cloneBugErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if wpErr := writePage(w, "Error", errorTemplate, fmt.Sprintf("Clone creation failed. %v", cloneBugErr)); wpErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return wpErr
		}
		return cloneBugErr
	}
	targetRelease := bugzilla.BugUpdate{
		TargetRelease: []string{
			req.FormValue("release"),
		},
	}
	// Updating the cloned bug with the right target version
	if updateBugErr := client.UpdateBug(cloneID, targetRelease); updateBugErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		wpErr := writePage(w, "Error", errorTemplate, fmt.Sprintf("Clone created - Bug#%d, but failed to specify version for the cloned bug. %v", cloneID, updateBugErr))
		if wpErr != nil {
			http.Error(w, "Error building page.", http.StatusInternalServerError)
			return wpErr
		}
		return updateBugErr
	}
	// Repopulate the fields of the page with the right data
	data, err := getClonesTemplateData(bugID, w, client, allTargetVersions)
	if err != nil {
		return err
	}
	// Populating the NewCloneId which is used to show the success info banner
	data.NewCloneID = cloneID
	if err = writePage(w, "Clones", clonesTemplate, *data); err != nil {
		http.Error(w, "Error building page.", http.StatusInternalServerError)
		return err
	}
	return nil
}
