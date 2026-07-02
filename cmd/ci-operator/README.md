# ci-operator

## What
Core CI execution engine. Every OpenShift CI job runs inside ci-operator. It reads a declarative YAML config, builds a DAG of steps (source clone, image builds, test execution, promotion), creates an ephemeral namespace, executes the graph with maximum parallelism, collects artifacts, and tears down.

This is the beating heart of OpenShift CI: thousands of jobs per day, all driven by this single binary.

## How it works — full flow

### 1. Startup
1. Read config from: `--config` flag > `CONFIG_SPEC` env var (supports base64+gzip) > `CONFIG_SPEC_GCS_URL` > configresolver API
2. Resolve job spec from `JOB_SPEC` environment variable (Prow injects this into every job pod)
3. Set up dual logging: human-friendly console on stdout + JSON file (`ci-operator.log` in artifacts dir)
4. Create `secrets.DynamicCensor` for automatic credential censoring in all output
5. Initialize MetricsAgent with plugins: insights, events, builds, nodes, leases, pods, machines, images

### 2. Namespace lifecycle
When `--namespace` is not set, the namespace defaults to `ci-op-{id}` where `{id}` is replaced with a hash (SHA256 of all inputs, base32-encoded, 5 bytes). When `--namespace` is explicitly provided, `{id}` substitution only applies if the provided value contains the literal string `{id}`.

Steps:
1. ProjectRequest creation with retry loop (waits for TerminatingPhase to clear if a stale namespace exists)
2. RBAC readiness check: SelfSubjectAccessReview for "create rolebindings" (30 retries, 1s each)
3. Annotations: `ci.openshift.io/idle-cleanup-duration-ttl` (default 1h), `ci.openshift.io/cleanup-duration-ttl` (default 72h)
4. Heartbeat goroutine updates `ci.openshift.io/namespace-last-active` every 10 minutes
5. Wait for ServiceAccount imagePullSecrets (299 retries, 1s each = ~5 min timeout)
6. PR author access: RoleBinding `ci-op-author-access` granting admin to `{author}-group` group
7. Secrets: pull, push, upload, clone auth (SSH or OAuth), external image pull, promotion kubeconfig
8. Pipeline ImageStream creation with local lookup policy
9. PodDisruptionBudget: maxUnavailable=0 for all CI pods

### 3. Step graph
The core abstraction. Every action is a Step with:
- `Requires()` — dependency StepLinks (image tags, parameters, etc.)
- `Creates()` — output StepLinks
- `Run(ctx)` — execution

Step types include: source clone, binary build, test-binary build, RPM build, image builds (BuildConfig-based), multi-stage tests (pre/test/post phases), input image tags (external imports), output image tags, index generation, bundle builds.

`BuildGraph()` creates the full DAG. `BuildPartialGraph()` creates a subset for named targets. `TopologicalSort()` detects cycles and orders execution.

Execution (`steps.Run()`): launches goroutines per step, DAG-scheduled. When a step completes, its output links are marked satisfied, unblocking children. Context cancellation propagates to all steps.

### 4. Multi-stage tests
Three phases executed in order:
- **pre**: Setup steps. Short-circuits to post on failure.
- **test**: Main test steps. Short-circuits to post on failure.
- **post**: Cleanup. Best-effort by default — failures don't fail the overall test.

Each step runs in its own pod via entrypoint-wrapper. Key mounts:
- `/var/run/secrets/ci.openshift.io/cluster-profile` — cloud credentials
- `/var/run/secrets/ci.openshift.io/multi-stage` — shared dir snapshot
- `/cli` — oc/kubectl binaries
- `/var/run/configmaps/ci.openshift.io/multi-stage` — step command script

Supports: observers (concurrent monitoring pods), VPN sidecar injection (from `vpn.yaml` in cluster profile), Google Secret Manager via CSI driver, cluster claims from Hive pools.

### 5. Lease management (Boskos)
- Owner: `{namespace}-{jobSpecHash}`
- `Acquire()`: blocks up to 120 minutes, retries 60 times
- `Heartbeat()`: every 30 seconds in background goroutine. Persistent failure cancels all dependent steps.
- `ReleaseAll()` on cleanup

### 6. Promotion
After tests pass, if `--promote` flag set:
- Runs promotion steps concurrently via goroutines
- Each target gets its own promotion step
- Channel-based result collection
- Any promotion failure fails the whole job

