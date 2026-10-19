# prow-job-dispatcher

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

## Observation API

Set `--observation-api-token-path` to enable the GET-only `/observe/v1/` API.
Use `Authorization: Bearer <observation-token>` with a credential distinct from
the control API token. Observation is disabled when the flag is unset. If token
values collide during rotation, control API access fails closed.

- `/state` reports readiness, policy identity, inventory and effective job demand
  per cluster. `inventoryKnown: false` identifies targets outside the inventory;
  capacity is the configured scheduling weight, not measured utilization.
- `/jobs?limit=100` lists baseline and effective decisions, with optional `name`
  substring, exact `group`, and effective `cluster` filters. Limits are 1 to 500.
  Continue with `after=<nextAfter>&generation=<policy.generation>` and the same
  filters. HTTP 409 means the policy changed; restart pagination.
  Each page is live at its `observedAt`: expiry can change the effective cluster
  without changing generation. Restart a filtered traversal after an expiry boundary.
- `/overrides` reports durable records and audit history separately from snapshot
  inclusion (`published`) and effective assignment counts (`servingJobs`). Listing
  failures or disabled runtime controls return HTTP 503.

Responses use one serving snapshot and observation time, apply expiry fallback,
and do not schedule jobs or change policy. Jobs return HTTP 503 before readiness;
state reports `ready: false`. `generatedAt` is the last policy change, not a heartbeat.
The reconcile success timestamp and error counter are exported as
`prowjob_dispatcher_last_successful_reconcile_timestamp_seconds` and
`prowjob_dispatcher_reconcile_errors_total`. The
`prowjob_dispatcher_last_baseline_update_timestamp_seconds` metric records input
delivery to the control plane, including unchanged inputs; it does not measure
Git synchronization or demand-query freshness. These metrics start at zero.
