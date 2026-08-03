# ci-operator-configresolver

## What
Long-running HTTP service that loads, resolves, and serves ci-operator configurations and the multi-stage step registry on demand. It is the central config resolution service for OpenShift CI: `ci-operator` calls it at runtime to get its fully-resolved configuration (with all step registry references expanded inline).

It also hosts a web UI (the "Step Registry" browser) on a separate port where users can browse, search, and view documentation for steps, chains, workflows, and job configurations.

## How it works -- full flow

### Startup
1. Parse and validate flags
2. Add the OpenShift `imagev1` scheme (needed for ImageStream lookups)
3. Initialize config and registry agents:
   - **Config agent** (`agents.NewConfigAgent`): loads all ci-operator YAML configs from `--config` (or `{release-repo-path}/ci-operator/config/`)
   - **Registry agent** (`agents.NewRegistryAgent`): loads the multi-stage step registry from `--registry` (or `{release-repo-path}/ci-operator/step-registry/`)
4. If `--release-repo-git-sync-path` is used, both agents share a single `UniversalSymlinkWatcher` that watches for git-sync symlink changes and triggers reload of both configs and registry simultaneously
5. If `--validate-only` is set, exit after loading (used for CI validation of configs)
6. Create a Kubernetes client for ImageStream lookups (in-cluster config)
7. Start two HTTP servers on separate ports

### API server (port 8080 by default)

| Endpoint | Method | What it does |
|---|---|---|
| `/config` | GET | Resolve a stored config by metadata query params (`org`, `repo`, `branch`, `variant`). Looks up the config, resolves all registry references, returns fully-resolved JSON. |
| `/resolve` | POST | Resolve a literal (inline) config. Accepts an unresolved `ReleaseBuildConfiguration` JSON in the request body, resolves registry references, returns resolved JSON. |
| `/mergeConfigsWithInjectedTest` | GET | Merge multiple configs (specified via repeated query params) and inject a test from one config into the merged result. Used for cross-repo test injection. |
| `/clusterProfile` | GET | Return details about a cluster profile by `name` query param. |
| `/configGeneration` | GET | Return the current generation counter for configs (increments on reload). |
| `/registryGeneration` | GET | Return the current generation counter for registry (increments on reload). |
| `/integratedStream` | GET | Return information about an integrated ImageStream by `namespace` and `name` query params. Responses are cached in memory with 1-minute TTL. Validates against an allowlist of stream patterns (e.g. `ocp/4.12+`, `origin/4.12+`, `ocp-private/4.12+`, `origin/scos-*`, `origin/sriov-*`, `origin/metallb-*`, `origin/ptp-*`). |
| `/readyz` | GET | Readiness probe (always 200). |

### UI server (port 8082 by default)

| Path | What it shows |
|---|---|
| `/` | Main page listing all references, chains, and workflows in the registry |
| `/search` | Search across all configs by org/repo/branch/test name |
| `/job` | View a specific job's resolved config with all steps expanded |
| `/reference/{name}` | View a specific step reference with documentation, code, and usage |
| `/chain/{name}` | View a specific chain with its step sequence and documentation |
| `/workflow/{name}` | View a specific workflow with pre/test/post chains and documentation |
| `/ci-operator-reference` | Syntax-highlighted YAML reference for ci-operator configuration |
| `/static/...` | Static assets (CSS, JS) |

### Config resolution flow (what `/config` does internally)
1. Extract `org`, `repo`, `branch`, `variant` from query parameters
2. Look up the matching config via `configAgent.GetMatchingConfig()` (supports regex matching on branch names)
3. Call `registryAgent.ResolveConfig()` which expands all step registry references:
   - Inline the commands from referenced steps
   - Expand chains into their constituent steps
   - Expand workflows into pre/test/post step sequences
   - Resolve cluster profile references
4. Return the fully-resolved config as indented JSON

### Integrated stream cache
The `/integratedStream` endpoint fetches ImageStream data from the cluster and caches it in memory for 1 minute to avoid excessive API calls. The cache key is `{namespace}/{name}`. Concurrent access is protected by a mutex.

### File watching and hot-reload
- When `--release-repo-git-sync-path` is set, a single `fsnotify` watcher monitors the git-sync symlink. When git-sync updates the symlink (on new commits), both the config agent and registry agent reload simultaneously.
- When `--config` and `--registry` are set separately, each agent watches its own directory independently via `ConfigMap` mount watchers.
- Each reload increments a generation counter accessible via `/configGeneration` and `/registryGeneration`.

### Metrics
- Prometheus metrics exposed on the default metrics port under `ci-operator-configresolver` prefix
- HTTP request duration and response size tracked per endpoint
- Error rate counters for config resolution failures

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--config` | `""` | Path to ci-operator config directory (mutually exclusive with `--release-repo-git-sync-path`) |
| `--registry` | `""` | Path to step registry directory (mutually exclusive with `--release-repo-git-sync-path`) |
| `--release-repo-git-sync-path` | `""` | Path to a git-synced release repo; derives config and registry paths automatically |
| `--log-level` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `--port` | `8080` | API server port |
| `--ui-port` | `8082` | UI server port |
| `--address` | `:8080` | DEPRECATED: use `--port` |
| `--ui-address` | `:8082` | DEPRECATED: use `--ui-port` |
| `--gracePeriod` | `10s` | Grace period for server shutdown |
| `--validate-only` | `false` | Load and validate configs/registry, then exit |
| `--flat-registry` | `false` | Disable directory-structure-based registry validation |
| Instrumentation flags | | `--health-port`, metrics port, etc. |

## Key files
- `cmd/ci-operator-configresolver/main.go` -- entry point, server setup, endpoint wiring, integrated stream cache
- `pkg/registry/server/server.go` -- HTTP handler implementations for `/config`, `/resolve`, `/mergeConfigsWithInjectedTest`, `/clusterProfile`
- `pkg/webreg/webreg.go` -- web UI handler (`WebRegHandler`) with routing for `/`, `/search`, `/job`, `/reference`, `/chain`, `/workflow`
- `pkg/load/agents/configAgent.go` -- config loading agent with file watching
- `pkg/load/agents/registryAgent.go` -- registry loading agent with file watching
- `pkg/api/configresolver/` -- `LocalIntegratedStream()` for ImageStream lookups

## Deployment
Long-lived Deployment on `app.ci`, namespace `ci`. Two ports exposed:
- Port 8080: API (consumed by `ci-operator` at runtime)
- Port 8082: UI (the Step Registry browser, accessible to users)

Health check: readiness probe hits `/readyz` on the API port. The health endpoint gates on the API server being responsive.

Uses git-sync sidecar to keep a local copy of `openshift/release` up to date.

## Related
- `cmd/ci-operator` -- primary consumer of the `/config` and `/resolve` API endpoints
- `cmd/generate-registry-metadata` -- generates metadata consumed by the UI
- The public UI is available at `https://steps.ci.openshift.org/`
