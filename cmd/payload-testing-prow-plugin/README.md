# payload-testing-prow-plugin

## What
Prow webhook plugin that triggers release payload qualification tests against PR code. Users comment `/payload`, `/payload-job`, `/payload-aggregate`, or their `-with-prs` variants on a PR, and the plugin creates `PullRequestPayloadQualificationRun` (PRPQR) custom resources. These are reconciled by the `job-trigger-controller-manager` into actual ProwJobs that build a payload with the PR's changes and run the specified release qualification jobs against it.

This is how OpenShift developers validate that their changes do not break release-blocking or informing jobs before merging.

## User-facing commands
All triggered via PR comments:

| Command | What it does |
|---|---|
| `/payload <ocp> <release> <jobs>` | Run qualification jobs for a release (e.g., `/payload 4.10 nightly informing`) |
| `/payload-with-prs <ocp> <release> <jobs> <PRs...>` | Same, but include additional PRs in the payload |
| `/payload-job <job-name> [<job-name>...]` | Run specific named jobs (space-separated) |
| `/payload-job-with-prs <job-name> <PRs...>` | Run a specific job with additional PRs |
| `/payload-aggregate <job-name> <count>` | Run a job N times with aggregation |
| `/payload-aggregate-with-prs <job-name> <count> <PRs...>` | Aggregated run with additional PRs |
| `/payload-abort` | Abort all active payload jobs for this PR |

### Parameters
- `<ocp>`: OCP version like `4.10`, `4.14`
- `<release>`: `nightly`, `ci`, or `konflux-nightly`
- `<jobs>`: `blocking`, `informing`, `periodics`, or `all`
- `<PRs>`: Additional PRs in `org/repo#number` format
- `<count>`: Number of aggregated runs (integer)

### Constraints
- Only works on repos that promote official OpenShift images (checked via `PromotesOfficialImages`)
- The `-with-prs` commands can only be used once per comment (no multi-line batching)
- User must be trusted (org member or trusted GitHub App)
- Sharded jobs are automatically expanded to all shards when no specific shard suffix is provided

## How it works -- full flow

### On `/payload` command
1. **Trust check**: Verify the commenter is trusted for the repo. GitHub Apps listed in `--trusted-app` are also accepted.

2. **Validate repo**: Fetch the PR, resolve the ci-operator config for the repo's base branch, and verify it promotes official images. If not, respond that the repo does not contribute to OpenShift images.

3. **Resolve jobs**: For `/payload` commands, call the release controller's API to get the list of jobs matching the OCP version, release type (nightly/ci), and job category (blocking/informing/all). Filter out any jobs matching `SKIP_JOB_REGEX_*` environment variables (with `SKIP_JOB_EXPIRE_*` expiration dates).

4. **Resolve tests**: For `/payload-job` commands (no OCP/release specified), resolve each job name to its ci-operator test metadata by scanning all configs under `openshift` and `openshift-eng` orgs via the config agent.

5. **Handle sharding**: If a job has `ShardCount > 1` in its config and neither the user specified a shard suffix nor an aggregation count, automatically expand to all shards (e.g., `job-1of3`, `job-2of3`, `job-3of3`).

6. **Resolve additional PRs**: For `-with-prs` variants, fetch each additional PR's details (base ref, base SHA, head SHA, author, title).

7. **Build PRPQR**: Create a `PullRequestPayloadQualificationRun` custom resource with:
   - Labels: `dptp-requester: payload-testing`, org/repo/pull/baseRef/event-GUID labels
   - Name: `<event-GUID>-<counter>` (counter increments for multiple specs in one comment)
   - Spec contains: list of `ReleaseJobSpec` entries, release controller config (OCP/release/specifier), and `PullRequestUnderTest` entries for the origin PR and any additional PRs

8. **Create in Kubernetes**: Submit the PRPQR to the cluster. The `job-trigger-controller-manager` watches for these and creates actual ProwJobs.

9. **Post comment**: Post a comment listing the triggered jobs with a link to the payload-testing UI: `https://pr-payload-tests.ci.openshift.org/runs/<namespace>/<run-name>`. If additional PRs were included, also comment on those PRs with a cross-reference.

### On `/payload-abort`
1. List all PRPQRs for the PR using label selectors (org, repo, pull number).
2. For each PRPQR, find ProwJobs in triggered or pending state.
3. Abort each active ProwJob by setting `status.state = Aborted`.
4. For aggregator jobs (identified by the `aggregator-` prefix and `AggregationIDLabel`), also abort all aggregated job runs sharing the same aggregation ID.

### On PR close/merge
Automatically prune (delete) all PRPQRs associated with the closed PR to prevent accumulation of stale resources.

### Job skip mechanism
Environment variables control temporary job skips:
- `SKIP_JOB_REGEX_<N>`: regex pattern matching job names to skip
- `SKIP_JOB_EXPIRE_<N>`: RFC3339 expiration date for the skip

Expired skips produce a warning log but do not block jobs.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--log-level` | `info` | Log verbosity |
| `--hmac-secret-file` | `/etc/webhook/hmac` | GitHub webhook HMAC secret |
| `--namespace` | `ci` | Namespace for PRPQR creation |
| `--ci-op-config-dir` | (required) | Path to CI operator configuration directory |
| `--release-repo-git-sync-path` | `/var/repo/release` | Path to git-synced openshift/release repo for config agent |
| `--trusted-app` | (none) | GitHub App slug allowed to issue /payload (repeatable) |

Standard Prow GitHub flags, Kubernetes flags, and `githubeventserver.Options` are also supported.

## Key files
- `cmd/payload-testing-prow-plugin/main.go` -- entry point, flag parsing, kube client setup, config agent initialization with universal symlink watcher
- `cmd/payload-testing-prow-plugin/server.go` -- webhook handlers, command parsing (7 regex patterns), PRPQR building, abort logic, PR close pruning, error formatting
- `cmd/payload-testing-prow-plugin/rcjobresolver.go` -- release controller job resolver: fetches job lists from release controller API, applies skip filters
- `cmd/payload-testing-prow-plugin/filetestresolver.go` -- resolves job names to ci-operator test metadata by scanning config files

## Deployment
Long-lived webhook Deployment on app.ci. Requires:
- Kubeconfig with access to the app.ci cluster (for PRPQR CRUD)
- Network access to the release controller API
- Git-synced copy of openshift/release at `--release-repo-git-sync-path`

Listens for GitHub `issue_comment` and `pull_request` (closed) events.

## Related
- `cmd/payload-testing-ui` -- read-only web UI displaying PRPQR results
- `cmd/job-trigger-controller-manager` -- reconciles PRPQR objects into actual ProwJobs
- [Docs](https://docs.ci.openshift.org/release-oversight/payload-testing/)
