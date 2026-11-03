# ci-operator startup diagnostics

The namespace-artifact cleanup hook writes `namespace/node-startup.json` alongside
the existing namespace artifacts. It runs after graph execution returns, including
release-import failures before dependent tests start, and on handled interruption.
It is registered before graph execution, not before configuration resolution or
namespace initialization. If ci-operator itself cannot start, it cannot collect
this artifact; those failures require Prow/build-farm diagnostics.

The artifact is best-effort and does not change the execution result. The hook has
a separate 20-second cleanup deadline, including existing namespace API reads.
Node/event requests also have two-second deadlines. It reuses the namespace pod
and event snapshots and never queries other workloads' pods.

## Schema and limits

- `status`: namespace pod-read status, `missing_run_identity`, `no_relevant_nodes`,
  or `collected`.
- `pod_events_status`: availability of the reused namespace event snapshot.
- `pods`: at most 32 own-namespace associations (`name`, `node`, `status`). Status
  is `failed_start` or `missing_assignment` for unassigned Pending pods. Assigned
  pods require a startup failure reason in Pending/Failed status, a waiting
  container, or a recent Warning event referring to that exact pod UID. Running,
  previously running, and application-failed containers are excluded.
  Selection requires the existing created-by-CI and job-ID labels to match the
  current run, using normal ci-operator label sanitization. Namespace reuse alone
  does not establish ownership. Missing run identity fails closed without node
  queries; unlabeled and other-run pods are excluded.
- `nodes`: at most three distinct assigned nodes. Each has a read `status`, standard
  Ready/MemoryPressure/DiskPressure/PIDPressure condition status and heartbeat/
  transition times, `events_status`, and at most 20 runtime Warning projections.
  Event projections contain only allowlisted `reason`, canonical `source`,
  `count`, `first`, and `last` observation times. Events must refer to the exact
  node name/UID and have kubelet provenance. Pod events are not emitted.
- Node event reads use object/type selectors, at most two 100-event pages per
  node, and a 15-minute lookback. Events outside the window, including future
  observations, are excluded locally even if the API ignores selectors.
  Selection examines at most 1,000 pods and 1,000 namespace events from the reused
  snapshots; incomplete snapshots are flagged as truncated.
- Read statuses distinguish `available`, `forbidden`, `unauthorized`,
  `unavailable`, `canceled`, and `timeout`, without raw API errors.
- `truncated` flags incomplete associations, nodes, or events; individual nodes
  also flag truncated events. Names are censored before encoding and bounded to
  253 bytes. Output is capped at 64 KiB; node details, then associations, are
  dropped if needed.
- `journals` explicitly reports `unavailable: no approved sanitized collection
  path`. Kubernetes Events are not kubelet/CRI-O journals and do not establish
  root cause. No node log/proxy access, privileged workloads, journal downloads,
  mutations, RBAC grants, or speculative retries are added.

Node labels, annotations, addresses, provider/image IDs, arbitrary condition/event
messages, and pod specs/environment are never projected. Known-secret censoring
is applied before JSON encoding as well as through the standard artifact writer;
it is defense in depth, not a substitute for the field allowlist. Access uses the
existing ci-operator identity; unavailable or denied observations are not evidence
of node health. Cancellation can race pod deletion, so a snapshot is not a complete
history of every pod created by a run.
