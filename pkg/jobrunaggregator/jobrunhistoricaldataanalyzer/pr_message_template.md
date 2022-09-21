## {{ .DataType }} Information

There were (`{{len .AddedJobs}}`) added jobs and (`{{len .MissingJobs}}`) were removed.

{{- if gt .IncreasedCount 0 }}

### Comparisons were above allowed leeway of `{{.Leeway}}`

Note: {{.DataType}} had `{{.IncreasedCount}}` jobs increased and `{{.DecreasedCount}}` jobs decreased.

{{formatTableOutput .Jobs true}}

{{- end -}}

{{ if .MissingJobs }}

### Missing Data

Note: Jobs that are missing from the new data set but were present in the previous dataset.

{{formatTableOutput .MissingJobs false}}

{{ end }}
