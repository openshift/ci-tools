# job-run-aggregator

## What
Cobra-based multi-command CLI for statistical analysis of CI job runs. It ingests job run data from GCS into BigQuery, analyzes pass/fail rates across multiple runs of the same job (or across jobs for a payload), and detects regressions by comparing against historical baselines. Used by the release controller to gate payload acceptance.

## Subcommands

### `upload-disruptions`
Uploads backend disruption data from GCS job artifacts into the `BackendDisruption` BigQuery table.
- Reads `backend-disruption` prefixed files from each job run's GCS artifacts.
- Uses 10 concurrent worker goroutines to process job runs.
- Tracks which job runs have already been uploaded to avoid duplicate inserts (BigQuery does not prevent duplicates).
- Only processes jobs where `CollectDisruption` is true in the jobs table.

### `upload-alerts`
Uploads alert firing data from GCS job artifacts into the `Alerts` BigQuery table.
- Reads `alert` prefixed files from each job run's GCS artifacts.
- Populates zeros for known alerts that were not observed in a run, ensuring correct percentile calculations.
- Maintains a `KnownAlertsCache` of all alert/namespace/level combinations ever seen per release.
- Processes all jobs (not filtered by `CollectDisruption`).

### `analyze-job-runs`
Aggregates multiple runs of a single job to determine pass/fail for the overall payload.
- Locates job runs by either `--payload-tag` (release controller payloads) or `--aggregation-id` (per-PR payload promotion).
- Waits up to `--timeout` (default 5h30m) for all job runs to complete.
- Uses a `weeklyAverageFromTenDaysAgo` pass/fail calculator that compares current results against a 6-window weekly average baseline.
- Queries job run states from BigQuery or directly from the ProwJob cluster (`--query-source`).
- Writes JUnit XML and a spyglass summary to the working directory.

### `analyze-test-case`
Analyzes test case pass rates across multiple different jobs for a payload, used to gate payloads on cross-job test stability.
- Filters jobs by `--platform`, `--network`, `--infrastructure`, and `--include-job-names`/`--exclude-job-names`.
- Supports test groups: `install`, `upgrade`, `overall`.
- Enforces `--minimum-successful-count` (default 1) successful test runs across matching jobs.
- For PR payloads, uses `--payload-invocation-id` and `--explicit-gcs-prefixes`.

### `analyze-historical-data`
Compares new BigQuery query results against a current baseline file for alerts, disruptions, or test pass rates.
- Supports data types: `alerts`, `disruptions`, `tests`.
- For alerts and disruptions: compares new vs current data with `--leeway` percentage threshold.
- For tests: generates historical test data without comparison.
- Outputs results to `--output-file` (default `results_{datatype}.json`).

### `prime-job-table`
Inserts or updates job metadata in the BigQuery jobs table.
- Reads all job names and generates entries with release, platform, network, and other variant data.
- Supports `--dry-run` mode.

### `create-releases`
Creates the BigQuery release tables schema.

### `upload-releases`
Uploads release/changelog data to BigQuery for specified `--releases` and `--architectures`.
- Parses release changelogs from the release controller API.

## Common flags (shared across subcommands via BigQuery coordinates and authentication)
| Flag | Default | What it controls |
|---|---|---|
| `--bigquery-project` | (from DataCoordinates) | Google Cloud project ID for BigQuery |
| `--bigquery-dataset` | (from DataCoordinates) | BigQuery dataset ID |
| `--google-service-account-credential-file` | (from Authentication) | Path to GCP service account key file |
| `--google-storage-bucket` | `test-platform-results` | GCS bucket holding test artifacts |

### analyze-job-runs specific flags
| Flag | Default | What it controls |
|---|---|---|
| `--job` | (required) | Name of the job to inspect |
| `--payload-tag` | (one required) | Payload tag to aggregate (mutually exclusive with `--aggregation-id`) |
| `--aggregation-id` | (one required) | Matches `release.openshift.io/aggregation-id` label on ProwJobs |
| `--explicit-gcs-prefix` | (none) | Override GCS prefix for per-PR payload jobs |
| `--working-dir` | `job-aggregator-working-dir` | Directory for caches and output |
| `--timeout` | `5h30m` | Maximum wait time for aggregation |
| `--job-start-time` | now | Estimated job start time in RFC3339 format |
| `--query-source` | `bigquery` | Source for job states: `bigquery` or `cluster` |

### analyze-test-case specific flags
| Flag | Default | What it controls |
|---|---|---|
| `--test-group` | `install` | Test group: `install`, `upgrade`, or `overall` |
| `--platform` | (none) | Filter by platform: aws, gcp, azure, vsphere, metal, ovirt, libvirt |
| `--network` | (none) | Filter by network: ovn, sdn |
| `--infrastructure` | (none) | Filter by infrastructure: ipi, upi |
| `--minimum-successful-count` | 1 | Minimum required successful test runs |
| `--payload-invocation-id` | (none) | For PR payloads, matches the prowjob label |
| `--explicit-gcs-prefixes` | (none) | Comma-separated `jobname=prefix` pairs for PR payloads |
| `--timeout` | `3h30m` | Maximum wait time |

