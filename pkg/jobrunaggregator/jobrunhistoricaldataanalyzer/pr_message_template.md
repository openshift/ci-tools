## {{ .DataType }} Information

There were (`{{len .AddedJobs}}`) added jobs and (`{{len .MissingJobs}}`) were removed.

{{- if gt .IncreasedCount 0 }}

### Comparisons were above allowed leeway of `{{.Leeway}}`

Note: For P99, {{.DataType}} had `{{.IncreasedCount}}` jobs increased and `{{.DecreasedCount}}` jobs decreased.

<details>
  <summary>Click To Show Table</summary>

{{formatTableOutput .Jobs true}}

</details>
{{ end }}
