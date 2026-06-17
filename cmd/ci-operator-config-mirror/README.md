# ci-operator-config-mirror

## What
CLI tool that mirrors ci-operator configuration files from public organizations into a private organization (typically `openshift-priv`). It transforms all image references, namespaces, and promotion targets so that private CI builds use private image streams instead of public ones.

This is a critical part of the private CI pipeline: repos that build official OCP images have their CI configs duplicated into the private org so that embargoed fixes can be tested with private image streams before public disclosure.

## How it works -- full flow

1. Iterate over every ci-operator config file in `--config-dir` using `OperateOnCIOperatorConfigDir()`
2. For each config, apply filtering:
   - Skip configs already belonging to the destination org (`--to-org`)
   - If `--only-org` is set, skip configs from other orgs (unless whitelisted via `--whitelist-file`)
   - Skip repos that don't build any official images (`api.BuildsAnyOfficialImages` with `WithoutOKD`) and aren't whitelisted
3. For each eligible config, apply transformations:
   - Set `canonical_go_repository` to the original public `github.com/org/repo` path (so Go imports resolve correctly in private builds)
   - **Release tag configuration**: if namespace is `ocp`, change to `ocp-private` and append `-priv` to the name (e.g. `4.16` becomes `4.16-priv`)
   - **Integration releases**: same `ocp` to `ocp-private` / `-priv` transformation
   - **Build root**: if the ImageStreamTag is in `ocp`, rewrite to `ocp-private` with `-priv` suffix
   - **Base images / base RPM images**: rewrite any `ocp` namespace references that are valid OCP versions to `ocp-private` with `-priv`
   - **Promotion configuration**: for targets in `ocp` namespace, append `-priv` to name/tag, set namespace to `ocp-private`, disable `tag_by_commit`; for targets in other namespaces, set `Disabled: true` to prevent conflicts with the public counterpart. For whitelisted repos that don't build official images, disable all non-official promotion targets.
   - **Tests**: strip all periodic and postsubmit tests (only presubmits are needed in private repos)
   - Rewrite the org to `--to-org` and rename the repo using `MirroredRepoName()` (flattened orgs keep the original repo name; non-flattened orgs get `{org}-{repo}`)
4. Collect the transformed configs grouped by repo
5. If `--clean` is true (default), delete all existing subdirectories under the destination org's config directory to remove stale configs
6. Write all transformed configs to disk using `DataWithInfo.CommitTo()`

### Repo naming convention
- **Flattened orgs** (default: `openshift`, `openshift-eng`, `operator-framework`, `redhat-cne`, `openshift-assisted`, `ViaQ`): repo name stays the same (e.g. `openshift/installer` -> `openshift-priv/installer`)
- **Non-flattened orgs**: repo name is prefixed with the org (e.g. `stolostron/multicloud-operators-subscription` -> `openshift-priv/stolostron-multicloud-operators-subscription`)
- Additional orgs can be flattened with `--flatten-org`

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--config-dir` | (required) | Directory containing ci-operator config files |
| `--to-org` | (required) | Destination organization name for mirrored configs |
| `--only-org` | `""` | Only mirror configs from this source organization |
| `--flatten-org` | (repeatable) | Additional orgs whose repos should not have org prefix in private |
| `--clean` | `true` | Delete all subdirectories under `--to-org` before generating new configs |
| `--whitelist-file` | `""` | Path to YAML file listing repos to include even if they don't build official images |

## Key files
- `cmd/ci-operator-config-mirror/main.go` -- all logic is in this single file
- `pkg/privateorg/flatten.go` -- `MirroredRepoName()` naming logic and `DefaultFlattenOrgs` list
- `pkg/config/whitelist.go` -- whitelist file loading and matching
- `pkg/api/promotion.go` -- `BuildsAnyOfficialImages()` used for filtering

## Deployment
CLI tool. Not a long-running service. Invoked by `auto-config-brancher` as part of the periodic config generation pipeline in `openshift/release`. Runs in the `ci_auto-config-brancher_latest` container image.
