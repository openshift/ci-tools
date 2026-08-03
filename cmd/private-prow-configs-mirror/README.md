# private-prow-configs-mirror

## What
CLI tool that mirrors Prow configuration (branch protection, Tide settings, plugin configs, etc.) from public repositories to their `openshift-priv` equivalents. This ensures that private forks have the same Prow policies as their public counterparts without manual duplication.

It reads the full Prow config and plugin config from the release repo, determines which repos have private mirrors (by scanning ci-operator configs for repos that build official images + the whitelist), and injects matching entries for `openshift-priv`.

## How it works -- full flow

1. Load all Prow config and plugin config from `--release-repo-path`
2. Strip all job definitions (presubmits, postsubmits, periodics) from the loaded Prow config -- this tool only handles non-job config
3. Load the plugins config from `core-services/prow/02_config/_plugins.yaml`
4. Query the GitHub API via `ghClient.GetRepos("openshift-priv", false)` to get the list of repos that actually exist in the private org
5. Scan ci-operator configs to find all repos building official images + add whitelisted repos, then cross-reference against repos that actually exist in `openshift-priv` to build the `orgReposWithOfficialImages` mapping
6. Clean all existing `openshift-priv` entries from plugin configs to avoid retaining stale names
7. For each Prow config section, inject private org equivalents:

### Config sections mirrored
| Section | Behavior |
|---|---|
| **Branch protection** | Delete existing `openshift-priv` org entry, then copy per-repo rules from source orgs into `openshift-priv` repos |
| **Tide context policy** | Copy per-repo context policies to `openshift-priv` repos |
| **Tide merge type** | Mirror org/repo and org/repo@branch merge method settings; drop stale `openshift-priv` repo-level entries |
| **Tide queries** | Add `openshift-priv/{repo}` to every Tide query that includes the public `{org}/{repo}`, remove stale priv entries |
| **PR status base URLs** | Copy per-repo PR status URLs to `openshift-priv` |
| **Plank decoration configs** | Copy per-repo decoration configs to `openshift-priv` |
| **Job URL prefix config** | Copy per-repo job URL prefixes to `openshift-priv` |
| **Approve plugin** | Add `openshift-priv/{repo}` to approve plugin repo lists |
| **LGTM plugin** | Add `openshift-priv/{repo}` to LGTM plugin repo lists |
| **Bugzilla plugin** | Copy per-repo Bugzilla options to `openshift-priv` |
| **Plugins** | Compute the union of org-level and repo-level plugins for each mirrored repo, extract common plugins to `openshift-priv` org level, keep repo-specific differences at repo level |

8. Write updated Prow config to `core-services/prow/02_config/_config.yaml`
9. Write updated plugin config (sharded) via `WriteShardedPluginConfig`

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--release-repo-path` | (required) | Path to openshift/release repository directory |
| `--config-dir` | (derived from release-repo-path) | ci-operator config directory |
| `--whitelist-file` | `""` | Path to YAML file listing repos to include |
| `--flatten-org` | (repeatable) | Additional orgs whose repos should not have org prefix |
| `--dry-run` | `true` | Use API tokens but do not mutate |
| GitHub flags | | Standard Prow GitHub options (`--github-token-path`, etc.) |

## Key files
- `cmd/private-prow-configs-mirror/main.go` -- all logic in this single file
- `pkg/privateorg/flatten.go` -- `MirroredRepoName()` and `DefaultFlattenOrgs`
- `pkg/config/whitelist.go` -- whitelist configuration
- `pkg/prowconfigsharding/` -- plugin config sharding for output
- `pkg/prowconfigutils/` -- `ExtractOrgRepoBranch()` for parsing merge type keys

## Deployment
CLI tool. Not a long-running service. Invoked by `auto-config-brancher` as part of the periodic config generation pipeline. Runs in the `ci_auto-config-brancher_latest` container image.
It walks through all the ci-operator configuration files to get the information of which repositories are promoting
official images.
When a configuration is detected, the tool generates the configuration for the corresponding repository of the `openshift-priv` organization instead.

The following components of the configs will be affected:

### Prow Config
* branch-protection
* context_options
* tide.merge_method
* tide.queries
* tide.pr_status_base_urls
* plank.default_decoration_configs
* plank.job_url_prefix_config

### Prow Plugins
* approve
* lgtm
* plugins
* bugzilla
