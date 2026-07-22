The `multi-pr-prow-plugin` is an external prow plugin that facilitates running presubmit tests
from sources built using multiple pull requests. The included pull requests can be from the same, or a different, repo.
It creates and manages GitHub `check_runs` to keep share the state and logs of the jobs with the user.

# multi-pr-prow-plugin

## What
Prow webhook plugin that enables testing changes from multiple pull requests together in a single CI job. When a user comments `/testwith <org>/<repo>/<branch>/<test> <org>/<repo>#<number> ...`, the plugin constructs a ProwJob (as a periodic) that checks out code from all specified PRs and runs the requested test against the combined sources. This is essential for validating cross-repository changes that must land together.

Results are reported back to the origin PR as GitHub Check Runs.

## User-facing commands
All triggered via PR comments:

| Command | What it does |
|---|---|
| `/testwith <org>/<repo>/<branch>/<test> <PRs...>` | Run the named test with code from multiple PRs |
| `/testwith <org>/<repo>/<branch>/<variant>/<test> <PRs...>` | Same, but for a variant-qualified test |
| `/testwith abort` | Abort all active multi-PR jobs where this PR is the origin |

PR references use the format `org/repo#number`. Full GitHub URLs (`https://github.com/org/repo/pull/123`) are also accepted and auto-converted. Up to 20 PRs can be included per command. Multiple `/testwith` lines in a single comment are supported.

### Examples
```
/testwith openshift/kubernetes/master/e2e openshift/kubernetes#1234 openshift/installer#999
/testwith openshift/origin/master/gcp/e2e-gcp openshift/origin#5678
/testwith abort
```

## How it works -- full flow

### On `/testwith` command
1. **Trust check**: Verify the commenter is a trusted user for the repo via Prow's `TrustedUser` mechanism. Untrusted users are rejected with an error comment.

2. **Parse command**: Extract the job specification from the comment. The job spec format is `<org>/<repo>/<branch>/<test>` or `<org>/<repo>/<branch>/<variant>/<test>`. Also extract the list of additional PRs.

3. **Resolve PRs**: For each referenced PR, call `GetPullRequest` to get its current state (base ref, head SHA, user login, title). For PRs targeting the same org/repo as the test, normalize the base branch to the job metadata's branch to handle renamed default branches (e.g., `master` to `main`).

4. **Resolve CI config**: Call the ci-operator config resolver service to get the `ReleaseBuildConfiguration` for the test's org/repo/branch/variant. Find the matching test definition by `As` name.

5. **Build ProwJob**: Generate a periodic-type ProwJob using `prowgen`:
   - Inject `--test-from` via `InjectTestFrom` to run the specific test from the resolved config
   - Set a custom hash input matching the job name for deterministic naming
   - Query the prowjob-dispatcher for cluster assignment (results cached for 15 minutes)
   - Enforce a minimum timeout of 8 hours (`defaultMultiRefJobTimeout`)
   - Build `Refs` and `ExtraRefs` from all PRs, grouped by base org/repo/branch. The test's own org/repo becomes the primary `Refs`; all others become `ExtraRefs`. Path aliases are resolved via the config resolver
   - Fetch base SHAs via `GetRef("heads/<branch>")` for each ref
   - Apply Prow defaults via `DefaultPeriodic`

6. **Submit ProwJob**: Create the ProwJob in Kubernetes in the configured namespace with a `ci.openshift.io/testwith` label encoding the origin PR.

7. **Report via Check Runs**: Asynchronously wait for the ProwJob to get a status URL (polling every 5s for up to 60s), then create a GitHub Check Run on the origin PR with `status: in_progress`. The Check Run body links to the job logs and lists all included PRs.

### Job naming
Jobs are named: `multi-pr-<origin-org>-<origin-repo>-<origin-number>-[<additional-pr-parts>-]<test-name>`

### On `/testwith abort`
1. List ProwJobs matching the origin PR's org, repo, number, and the `ci.openshift.io/testwith` label.
2. Set `status.state = Aborted` on all non-complete jobs via `Update` (not Patch, to avoid overwriting concurrent state changes by the ProwJob controller).

### Reporter sync loop
A background goroutine runs every `--job-sync-seconds` (default 60s). For each tracked job:
- Fetch the ProwJob's current state from Kubernetes
- If completed (success, failure, error, aborted), update the Check Run with the mapped conclusion (`success`/`failure`/`failure`/`cancelled`) and remove the job from tracking
- If the ProwJob is not found, remove from tracking
- If the job was created over 25 hours ago, remove from tracking (stale entry)

Job tracking state is persisted to a JSON file (`--job-config`) for crash recovery.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--log-level` | `info` | Log verbosity |
| `--hmac-secret-file` | `/etc/webhook/hmac` | GitHub webhook HMAC secret |
| `--namespace` | `ci` | Namespace for ProwJob creation |
| `--ci-op-config-dir` | (required) | Path to CI operator configuration directory |
| `--dispatcher-address` | `http://prowjob-dispatcher.ci.svc.cluster.local:8080` | Address of the prowjob-dispatcher for cluster assignment |
| `--job-config` | (required) | Path to JSON file tracking active Check Runs |
| `--job-sync-seconds` | `60` | Interval in seconds between Check Run sync cycles |

Standard Prow config flags (`--config-path`), GitHub flags, Kubernetes flags, and `githubeventserver.Options` are also supported.

## Key files
- `cmd/multi-pr-prow-plugin/main.go` -- entry point, flag parsing, kube client setup, reporter and server initialization
- `cmd/multi-pr-prow-plugin/server.go` -- webhook handler, command parsing, ProwJob generation (config resolution, ref building, cluster dispatch), abort logic
- `cmd/multi-pr-prow-plugin/report.go` -- Check Run reporter: creation on job start, periodic sync loop, state-to-conclusion mapping, JSON config persistence

## Deployment
Long-lived webhook Deployment on app.ci. Requires:
- Kubeconfig with access to the app.ci cluster (for ProwJob CRUD)
- GitHub App or token with Check Run permissions
- Network access to the prowjob-dispatcher and ci-operator config resolver services

Listens for GitHub `issue_comment` events.

## Related
- `cmd/job-trigger-controller-manager` -- reconciles PRPQR objects into ProwJobs (used by payload-testing, not this plugin)
