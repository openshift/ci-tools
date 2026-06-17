# rebalancer

## What
CLI tool that redistributes CI tests across equivalent cluster profiles to balance Boskos lease usage. When multiple cloud profiles can serve the same purpose (e.g. `aws-1` and `aws-2`), rebalancer assigns each test to the profile with the least accumulated workload, then rewrites the ci-operator config files in place.

## How it works -- full flow

1. **Parse profile groups**: the `--profiles` flag is specified one or more times, each value being a comma-separated list of equivalent profiles (e.g. `--profiles aws-1,aws-2 --profiles gcp-1,gcp-2`). These form groups within which load is balanced.

2. **Query Prometheus for job volumes**: using the `--prometheus-*` flags, it queries Prometheus for job execution volumes over the past `--prometheus-days-before` days (default 14). The result is a `map[string]float64` mapping full job names to their execution weight (volume). This uses `pkg/dispatcher.NewPrometheusVolumes` and `GetJobVolumes()`.

3. **Walk ci-operator configs**: it reads all ci-operator configuration from `ci-operator/config/` in the current working directory via `config.OperateOnCIOperatorConfigDir`.

4. **Greedy assignment**: for each test in each config that has a `MultiStageTestConfiguration` with a `ClusterProfile` belonging to one of the configured groups:
   - Find the profile in the group with the minimum accumulated workload (bucket value).
   - If the test's current profile differs from the best choice, reassign it and log the change.
   - Add the test's Prometheus-derived volume weight to the chosen profile's bucket.

5. **Write updated configs**: modified configs are committed back to disk via `config.DataWithInfo.CommitTo()`.

6. **Log final weights**: the accumulated weight per profile is logged for visibility.

### Job name construction
The tool constructs the expected Prow job name for each test to look it up in the Prometheus volume data. The format is: `{periodic|branch|pull}-ci-{org}-{repo}-{branch}[-{variant}]-{testname}`.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--profiles` | (required, repeatable) | Comma-separated list of profiles forming one equivalence group. Specify multiple times for multiple groups |
| `--prometheus-days-before` | 14 | Number of days of historical data to query from Prometheus (range [1,15]) |
| `--prometheus-username` | (from PrometheusOptions) | Prometheus basic auth username |
| `--prometheus-password-path` | (from PrometheusOptions) | Path to file containing Prometheus password |
| `--prometheus-bearer-token-path` | (from PrometheusOptions) | Path to file containing Prometheus bearer token |

## Key files
- `cmd/rebalancer/main.go` -- entry point, profile group parsing, greedy assignment loop, config rewriting
- `pkg/dispatcher/prometheus_volumes.go` -- Prometheus client for job volume queries
- `pkg/dispatcher/prometheus.go` -- `GetJobVolumesFromPrometheus()` query implementation
- `pkg/config/load.go` -- `OperateOnCIOperatorConfigDir()` for walking ci-operator configs

## Deployment
CLI tool. Typically invoked by `auto-config-brancher` as part of an automated periodic job that proposes changes to the openshift/release repository. Must be run from a directory containing `ci-operator/config/` (the release repo).

Rebalancer is needed when Boskos leases are in short supply for some profiles.

## Usage

```bash
oc --context app.ci whoami -t > /tmp/token
# go to release repository folder and execute:
/path/to/rebalancer --profiles='azure4,azure-2' --prometheus-bearer-token-path=/tmp/token
```
