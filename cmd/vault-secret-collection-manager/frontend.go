package main

import (
	_ "embed"
	"text/template"
)

var (
	//go:embed style.css
	styleCSS []byte

	//go:embed index.js
	indexJS []byte

	//go:embed index.template.html
	indexTemplateRaw []byte

	indexTemplate = template.Must(template.New("index").Parse(string(indexTemplateRaw)))
)
