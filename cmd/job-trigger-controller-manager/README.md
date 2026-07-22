# job-trigger-controller-manager

## What
Kubernetes controller that reconciles `PullRequestPayloadQualificationRun` (PRPQR) custom resources into ProwJobs. This is the execution engine for payload qualification testing: when a PRPQR is created (typically by the `payload-testing` Prow plugin), this controller resolves the ci-operator configuration, generates the appropriate ProwJobs, creates them in the cluster, and tracks their status back to the PRPQR status.

## How it works -- full flow

### Controller setup
1. Registers a controller-runtime manager watching PRPQR objects in the configured namespace (default: `ci`)
2. Also registers a `pjstatussyncer` sub-controller that watches ProwJobs and syncs their status back to the owning PRPQR
3. Watches for PRPQR create and update events only (not deletes)

### Reconciliation loop
When a PRPQR is created or updated:

1. **Fetch existing state**: Get the PRPQR and list any ProwJobs already created for it (matched by the `pullrequestpayloadqualificationrun` label)

2. **Handle deletion**: If the PRPQR is being deleted, abort all associated ProwJobs by setting their state to `Aborted`

3. **Trigger jobs**: For each job spec in `prpqr.Spec.Jobs.Jobs`:
   - Skip if a ProwJob already exists for this job (matched by name hash)
   - Resolve the ci-operator config by calling the config-resolver service, injecting the test from `MetadataWithTest`
   - For aggregated jobs (`AggregatedCount > 0`): generate an aggregator ProwJob plus N child ProwJobs
   - For multi-ref jobs: generate a ProwJob that tests multiple PRs together
   - For regular jobs: generate a single ProwJob
   - Query the prowjob-dispatcher for cluster assignment
   - Create the ProwJob(s) in the cluster
   - Wait `--job-trigger-wait-seconds` (default 20s) after creation to let the ProwJob controller pick it up

4. **Build ProwJob specs**: Each ProwJob is built by:
   - Creating a `periodic` Prow job spec with the resolved ci-operator config
   - Setting extra refs for the PR(s) under test
   - Applying Prow config defaults
   - Adding labels linking back to the PRPQR

5. **Update PRPQR status**: Write job statuses (ProwJob name, state, description) back to the PRPQR's `.status.jobs` field. Set the `AllJobsTriggered` condition when complete.

### ProwJob status syncer
A separate sub-controller (`pjstatussyncer`) watches ProwJobs with the PRPQR label and syncs status changes back to the parent PRPQR. This keeps the PRPQR status up-to-date as jobs progress through Pending, Running, Success, Failure, etc.

### Job timeout
Aggregator and multi-ref jobs have configurable timeouts (`--aggregator-job-timeout`, `--multi-ref-job-timeout`). Jobs that exceed their timeout are marked as timed out in the PRPQR status.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--dry-run` | `true` | Dry-run mode: does not create real ProwJobs |
| `--namespace` | `ci` | Namespace to watch for PRPQR objects |
| `--job-trigger-wait-seconds` | `20` | Seconds to wait after creating a ProwJob for status to appear |
| `--aggregator-job-timeout` | `6` | Hours before an aggregator job is considered timed out |
| `--multi-ref-job-timeout` | `6` | Hours before a multi-ref job is considered timed out |
| `--dispatcher-address` | `http://prowjob-dispatcher.ci.svc.cluster.local:8080` | Address of the prowjob-dispatcher service |
| `--config-path` | (Prow) | Prow config path (for job defaults) |

## Key files
- `cmd/job-trigger-controller-manager/main.go` -- entry point, controller-runtime manager setup
- `pkg/controller/prpqr_reconciler/prpqr_reconciler.go` -- reconciler logic: job generation, creation, status management
- `pkg/controller/prpqr_reconciler/pjstatussyncer/` -- ProwJob-to-PRPQR status sync sub-controller
- `pkg/api/pullrequestpayloadqualification/v1/` -- PRPQR CRD types

## Related
- `cmd/payload-testing-prow-plugin` -- creates PRPQR objects this controller reconciles
- `cmd/multi-pr-prow-plugin` -- also creates PRPQR objects

## Deployment
Long-lived controller-runtime Deployment on app.ci in the `ci` namespace. Requires in-cluster access with permissions to create/list/update ProwJobs and PRPQR resources.

Uses the ci-operator config resolver service (`https://config.ci.openshift.org`) and the prowjob-dispatcher for cluster assignment.
