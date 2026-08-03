# determinize-prow-config

## What
Normalizes Prow configuration and plugin configuration YAML files to enforce deterministic formatting. Optionally shards monolithic Prow config into per-org and per-org/repo files for scalability, extracting org- and repo-specific settings from the main config into a directory tree.

This tool handles two separate config files: the Prow config (`_config.yaml`) and the plugin config (`_plugins.yaml`).

## How it works -- full flow

### Prow config normalization
1. Load `{prow-config-dir}/_config.yaml` in **strict mode** via Prow's `LoadStrict()`. If `--sharded-prow-config-base-dir` is set, also load supplemental `_prowconfig.yaml` files from that directory tree.
2. If `--sharded-prow-config-base-dir` is set, shard the Prow config:
   - **Branch protection**: Extract per-org and per-org/repo branch protection rules into separate files. Org-level policies are written to `{org}/_prowconfig.yaml`, repo-level to `{org}/{repo}/_prowconfig.yaml`. The rules are removed from the main config.
   - **Tide merge types**: Extract per-org and per-org/repo merge method configurations into the shard files.
   - **Tide queries**: Split queries by org and repo scope. Each org-scoped query goes to `{org}/_prowconfig.yaml`, each repo-scoped query to `{org}/{repo}/_prowconfig.yaml`. Query copies are deep-copied to avoid mutation.
   - **Slack reporter configs**: Extract per-org/repo Slack reporter configurations (the global `*` config stays in main).
3. Marshal the (now stripped) main config to YAML and write it back to `{prow-config-dir}/_config.yaml`.

### Plugin config normalization
1. Load `{prow-config-dir}/_plugins.yaml` using the Prow plugin agent, with supplemental `_pluginconfig.yaml` files from the config directory.
2. If `--sharded-plugin-config-base-dir` is set, shard the plugin config:
   - **Plugins**: Per-org/repo plugin enablement goes to `{org/repo}/_pluginconfig.yaml`
   - **Bugzilla**: Org-level defaults go to `{org}/_pluginconfig.yaml`, repo-level to `{org}/{repo}/_pluginconfig.yaml`
   - **Approve**: Per-repo approve configs are extracted
   - **LGTM**: Per-repo LGTM configs are extracted
   - **Triggers**: Per-repo trigger configs are extracted
   - **Welcome**: Per-repo welcome message configs are extracted
   - **External plugins**: Per-org/repo external plugin registrations are extracted
   - **Restricted labels**: Per-org/repo restricted label configs are extracted (global `*` stays in main)
3. Marshal the (now stripped) main plugin config to YAML and write it back.

### What "determinize" means
Without sharding flags, this is a read-parse-serialize roundtrip that standardizes YAML formatting (field order, indentation, quoting). With sharding flags, it additionally redistributes config into a normalized directory structure.

### Sharding file structure
```text
{sharded-prow-config-base-dir}/
  {org}/
    _prowconfig.yaml          # org-level branch protection, tide queries, merge methods
    {repo}/
      _prowconfig.yaml        # repo-level branch protection, tide queries, slack reporter, merge methods

{sharded-plugin-config-base-dir}/
  {org}/
    _pluginconfig.yaml        # org-level bugzilla defaults, plugins
    {repo}/
      _pluginconfig.yaml      # repo-level plugins, approve, lgtm, triggers, welcome, external plugins, bugzilla, restricted labels
```

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--prow-config-dir` | (required) | Path to the Prow configuration directory containing `_config.yaml` and `_plugins.yaml` |
| `--sharded-prow-config-base-dir` | `""` | Base directory for sharded Prow config output; if set, org/repo-specific config is extracted from main config and written here |
| `--sharded-plugin-config-base-dir` | `""` | Base directory for sharded plugin config output; if set, org/repo-specific plugin config is extracted and written here |

## Key files
- `cmd/determinize-prow-config/main.go` -- entry point, orchestrates config and plugin config normalization
- `pkg/api/shardprowconfig/shardprowconfig.go` -- `ShardProwConfig()` implements Prow config sharding (branch protection, tide, slack reporter, merge methods)
- `pkg/prowconfigsharding/prowconfigsharding.go` -- `WriteShardedPluginConfig()` implements plugin config sharding (plugins, bugzilla, approve, lgtm, triggers, welcome, external plugins, restricted labels)
- `pkg/config/release.go` -- constants: `ProwConfigFile` (`_config.yaml`), `PluginConfigFile` (`_plugins.yaml`), `SupplementalProwConfigFileName` (`_prowconfig.yaml`), `SupplementalPluginConfigFileName` (`_pluginconfig.yaml`)

## Deployment
CLI tool. Run as part of the config generation pipeline in openshift/release (via `make jobs` or `auto-config-brancher`). Also used to verify Prow config is determinized in presubmit checks.
