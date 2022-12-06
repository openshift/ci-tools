## {{ .DataType }} Information

There were (`{{len .AddedJobs}}`) added jobs and (`{{len .MissingJobs}}`) were removed.

{{- if .NewReleaseData }}

> :warning: {{ .DataType }} data has been updated to latest release. {{ if eq (len .AddedJobs) 0}}No Jobs were added, please check data set. ([query docs](https://docs.ci.openshift.org/docs/release-oversight/disruption-testing/data-architecture/#query)){{ end}}

{{ end }}

{{- if gt .IncreasedCount 0 }}

### Comparisons were above allowed leeway of `{{.Leeway}}`

Note: For P99, {{.DataType}} had `{{.IncreasedCount}}` jobs increased and `{{.DecreasedCount}}` jobs decreased.

<details>
  <summary>Click To Show Table</summary>

{{formatTableOutput .Jobs true}}

</details>
{{ end }}

{{- if .MissingJobs }}

### Missing Data

Note: Jobs that are missing from the new data set but were present in the previous dataset.

<details>
  <summary>Click To Show Table</summary>

{{formatTableOutput .MissingJobs false}}

</details>
{{ end }}
