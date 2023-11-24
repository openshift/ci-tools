package html

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
)

const StaticSubdir = "dist"
const StaticURL = "/static/"

//go:embed dist
var StaticFS embed.FS

var pageStart = `<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8"><title>%s</title>
<meta name="viewport" content="width=device-width, initial-scale=1, shrink-to-fit=no">
<link rel="stylesheet" href="` + StaticURL + `base.css">
<link rel="stylesheet" href="https://stackpath.bootstrapcdn.com/bootstrap/4.1.3/css/bootstrap.min.css" integrity="sha384-MCw98/SFnGE8fJT3GXwEOngsV7Zt27NXFoaoApmYm81iuXoPkFOJwJ8ERdknLPMO" crossorigin="anonymous">
<script src="https://code.jquery.com/jquery-3.3.1.slim.min.js" integrity="sha384-q8i/X+965DzO0rT7abK41JStQIAqVgRVzpbzo5smXKp4YfRvH+8abtTE1Pi6jizo" crossorigin="anonymous"></script>
<script src="https://stackpath.bootstrapcdn.com/bootstrap/4.1.3/js/bootstrap.min.js" integrity="sha384-ChfqqxuZUCnJSK3+MXmPNIyE6ZbWh2IMqE241rYiqJxyMiZ6OW/JmZQ5stwEULTy" crossorigin="anonymous"></script>
</head>
<body>
`

const pageEnd = `</body>
</html>
`

func WritePage(w http.ResponseWriter, title, bodyStart, end string, body *template.Template, data interface{}) error {
	e := func(err error) error {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s: %v", http.StatusText(http.StatusInternalServerError), err)
		return err
	}
	if _, err := fmt.Fprintf(w, pageStart, title); err != nil {
		return e(err)
	}
	if _, err := fmt.Fprintln(w, bodyStart); err != nil {
		return e(err)
	}
	if err := body.Execute(w, data); err != nil {
		return e(err)
	}
	if _, err := fmt.Fprintln(w, end); err != nil {
		return e(err)
	}
	if _, err := fmt.Fprint(w, pageEnd); err != nil {
		return e(err)
	}
	return nil
}
