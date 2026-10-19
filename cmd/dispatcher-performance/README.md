# Dispatcher performance observations

Download `ci-operator-metrics.json` artifacts from representative baseline and
candidate job runs into separate directories, preserving one subdirectory per
artifact. Then run from the repository root:

```sh
go run ./cmd/dispatcher-performance --baseline /path/to/baseline --candidate /path/to/candidate > comparison.json
```

The command recursively reads files named exactly `ci-operator-metrics.json`.
Empty directories, missing job names, malformed JSON and invalid measured times
are errors. Unknown metrics fields are accepted for compatibility.

JSON includes each exact Prow job name, all-job totals, matched-job totals and
lists of matched and unmatched job names. Compare per-job distributions first.
Even matched totals can change with the frequency of jobs, inputs, failures,
clusters, targets or traffic; this tool does not normalize those factors or claim
causality, statistical significance or a predicted gain.

Run identity is `prowjobid` plus `target`, falling back to the SHA-256 of the file
contents when `prowjobid` is absent. Duplicate runs retain the first artifact in
lexical path order. All files are validated before deduplication. The output
reports file and duplicate counts, while summary `runs` counts distinct runs.

Namespace reuse reports reused runs, runs with a reuse observation (the
denominator) and runs missing an observation. Older artifacts without `reused`
are missing, not false. Timing distributions use one maximum observation per
run, sample counts, and nearest-rank p50/p95 in seconds. Missing percentiles are
JSON null. `ci_operator_setup` is `execution_started.started_after`, time since
ci-operator started, **not Prow queue latency**. `ci_operator_total` is
`execution_completed.duration_seconds`, also measured from ci-operator start.

Builds are deduplicated by UID with the maximum measured duration per UID.
Duration is calculated from nonzero start/completion timestamps for terminal
`Complete`, `Failed`, `Error` or `Cancelled` observations. Pending/running builds
and failures before starting lack measured duration, even though the producer
serializes `duration_seconds: 0`. A terminal build whose valid timestamps are
equal has a measured duration of zero. Reversed intervals are errors.
Summed duration is observed build elapsed seconds, **not node hours, CPU usage
or cost**. Missing UID observations and unique UIDs missing durations are
reported. Pod scheduling percentiles likewise deduplicate by UID, use maximum
observations and convert nanoseconds to seconds, with missing UID and latency
coverage. Older artifacts without UIDs cannot contribute to unique-resource
statistics. Per-job resource summaries can overlap; their sums must not be used
as overall resource totals. Overall summaries deduplicate shared UIDs across
all their included jobs.

`possible_repeated_builds` lists input-hash/image pairs with multiple distinct
observed build UIDs. Retries, namespace TTL expiry, clusters or changes outside
the hash can cause these instances; they do not establish wasted work. Artifacts
without an input hash or image name cannot contribute to this list. No overall
percentage saving is estimated.
