# ci-operator-yaml-creator

## What
Creates or updates `.ci-operator.yaml` files in ART-built repositories to declare their `build_root_image`. This enables reading the build root configuration directly from the component repository rather than from the central ci-operator config in openshift/release.

When the in-repo `.ci-operator.yaml` already matches the central config, the tool also updates the central config to set `build_root.from_repository: true`, completing the migration.

## How it works -- full flow

1. **Build ART repo filter**: Load all image configs from the ocp-build-data directory (all versions) and build a set of `org/repo` strings that are ART-built. Only repositories in this set are processed.

2. **Iterate ci-operator configs**: Walk the `--ci-operator-config-dir` directory using `config.OperateOnCIOperatorConfigDir()`. For each config file:
   - Skip if the repo is not ART-built (not in the filter set)
   - Skip if `build_root_image` is nil, already `from_repository`, has a variant, or is not on the `master`/`main` branch
   - Skip if `build_root_image.image_stream_tag` is nil (other build root types are not handled)

3. **Check in-repo config**: Fetch the existing `.ci-operator.yaml` from the component repo (on its default branch) via `github.FileGetterFactory`.

4. **Compare**: Build the expected `CIOperatorInrepoConfig` from the central config's `ImageStreamTagReference` and diff against the in-repo version.

5. **If they match**: The in-repo file is already correct. Update the central ci-operator config file in place:
   - Clear `image_stream_tag` from `build_root_image`
   - Set `from_repository: true`
   - Write the modified config back to the ci-operator config directory

6. **If they differ**: The in-repo file needs updating.
   - Serialize the expected config to YAML
   - Clone the repo (up to `--push-ceiling` repos)
   - Checkout the default branch
   - Write the new `.ci-operator.yaml` to the repo checkout
   - Call the PR creation function to push and open a PR with a descriptive body explaining the change, linking to documentation, and noting this is mandatory for OCP components with ART build configs

### PR body content
The PR explains:
- `.ci-operator.yaml` references the `build_root_image` from openshift/release
- This enables updating the build root in lockstep with code changes
- It is mandatory for all OCP components with an ART build config
- Links to the docs at `docs.ci.openshift.org/architecture/ci-operator/#build-root-image`
- A second auto-generated PR to openshift/release will follow once this one merges

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--ci-operator-config-dir` | (required) | Base path to ci-operator config directory (e.g. `ci-operator/config` in openshift/release) |
| `--ocp-build-data-dir` | `../ocp-build-data` | Path to ocp-build-data repo checkout |
| `--push-ceiling` | `1` | Max number of repos to push updated `.ci-operator.yaml` to; 0 = unlimited |
| `--create-prs` | `false` | Whether to create GitHub PRs after pushing |
| `--max-concurrency` | `4` | Legacy flag, does nothing (tool cannot run concurrently) |
| PR creation flags | -- | `--self-approve`, `--github-token-path`, `--pr-source-mode`, etc. via `PRCreationOptions` |

## Key files

- `cmd/ci-operator-yaml-creator/main.go` -- all logic: ART filter construction, config iteration, in-repo file comparison, PR creation
- `pkg/api/ocpbuilddata/` -- `LoadImageConfigs` for ART repo discovery
- `pkg/api/types.go` -- `CIOperatorInrepoConfig`, `CIOperatorInrepoConfigFileName` (`.ci-operator.yaml`)
- `pkg/config/` -- `OperateOnCIOperatorConfigDir`, `Info` metadata
- `pkg/github/prcreation/prcreation.go` -- `PRCreationOptions.UpsertPR()`

## Deployment
Runs as a periodic Prow job. Requires GitHub token for fetching in-repo files and creating PRs, plus read access to ocp-build-data and the ci-operator config directory.
If the `.ci-operator.yaml` is already up-to-date, it will set `build_root.from_repository: true`
