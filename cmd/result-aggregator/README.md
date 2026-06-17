# result-aggregator

## What
HTTP server that collects CI failure reasons and pod-scaler resource configuration warnings from ci-operator and pod-scaler, exposing them as Prometheus counters. This is the central collection point for CI error classification metrics, enabling dashboards and alerting on failure patterns.

## How it works -- full flow

### Server startup
1. Parse flags and validate: `--passwd-file` is required.
2. Register two Prometheus counter vectors: `ci_operator_error_rate` and `pod_scaler_admission_high_determined_resource`.
3. Set up HTTP routes with basic auth middleware.
4. Start serving metrics on the default Prow metrics port (via `metrics.ExposeMetrics`).
5. Listen on `--address` (default `:8080`) with graceful shutdown support.

### POST /result -- ci-operator error reporting
ci-operator calls this endpoint at the end of each job run to report success or failure.

1. Authenticate via HTTP basic auth against the password file.
2. Decode the JSON body into a `results.Request` struct.
3. Validate required fields: `job_name`, `type`, `state`, `reason`, `cluster`.
4. Increment the `ci_operator_error_rate` Prometheus counter with labels: `job_name`, `type` (presubmit/postsubmit/periodic), `state` (succeeded/failed), `reason` (colon-delimited chain of failure reasons), `cluster`.

### POST /pod-scaler -- resource configuration warnings
pod-scaler calls this endpoint when it determines a higher resource amount than what was configured.

1. Authenticate via HTTP basic auth.
2. Decode the JSON body into a `results.PodScalerRequest` struct.
3. Validate required fields: `workload_name`, `workload_type`, `configured_amount`, `determined_amount`, `resource_type`.
4. Increment the `pod_scaler_admission_high_determined_resource` counter with labels: `workload_name`, `workload_type`, `configured_amount`, `determined_amount`, `resource_type`, `measured` (true/false), `workload_class`.

### Authentication
Authentication uses a password file where each line is `username:password` (colon-delimited). The `multi` validator wraps one or more `passwdFile` validators, returning true if any delegate validates the credentials. The file is re-read on every request (no caching).

### Client-side reporting
The `pkg/results` package provides the client side: `Options.Reporter()` creates a `Reporter` that POSTs to the result-aggregator's `/result` endpoint. Each ci-operator invocation calls `reporter.Report(err)` at completion. A report is always sent regardless of outcome: on success (`Report(nil)`), a request with reason "unknown" and state "succeeded" is sent; on failure, the chain of `results.Reason` values is extracted from the error and one request per reason chain is sent.

The default server address is `https://result-aggregator-ci.apps.ci.l2s4.p1.openshiftapps.com`.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--address` | `:8080` | Address to listen on |
| `--log-level` | `info` | Log level |
| `--gracePeriod` | `10s` | Grace period for server shutdown |
| `--passwd-file` | (required) | Path to file with `username:password` lines for basic auth |

## Key files
- `cmd/result-aggregator/main.go` -- HTTP server, request handlers, Prometheus counter registration, validation
- `cmd/result-aggregator/validator.go` -- password file parsing (`passwdFile`), multi-validator support
- `pkg/results/report.go` -- client-side `Reporter` and `PodScalerReporter` implementations, `Request` and `PodScalerRequest` types
- `pkg/results/error.go` -- `Error` type with `Reason` chains, `ForReason()` builder, `Reasons()` extractor
- `pkg/results/results.go` -- `Reason` type definition

## Deployment
Long-lived Deployment on app.ci in the `ci` namespace. Exposed via a Route. The password file is mounted from a Secret. Prometheus scrapes the metrics port.

## Related
- Every ci-operator invocation reports to this server via the `--report-address` and `--report-credentials-file` flags.
- pod-scaler-admission reports resource warnings via the `/pod-scaler` endpoint.
- Prometheus counter `ci_operator_error_rate` powers CI failure dashboards.
