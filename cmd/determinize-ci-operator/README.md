# determinize-ci-operator

## What
Normalizes ci-operator configuration YAML files to enforce consistent, deterministic formatting. This is a pure formatting tool -- it does not change semantics. Every config file is read, unmarshalled into Go structs, and re-serialized back to YAML, which eliminates formatting inconsistencies like field ordering, whitespace, and quoting differences.

The tool also ensures that each config file's `zz_generated_metadata` field matches the metadata derived from its filesystem path (org/repo/branch/variant), treating the filepath as the source of truth.

## How it works -- full flow

1. Parse flags via `ConfirmableOptions`: `--config-dir`, `--confirm`, `--org`, `--repo`, `--log-level`, `--only-process-changes`
2. Walk all ci-operator config files in `--config-dir` using `OperateOnCIOperatorConfigDir()`:
   - Each `.yaml`/`.yml` file under the config directory is loaded and unmarshalled into a `ReleaseBuildConfiguration` struct
   - Files are filtered by `--org`/`--repo` if specified
   - If `--only-process-changes` is set, only files modified relative to the upstream branch are processed
3. For each config file:
   - If `--confirm` is **not** set: log "Would re-format file" and skip (dry-run mode)
   - If `--confirm` is set: overwrite the config's `Metadata` field with the metadata extracted from the filepath, then collect for writing
4. After walking all configs, batch-write all collected configs back to disk via `CommitTo()`:
   - Marshal the `ReleaseBuildConfiguration` to YAML using `github.com/ghodss/yaml` (which produces deterministic output)
   - Write to the canonical filepath derived from the config's metadata: `{config-dir}/{org}/{repo}/{org}-{repo}-{branch}[__variant].yaml`

### What "determinize" means in practice
- Field ordering is fixed by Go struct tag ordering
- YAML formatting (indentation, quoting, flow vs block style) is standardized by the marshaller
- The `zz_generated_metadata` block is always regenerated from the filepath, fixing any drift
- Empty/nil fields are omitted consistently via `omitempty` tags

### Dry-run vs. confirm
Without `--confirm`, the tool only logs what it would do without writing any files. This is useful for CI checks that verify configs are already determinized (if the tool would change anything, the check fails).

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--config-dir` | (required) | Path to the ci-operator configuration directory |
| `--confirm` | `false` | Actually write reformatted files; without this, dry-run only |
| `--org` | `""` | Limit to configs in this GitHub org |
| `--repo` | `""` | Limit to configs in this GitHub repo |
| `--log-level` | `info` | Log verbosity level |
| `--only-process-changes` | `false` | Only process files modified vs. the upstream branch |

## Key files
- `cmd/determinize-ci-operator/main.go` -- entry point, walks configs and re-serializes them
- `pkg/config/options.go` -- `ConfirmableOptions` struct with `--confirm` flag, `OperateOnCIOperatorConfigDir()` for filtered walking
- `pkg/config/load.go` -- `DataWithInfo.CommitTo()` serializes and writes a config to its canonical path

## Deployment
CLI tool. Run as part of the config generation pipeline in openshift/release (typically via `make jobs` or `auto-config-brancher`). Also used in presubmit checks to verify configs are already in determinized form.
