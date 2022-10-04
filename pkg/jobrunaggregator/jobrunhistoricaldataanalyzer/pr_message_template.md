## {{ .DataType }} Information

There were (`{{len .AddedJobs}}`) added jobs and (`{{len .MissingJobs}}`) were removed.

{{- if gt .IncreasedCount 0 }}

### Comparisons were above allowed leeway of `{{.Leeway}}`

Note: {{.DataType}} had `{{.IncreasedCount}}` jobs increased and `{{.DecreasedCount}}` jobs decreased.

<details>
  <summary>Click To Show Table</summary>

{{formatTableOutput .Jobs true}}

</details>
{{- end -}}

{{ if .MissingJobs }}

### Missing Data

Note: Jobs that are missing from the new data set but were present in the previous dataset.

<details>
  <summary>Click To Show Table</summary>

{{formatTableOutput .MissingJobs false}}

</details>
{{ end }}
