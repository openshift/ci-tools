# prow-job-dispatcher

As designed in [[DPTP-1152] Choose a cluster for prow jobs](https://docs.google.com/document/d/1aiuZ70jtvZiQBo2P8NgacRj0GmqUH6DRxE4KFFph1RM/edit) this tool chooses a cluster in the CI build farm for Prow jobs.

* It estimates demand from seven-day job run counts and optional aggregate duration, CPU and memory queries in the Prow-monitoring stack.
* It groups all jobs from a Prow job file together and will always try to put all of them on the same cluster.
* If a job has config stating it must be on a specific cluster, that will always be respected. This could lead to a job with tests on different clusters. We should not have many of those cases.
* If all e2e jobs in a group run on the same cloud provider, it will only consider clusters on that cloud provider, if any. Otherwise, all build clusters are considered.
* It chooses a cluster using projected demand divided by configured capacity, respecting placement restrictions, hysteresis and a movement budget.

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

## Ordering and shadow measurement

`--group-order=file-size` retains the configuration-file-size ordering. Set
`--group-order=demand` to process groups by aggregate relocatable demand, largest
first. Fixed and affinity-bound demand is charged before placing movable groups,
so it is visible even when its configuration file is processed last. This fixed
load accounting also applies to file-size ordering. Ties use repository-relative
paths. Both policies retain cloud and capability restrictions, existing explicit
placements, hysteresis and the movement budget.

`--shadow-group-order=demand` is enabled by default. It evaluates a second plan
from the same frozen job configurations, inventory, demand and previous
assignments. It has its own configuration copy and does not publish its
assignments or change the selected configuration. Use `file-size` as the shadow
when testing demand ordering, or `disabled` to turn the comparison off.

The structured `baseline ordering comparison` log and the
`prow_job_dispatcher_predicted_plan` gauge expose predictions from each full
evaluation. The `role` label is `selected` or `shadow`. Provider-level measures
include `normalized_max`, `normalized_mean` and `max_mean_skew`; a skew of one
means equal normalized demand. Per-cluster `demand` includes idle inventory
clusters. Global measures include moved jobs/groups/demand, split relocatable
groups, missing placement metadata and demand routed outside active inventory
(which can include legitimate manual clusters). Labels contain no job names,
group names or policy digests.

`affinity_bound_jobs`/`affinity_bound_demand` and
`unbound_relocatable_jobs`/`unbound_relocatable_demand` expose the effect of the
freeze. Relocatable metadata does not guarantee that another eligible cluster
exists. `sampled_jobs` and `fallback_demand_jobs` distinguish currently configured
jobs with usable demand samples from minimum-demand fallback. The global
`evaluated_timestamp_seconds` measure records the last successful comparison.

These are demand-model predictions, not CPU utilization, queue seconds or proven
cost savings. Shadow evaluation uses the same affinity restrictions as the selected
plan. In conservative freeze mode, all configured Kubernetes groups are bound,
so changing their ordering normally produces identical placements.

## Demand freshness

`--prometheus-days-before=0` evaluates the run-count window at the current time.
An offset of 14 evaluates it at fourteen days ago, covering approximately days
21 through 14 before the query. The offset affects optional resource queries too.
Check metric coverage and the reason for an existing offset before changing it.

`--prometheus-demand-cache-ttl=1h` controls the maximum age of cached successful
queries, including empty responses. Values must be positive and no greater than
24 hours; the former fixed cache lifetime was 24 hours. A shorter cache does not
itself trigger a full baseline calculation. Weekly planning remains enabled.
`--rebalance-interval=6h` can add periodic full evaluations, but requires a durable
affinity store so more frequent planning cannot discard established placement.
Zero leaves the additional schedule disabled.

The following metrics distinguish query freshness from planning quality:

* `prowjob_dispatcher_demand_refresh_timestamp_seconds`, last successful refresh.
* `prowjob_dispatcher_demand_evaluation_timestamp_seconds`, evaluation time including the historical offset.
* `prowjob_dispatcher_demand_observed_jobs`, sampled job names before filtering to currently configured jobs.
* `prowjob_dispatcher_demand_query_errors_total`, failed refreshes.

For example, `time() - prowjob_dispatcher_demand_evaluation_timestamp_seconds`
shows input age. A zero timestamp means no successful query yet.

The existing `--prometheus-job-duration-query`, `--prometheus-job-cpu-query` and
`--prometheus-job-memory-query` flags accept vectors labelled by `job_name`, in
aggregate seconds, CPU seconds and byte-seconds respectively. Their weights are
`--job-duration-hour-weight`, `--job-cpu-hour-weight` and
`--job-memory-gb-hour-weight`. Use child build/test workload provenance for those
queries. Parent Prow-pod usage or a join against the namespace's latest job labels
does not accurately attribute shared builds. Charge shared work once per actual
build instance. No guessed resource queries are enabled by this change.

## Runtime colocation and namespace preservation

Runtime capacity and drain overrides move units identified by configuration group
and baseline cluster. Every member must have known relocation metadata and permit
relocation. A destination must satisfy every member's capabilities. Units move
atomically using summed demand; a capability scope that selects only part of a
unit is rejected. Fixed or legacy-unclassified members immobilize their unit.
The next full or delta classification migrates legacy metadata conservatively.

To preserve established namespace hosts with the current job-name-only Prow
protocol, set `--group-affinity-storage-path` to a file on durable storage. This
enables a **conservative group freeze**, not dynamic scheduling by namespace:

1. Before planning or publishing, classify jobs and seed bindings from their
   configured Prow fallback clusters. New jobs also retain their configured host,
   because Prow can use `Spec.Cluster` when the dispatcher is unavailable.
2. Require configured fallback, persisted baseline and cached runtime assignments
   to agree. Divergent or implicit fallback hosts are rejected before migration.
3. Retain group bindings, job aliases and deletion tombstones across full/delta
   refreshes, file renames, temporary overrides and restarts. New siblings inherit
   a compatible existing binding. Affinity preparation and planning consume one
   frozen job-configuration snapshot.
4. Persist bindings before replacing Gob assignments or publishing snapshots and
   generated fallback configuration. Changed restrictions that conflict with the
   retained host fail instead of silently moving the group.

Bindings do not expire automatically. A bound unit is immovable to runtime
overrides, and a drain requiring its relocation rejects. Keep one dispatcher
writer and preserve the affinity file with the other routing state. Do not delete
it as a routine reset. Moving or retiring a bound group requires an externally
coordinated admission pause, cache/in-flight-work drain and namespace check; this
change provides no automatic release command.

Before enabling freeze, reconcile any already-divergent runtime placement,
generated fallback configuration, cached decisions and warm namespaces. Bootstrap
checks cannot reconstruct historical placements or fence another process's cache.
Subsequent external changes to Prow fallback hosts must continue to respect the
bindings. The legacy Prow fallback protocol itself is unchanged.

This deliberately restricts balancing. Exact placement of new reusable input
cohorts requires a producer/resolver protocol: ci-operator only computes the full
namespace identity after Prow selects a cluster. A PR head SHA is insufficient.
The current implementation does not claim that finer-grained scheduling.

## Measuring actual outcomes

ci-operator continues uploading `ci-operator-metrics.json`. Its
`namespace_initialized` insight now includes `reused`, `namespace_uid` and
`input_hash`. Build and pod events include object UIDs; build events also include
creation time. These distinguish reused objects from separate executions and
allow shared work to be counted once. The older `namespace_created` insight fires
on reuse too and must not be used as a new-namespace counter.

Download representative before/after artifact directories and run:

```sh
go run ./cmd/dispatcher-performance \
  --baseline /path/to/before \
  --candidate /path/to/after > comparison.json
```

See [dispatcher-performance](../dispatcher-performance/README.md) for the output
fields and coverage rules. The report includes per-job and matched-job-only
aggregates, timing p50/p95 with sample counts, namespace reuse, deduplicated build
instances and build seconds, and pod scheduling latency. Old artifacts lacking
new fields count as missing coverage. Repeated build instances can include
legitimate retries or namespace expiry, so they are not automatically wasted
work. Summed build seconds are not CPU-hours or node-hours.

For Prow creation-to-pending p95, the existing exporter supports:

```promql
histogram_quantile(0.95,
  sum by (le, org, repo) (
    rate(prow_job_runtime_seconds_bucket{last_state="triggered",state="pending"}[1d])
  )
)
```

That histogram has no cluster label. Artifact pod scheduling latency measures a
different, later queue. Compare matched job families and similar revision/retry
mix over representative periods. Accept an improvement when actual waiting or
resource cost falls without reduced namespace/build reuse, placement violations
or excess movement; a lower predicted skew alone does not establish a speedup.
