# prow-job-dispatcher

## What
Assigns Prow CI jobs to build farm clusters based on cloud provider affinity, cluster capacity, capability matching, and historical job volume. Exposes an HTTP API for real-time scheduling decisions and periodically rebalances the full assignment map.

## How it works

### Job assignment algorithm (`findClusterForJobConfig()`)
For each Prow job config file:
1. Determine cloud provider from job's e2e tests (checks `ci-operator.openshift.io/cloud` label or `CLUSTER_TYPE` env var)
2. If all tests target same cloud provider, prefer clusters on that provider
3. Check if the most-used cluster has spare capacity (< 75% of fair share distribution)
4. If yes: assign to most-used cluster (locality benefit)
5. If no: find cluster with minimum current volume across all providers (or specific provider if determined)
6. Only assign to clusters with 100% capacity

### Matching priority (in `DetermineClusterForJob()`)
Jobs are routed through this priority chain (first match wins):
1. Non-Kubernetes jobs — skip
2. vSphere jobs — direct assignment to vsphere cluster
3. SSH Bastion jobs — assign to configured bastion cluster
4. Explicit `ci.openshift.io/cluster` label — direct assignment
5. `capability/*` labels — match to cluster with all required capabilities
6. KVM device requests — route to KVM cluster list
7. Cloud provider mapping — deterministic by e2e test cloud
8. No-builds jobs — route to NoBuilds cluster list
9. Job/Path groups — match from config Groups section (job names or path regexes)
10. Build farm files — check BuildFarm configurations
11. Default cluster — fallback

### Scheduling modes
- **Full dispatch** (weekly, Sundays 7 AM UTC, or on config change): queries Prometheus for 7 days of job volumes, calculates fair share per cluster based on capacity, assigns all jobs
- **Delta dispatch** (every 5 minutes): assigns new/modified jobs not in full dispatch
- **Config monitoring** (every 1 minute): detects cluster config changes, triggers re-dispatch if capacity/capabilities changed
- **HTTP scheduling** (on-demand): POST `/` with `{"job": "name"}` returns `{"cluster": "name"}`

### Ephemeral cluster handling
Round-robin scheduling for Konflux ephemeral clusters with 24-hour TTL cache.

### PR creation
After full dispatch, optionally creates PR to openshift/release with updated job assignments. Labels: `rehearsals-ack`, `priority/ci-critical`. Sends Slack notification to ops channel.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--prow-jobs-dir` | — | Root directory of Prow job configs |
| `--config-path` | — | Dispatcher config file |
| `--cluster-config-path` | `core-services/sanitize-prow-jobs/_clusters.yaml` | Cluster configuration |
| `--jobs-storage-path` | — | Gob file for job assignments |
| `--prometheus-days-before` | 14 | Historical days for volume query (1-15) |
| `--prometheus-url` | thanos-querier URL | Prometheus endpoint |
| `--create-pr` | false | Create PR with updated assignments |
| `--slack-token-path` | — | Slack API token |
| `--ops-channel-id` | `CHY2E1BL4` | Slack ops channel |
| `--enable-cluster` | — | Enable disabled clusters (repeatable) |
| `--disable-cluster` | — | Disable enabled clusters (repeatable) |

## Key files
- `cmd/prow-job-dispatcher/main.go` — orchestration, cron scheduling, HTTP server
- `pkg/dispatcher/config.go` — `DetermineClusterForJob()`, matching chain
- `pkg/dispatcher/server.go` — HTTP API handlers
- `pkg/dispatcher/prowjobs.go` — thread-safe job assignment storage
- `pkg/dispatcher/prometheus_volumes.go` — volume distribution calculation

## Deployment
Long-lived Deployment on app.ci, namespace ci. Container listens on port 8080.

As designed in [[DPTP-1152] Choose a cluster for prow jobs](https://docs.google.com/document/d/1aiuZ70jtvZiQBo2P8NgacRj0GmqUH6DRxE4KFFph1RM/edit) this tool chooses a cluster in the CI build farm for Prow jobs.

* It starts off by figuring out how many runs of each Prow jobs we had in the last seven days by querying the Prometheus instance in Prow-monitoring stack.
* It groups all jobs from a Prow job file together and will always try to put all of them on the same cluster.
* If a job has config stating it must be on a specific cluster, that will always be respected. This could lead to a job with tests on different clusters. We should not have many of those cases.
* If all e2e jobs in a group run on the same cloud provider, it will only consider clusters on that cloud provider, if any. Otherwise, all build clusters are considered.
* It will then choose the cluster with the least number of jobs, based on the Prometheus metrics and the already dispatched jobs.

The choices of cluster are stored in the following stanza of [the config file](https://github.com/openshift/release/blob/main/core-services/sanitize-prow-jobs/_config.yaml) of [`sanitize-prow-jobs`](../sanitize-prow-jobs).

```
buildFarm:
  aws: 
    build01:
      jobs:
      - job-name-1
  gcp: 
    build02:
      jobs:
      - job-name-1

```

The tool `sanitize-prow-jobs` will then use the stored information to generate the `cluster` field of the Prow jobs.

We can use [run-prow-job-dispatcher.sh](../../hack/run-prow-job-dispatcher.sh) to build and run the tool locally.