### 7. Artifacts
- `ARTIFACT_DIR` env var mounted in test pods
- Post-completion: `tar czf` streams artifacts from pod to local storage
- Searches for `custom-prow-metadata.json` and merges into final `metadata.json`
- Per-container JUnit via `ci-operator.openshift.io/container-sub-tests` annotation

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--config` | — | Path to ci-operator config YAML |
| `--unresolved-config` | — | Unresolved config for configresolver |
| `--namespace` | `ci-op-{id}` | Namespace to use. If empty, defaults to `ci-op-{id}`. The literal `{id}` is replaced with the input hash. |
| `--lease-server` | app.ci boskos URL | Boskos server address |
| `--lease-server-credentials-file` | — | Format: `username:password` |
| `--promote` | false | Enable image promotion after tests |
| `--pod-pending-timeout` | 60m | Max time waiting for pod to start |
| `--delete-when-idle` | 1h | Idle TTL before namespace cleanup |
| `--delete-after` | 72h | Hard TTL before namespace cleanup |
| `--give-pr-author-access-to-namespace` | true | Grant PR author admin in test namespace |
| `--restrict-network-access` | false | Egress firewall to 10.0.0.0/8 |
| `--enable-secrets-store-csi-driver` | false | GSM secret injection via CSI |
| `--registry` | — | Step registry path for local resolution |
| `--node` | — | Restrict pod scheduling to named node |
| `--target` | (repeatable) | Select specific build targets to run |
| `--print-graph` | false | Print the step DAG and exit without running |
| `--secret-dir` | (repeatable) | Inject secret directory into test namespace |
| `--ssh-key-path` | — | SSH key for private repo cloning |
| `--oauth-token-path` | — | OAuth token for private repo cloning |
| `--resolver-address` | (configresolver URL) | Config resolver API address |
| `--org` | — | Organization for config resolver lookup |
| `--repo` | — | Repository for config resolver lookup |
| `--branch` | — | Branch for config resolver lookup |
| `--variant` | — | Variant for config resolver lookup |
| `--multi-stage-param` | (repeatable) | Override multi-stage environment parameters |
| `--dependency-override-param` | (repeatable) | Override step dependencies with pull specs |
| `--write-params` | — | Write env-compatible output file |
| `--git-ref` | — | Populate job spec from local git reference |
| `--lease-acquire-timeout` | 120m | Max time to wait for lease acquisition |

## Key env vars
| Variable | What it does |
|---|---|
| `JOB_SPEC` | Prow-injected JSON with org, repo, PR number, base/head SHA, job type |
| `CONFIG_SPEC` | Inline ci-operator config (supports base64+gzip encoding) |
| `CONFIG_SPEC_GCS_URL` | GCS URL to fetch ci-operator config from |
| `UNRESOLVED_CONFIG` | Unresolved config (needs registry resolution) |
| `ARTIFACT_DIR` | Directory for test artifacts, mounted into test pods |

## Gotchas
- The namespace TTL (`--delete-after`) controls cleanup — if a job is killed, the namespace may linger until TTL expires
- `--pod-pending-timeout` (default 60m) controls how long to wait for pods to schedule before failing
- Promotion only happens when `--promote` is passed AND the job succeeds
- Multi-stage test steps are wrapped by `entrypoint-wrapper`
- Namespace hash is deterministic from inputs — rerunning the same job may reuse a namespace if the previous one hasn't been cleaned up yet (waits for TerminatingPhase)

## Key files
- `cmd/ci-operator/main.go` — entry point, namespace lifecycle, flag parsing (~2600 lines)
- `pkg/api/types.go` — ReleaseBuildConfiguration schema (~1900 lines)
- `pkg/api/graph.go` — Step interface, StepLink, BuildGraph, TopologicalSort
- `pkg/steps/run.go` — concurrent DAG executor
- `pkg/steps/multi_stage/multi_stage.go` — multi-stage test orchestration
- `pkg/steps/pod.go` — PodStep (single container test)
- `pkg/steps/source.go` — source clone step
- `pkg/steps/project_image.go` — BuildConfig-based image builds
- `pkg/steps/lease.go` — LeaseStep wrapper
- `pkg/steps/artifacts.go` — artifact collection and JUnit
- `pkg/lease/client.go` — Boskos lease client
- `pkg/defaults/defaults.go` — graph generation from config (FromConfig)
- `pkg/metrics/` — MetricsAgent and plugins

## Deployment
Not deployed as a service. Runs as the main process inside every Prow job pod. ci-operator is the binary that Prow invokes. The binary is baked into the test pod image.

## Related
- `cmd/entrypoint-wrapper` — wraps each multi-stage test step
- `cmd/ci-operator-configresolver` — serves configs to ci-operator at runtime
- ci-docs: `architecture/ci-operator.md` — deep dive on config and execution model
- ci-docs: `architecture/step-registry.md` — multi-stage test architecture
