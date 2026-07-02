# lensserver

## What
Spyglass lens server that provides a "CI-Operator steps" visualization for Prow's log viewer (Deck/Spyglass). It renders an interactive step graph showing the execution timeline, dependencies, duration, and status of each ci-operator step in a job run.

Spyglass is Prow's artifact viewer. Lenses are plugins that render specific artifact types. This server hosts the `steps` lens which reads the step graph JSON artifact produced by ci-operator and renders it as an interactive HTML visualization.

## How it works -- full flow

1. Initialize Prow config agent from the provided config options
2. Create a `jobs.JobAgent` with a fake (no-op) ProwJob listing client -- real ProwJob data is not needed for artifact rendering
3. Create a storage artifact opener for GCS/S3 using the provided credentials
4. Register the `stepgraph.Lens` as a local lens with:
   - Name: `steps`
   - Title: `CI-Operator steps`
   - Priority: `6`
5. Create a `common.LensServer` from Prow's spyglass library, configured with:
   - Listen address: `127.0.0.1:1235`
   - The storage artifact fetcher (reads artifacts from GCS/S3)
   - A pod log artifact fetcher (unused in practice)
   - The step graph lens
6. Start the HTTP server

### Step graph lens rendering
The `stepgraph.Lens` (in `pkg/lenses/stepgraph/`) works as follows:
1. Expects exactly one artifact (the step graph JSON file produced by ci-operator, typically at `artifacts/ci-operator-step-graph.json`)
2. Reads and unmarshals the artifact into a `[]Step` slice, where each step contains:
   - Step name, description, dependencies
   - Start time, finish time, duration
   - Success/failure status
   - Kubernetes manifests applied by the step
3. Sorts steps by start time
4. Serializes any embedded Kubernetes manifests to YAML for display
5. Renders an HTML template (`static/template.html`) with the step data, producing an interactive graph visualization

### Why a separate server
Prow's Spyglass architecture supports "local lenses" that run as sidecar HTTP servers alongside Deck. The lens server communicates with Deck over localhost. This allows custom lens implementations without modifying Deck itself.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| Prow config flags | | Standard `configflagutil.ConfigOptions` (`--config-path`, `--job-config-path`, etc.) |
| `--gcs-credentials-file` | `""` | GCS service account key for reading artifacts |
| `--s3-credentials-file` | `""` | S3 credentials for reading artifacts |

## Key files
- `cmd/lensserver/main.go` -- entry point, server setup, fake PJ client
- `pkg/lenses/stepgraph/stepgraph.go` -- lens implementation (Header, Body, Callback, Config)
- `pkg/lenses/stepgraph/static/template.html` -- HTML/CSS/JS template for the step graph visualization
- `pkg/api/graph.go` -- `CIOperatorStepDetails` and `CIOperatorStepGraph` types

## Deployment
Not currently deployed. Historically designed as a sidecar container to Prow Deck (listening on `127.0.0.1:1235`), but no lensserver sidecar exists in the current Deck Deployment — the Deck pod only contains `deck` and `git-sync` containers.

For local development, use `hack/run-lens.sh`.

## Related
- Deck references this lens server at `127.0.0.1:1235` in its Spyglass lens configuration
- ci-operator writes the step graph artifact to `artifacts/ci-operator-step-graph.json` during job execution
- The step graph shows step dependencies, execution order, timing, and pass/fail status with expandable manifest details
Run it together with [Deck][3] via the hack script:

```sh
hack/run-lens.sh
```

[0](https://github.com/kubernetes/test-infra/blob/master/prow/spyglass/architecture.md#spyglass-lenses)
[1](https://github.com/kubernetes/test-infra/tree/master/prow/spyglass)
[3](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/deck)
