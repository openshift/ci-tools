# efs-cluster-writer

Converges the `cluster` field of Prow job files stored on EFS to the dispatcher's
published assignments.

## Why

The [Prow job dispatcher](../prow-job-dispatcher) plans every job in the Git job
tree. Jobs published to the EFS job-config volume are a separate tree: Prow reads
them, but until this tool existed the dispatcher did not, so a scheduling request
for one of those jobs missed the dispatcher and fell back to `default`, i.e. the
in-cluster app.ci API rather than a build cluster.

The dispatcher now reads the EFS tree read-only (`--read-only-jobs-dir`) and plans
those jobs alongside the Git ones. This tool materializes the result back onto EFS
as a `cluster:` field. EFS is therefore a *fallback cache*: the dispatcher remains
the source of truth, and a write lost to a race is repaired by the next pass.

The tool never creates, deletes or renames a job, and never changes a field other
than `cluster`.

## How a pass works

1. `GET /assignments?root=efs` returns the assignments for the EFS tree, the
   cluster inventory and the blocked clusters.
2. If a job on EFS has no assignment, the job was published after the dispatcher's
   last delta scan. The writer calls `POST /control/v1/rescan`, then re-reads.
3. Every job file under `--efs-root` is parsed and planned. Paths whose name starts
   with `..` are skipped, matching Prow's job loader; that is where the backups
   live.
4. A job is skipped when it has no assignment, when it already has the right
   value, or — if a write were needed — when its target cluster is blocked. A
   manual destination such as `vsphere02` is deliberately absent from the
   build-farm inventory, so inventory membership is not required.
5. The plan is checked against the move limit (see below). If it exceeds the limit,
   the whole pass is abandoned and nothing is written.
6. Each planned file is re-read immediately before writing. If the publisher
   rewrote it meanwhile, the change is recomputed against the newer content, so
   the publish is merged rather than overwritten; if the newer content needs no
   change, the file is left alone. The original is then backed up and the file
   rewritten atomically.
7. After writing, the file is read again. If it no longer matches what was written,
   the publisher replaced it again: the file is counted as deferred and the next
   pass re-applies the cluster value.

Steps 6 and 7 narrow the race but cannot remove it. Comparing and renaming cannot
be made atomic on a shared filesystem, so a publisher rename landing between the
final read and the writer's rename is still lost, and nothing on disk records what
it replaced. The window is the backup plus write of a single file rather than the
whole planning pass.

`--dry-run` runs steps 1 to 5 and logs the planned changes without writing.

The persisted value is `baselineCluster`, not the effective `cluster`: a temporary
`DispatchOverride` must not be baked into the fallback, because the file would keep
enforcing it after the override expires while the dispatcher has already reverted.

## Move limit

`--max-moves` and `--max-move-percent` bound how many existing placements one pass
may change, so a bad assignment cannot rewrite the whole tree. The effective limit
is the larger of the two, at least one.

Two categories are exempt:

- A job with an empty `cluster` has no placement yet. Filling it in is a first-time
  assignment, not a move, so seeding a newly published tree never trips the limit.
- Moving away from a cluster that is in the blocked set.

Anything else that would move more than the limit aborts the pass and increments
`efs_cluster_writer_circuit_breaker_total`.

## HTTP interface

| Path | Purpose |
|---|---|
| `POST /reconcile` | Run a pass now. Returns `202 Accepted`; the work is asynchronous. |
| `GET /status` | Last result. |
| `GET /healthz`, `GET /readyz` | Probes. |
| `GET /metrics` | Prometheus metrics. |

Passes are serialized: a trigger while a pass is running coalesces into one
follow-up pass, so the publisher's `curl -X POST .../reconcile` can never run two
reconcilers against the same tree.

There is no lock. The dispatcher is the source of truth and EFS `cluster:` is only
a fallback, so a lost update self-heals; the byte comparison above already prevents
the writer from clobbering concurrent publisher output.

## Metrics

| Metric | Meaning |
|---|---|
| `efs_cluster_writer_jobs_total{result}` | Jobs handled, by `missing`, `blocked`, `unchanged`, `updated`. |
| `efs_cluster_writer_files_changed_total` | Job files rewritten. |
| `efs_cluster_writer_last_generation` | Last dispatcher generation applied. |
| `efs_cluster_writer_reconcile_duration_seconds` | Pass duration. |
| `efs_cluster_writer_concurrent_writes_total` | Files deferred because they changed during the pass. |
| `efs_cluster_writer_circuit_breaker_total` | Passes aborted by the move limit. |

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--efs-root` | | Root of the EFS job-config tree. |
| `--backup-dir` | | Per-generation backups; must be under a `..`-prefixed path. |
| `--interval` | `5m` | Safety-net reconcile interval. |
| `--max-moves` | `5` | Minimum number of moved jobs allowed per pass. |
| `--max-move-percent` | `25` | Percentage of jobs that may move per pass. |
| `--dry-run` | `false` | Report changes without writing. |
| `--assignments-url` | `http://prowjob-dispatcher.ci.svc.cluster.local:8080/assignments` | Dispatcher bulk assignments endpoint. |
| `--rescan-url` | `http://prowjob-dispatcher.ci.svc.cluster.local:8080/control/v1/rescan` | Dispatcher delta rescan endpoint. |
| `--control-token-path` | | Bearer token for both dispatcher endpoints. |
| `--listen-address` | `:8080` | HTTP listen address. |

## Deployment

`clusters/app.ci/prow/03_deployment/efs-cluster-writer.yaml` in
[openshift/release](https://github.com/openshift/release). It runs as a single
replica with the `Recreate` strategy, because two reconcilers on the same tree
would duplicate work and race on the backups.