## Key files
- `cmd/job-run-aggregator/main.go` -- entry point, delegates to `pkg/jobrunaggregator.NewJobAggregatorCommand()`
- `pkg/jobrunaggregator/cmd.go` -- cobra command tree assembly, registers all subcommands
- `pkg/jobrunaggregator/jobrunaggregatoranalyzer/cmd.go` -- `analyze-job-runs` flag definitions and options builder
- `pkg/jobrunaggregator/jobrunaggregatoranalyzer/analyzer.go` -- core analysis: wait for jobs, collect results, calculate pass/fail
- `pkg/jobrunaggregator/jobrunaggregatoranalyzer/pass_fail.go` -- `weeklyAverageFromTenDaysAgo` statistical calculator
- `pkg/jobrunaggregator/jobruntestcaseanalyzer/cmd.go` -- `analyze-test-case` flag definitions
- `pkg/jobrunaggregator/jobruntestcaseanalyzer/analyzer.go` -- cross-job test case analysis
- `pkg/jobrunaggregator/jobrunbigqueryloader/disruption.go` -- `upload-disruptions` implementation
- `pkg/jobrunaggregator/jobrunbigqueryloader/alert.go` -- `upload-alerts` implementation, known alerts zero-fill
- `pkg/jobrunaggregator/jobrunbigqueryloader/uploader.go` -- generic job run upload orchestration, 10-worker concurrency
- `pkg/jobrunaggregator/jobtableprimer/cmd.go` -- `prime-job-table` implementation
- `pkg/jobrunaggregator/releasebigqueryloader/cmd.go` -- `create-releases` and `upload-releases` implementations
- `pkg/jobrunaggregator/jobrunhistoricaldataanalyzer/cmd.go` -- `analyze-historical-data` implementation
- `pkg/jobrunaggregator/jobrunaggregatorlib/` -- shared utilities: BigQuery client, GCS client, job run locators, Google auth

## Deployment
CLI tool. Invoked via periodic Prow jobs ([recent runs](https://prow.ci.openshift.org/?job=periodic-update-origin-disruption-alert-data)) and by the release controller for payload gating. Requires GCP service account credentials with BigQuery and GCS access.

The job-run-aggregator finds multiple runs of the same job for the same payload and analyzes the overall result
and the individual junit results.

The analysis allows failures within (ideally) a standard deviation of the norm for individual tests.
This will allow a single payload to checked by multiple parallel job runs and the average results for each test
checked to ensure that a regression hasn't happened.
That property allows us to have less than perfect test results to start and still be able to latch improvements into
the failure percentages, giving a path to improvement.

## Development and Debugging tips

When you run this in debugging mode, use a credential that has read-only access. This way, you
can set breakpoints and study the behavior without risk of overwriting anything.
You will, of course, get "permission denied" errors if write access is required.
At that point, you can (cautiously) switch to a credential that has write access.

This is a way to build (without optimazations) for debugging:

```
cd ci-tools
go build -gcflags='-N -l' `grep "module " go.mod |awk '{print $2}'`/cmd/job-run-aggregator
```

Example command lines:

```
# Run in dry run mode with read credential
dlv exec ./job-run-aggregator -- upload-test-runs --dry-run --bigquery-dataset ci_data --google-service-account-credential-file ~/project-reader.json

# Run command to create tables in write mode (in a dataset called "my_dataset" created for testing)
dlv exec ./job-run-aggregator -- create-releases --bigquery-dataset my_dataset --google-service-account-credential-file ~/project-write.json

# This will create the Jobs table:
dlv exec ./job-run-aggregator -- create-tables --bigquery-dataset my_dataset --google-service-account-credential-file ~/project-write.json

# This will run and insert the jobs in "Jobs" table
dlv exec ./job-run-aggregator -- prime-job-table --bigquery-dataset my_dataset --google-service-account-credential-file ~/project-write.json
```

Here's how to reproduce and (hopefully) fix things if the linter (run as part of CI) fails:

```
# Get the linter from https://golangci-lint.run/usage/install

# After you make your code changes, run the linter
cd ci-tools
./hack/lint.sh

# If something fails, this might fix it; then rerun the linter
gofmt -s -w <someFileThatFailsLint>/cmd.go
```

## Running Locally

### Analyze Historical Data

> Note: We also contain a helper script at the root of this repo that can be used to generate the files run `./hack/run-job-aggregator-analyzer.sh` for a list of options.

You can compare the historical data locally, the documentation on how to download the recent historical data can be found [here](https://docs.ci.openshift.org/release-oversight/disruption-testing/data-architecture/#query).

```sh
# Disruptions data
./job-run-aggregator analyze-historical-data  \
--current ./<current-disruptions>.json \
--data-type disruptions \
--new ./<new-disruptions>.json \
--leeway 30

# Alerts data
./job-run-aggregator analyze-historical-data  \
--current ./<current-alerts>.json \
--data-type alerts \
--new ./<new-alerts>.json \
--leeway 30
```

You can also automatically pull the latest from BigQuery if you have read credentials.

```sh
# Disruptions data
./job-run-aggregator analyze-historical-data  \
--current ./<current-disruptions>.json \
--data-type disruptions \
--leeway 30 \
--google-service-account-credential-file <gcs_creds.json>

# Alerts data
./job-run-aggregator analyze-historical-data  \
--current ./<current-alerts>.json \
--data-type alerts \
--leeway 30 \
--google-service-account-credential-file <gcs_creds.json>
```
