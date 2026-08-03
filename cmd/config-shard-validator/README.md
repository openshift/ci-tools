# config-shard-validator

## What
Validates that ci-operator configuration files and Prow job configuration files are correctly sharded into ConfigMaps according to Prow's `config_updater` plugin rules. This ensures that every config file is automatically synced to the right ConfigMap in the cluster and that Prow job specs reference the correct ConfigMap shard for their `CONFIG_SPEC` environment variable.

This is a safety net: if a config file does not match any `config_updater` glob, or matches more than one, the ConfigMap sync will silently miss it or create conflicts.

## How it works -- full flow

### Startup
1. Parse flags: `--release-repo-dir` (root of openshift/release checkout), plus `--org`/`--repo`/`--log-level` for filtering.
2. Derive paths: ci-operator configs at `{release-repo-dir}/ci-operator/config`, Prow jobs at `{release-repo-dir}/ci-operator/jobs`, plugin config at `{release-repo-dir}/core-services/prow/02_config/_plugins.yaml`.
3. Load the Prow plugin config to get the `config_updater.maps` section, which defines glob-to-ConfigMap mappings.

### Phase 1: Collect paths and config info
4. Walk all ci-operator config files via `OperateOnCIOperatorConfigDir()`:
   - For each config, record the relative path (relative to release repo root) and the expected ConfigMap name (computed from org/repo metadata via `info.ConfigMapName()`)
   - Build a lookup map of config basenames to their `Info` for cross-referencing in Phase 2

5. Walk all Prow job config files via `OperateOnJobConfigDir()`:
   - For each job config file, record the relative path and expected ConfigMap name
   - For every presubmit, postsubmit, and periodic job in every file, inspect the `PodSpec`

### Phase 2: Validate PodSpec CONFIG_SPEC references
6. For each job's PodSpec containers, check for `CONFIG_SPEC` environment variables that reference a ConfigMap via `ValueFrom.ConfigMapKeyRef`:
   - Look up the referenced key in the config info map to find which config file it points to
   - If the key does not correspond to any known ci-operator config file, report an error
   - If the ConfigMap name in the reference does not match the expected ConfigMap shard for that config, report an error (e.g., a job referencing `ci-operator-misc-configs` when the config should be in `ci-operator-4.15-configs`)

### Phase 3: Validate glob coverage
7. Compile all `config_updater.maps` glob patterns using `zglob`.
8. Perform additional validations on the `config_updater.maps` themselves:
   - **No `default` cluster alias**: Glob entries must use explicit cluster names, not the `default` alias
   - **GZIP required for job configs**: Any glob matching `ci-operator/jobs` must have `gzip: true` set
9. For each collected path (both ci-operator configs and job configs), check against all compiled globs:
   - **Zero matches**: The file does not belong to any auto-updating ConfigMap -- it will not be synced. Error.
   - **Exactly one match**: Verify the matched glob's ConfigMap name equals the expected ConfigMap name. If not, the file would land in the wrong ConfigMap. Error.
   - **Multiple matches**: The file matches globs from more than one ConfigMap -- ambiguous sync target. Error for each match.

All glob checking runs concurrently using a producer-consumer pattern.

### Exit
If any validation failures were found, the tool exits with a fatal error. Exit code 0 means all config files are correctly mapped to exactly one ConfigMap and all job `CONFIG_SPEC` references point to the right shard.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--release-repo-dir` | (required) | Path to the root of the openshift/release repository checkout |
| `--org` | `""` | Limit validation to configs in this org |
| `--repo` | `""` | Limit validation to configs in this repo |
| `--log-level` | `info` | Log verbosity level |
| `--only-process-changes` | `false` | Only validate files modified vs. the upstream branch |

## Key files
- `cmd/config-shard-validator/main.go` -- entry point, all three validation phases, glob compilation and matching
- `pkg/config/options.go` -- shared `Options` for config directory walking with org/repo filtering
- `pkg/config/load.go` -- `Info.ConfigMapName()` computes expected ConfigMap shard name from config metadata
- `pkg/jobconfig/files.go` -- `Info.ConfigMapName()` computes expected ConfigMap shard name for job config files

## Deployment
Runs as a presubmit check on openshift/release PRs. Prevents merging config changes that would break the ConfigMap auto-sync mechanism.
