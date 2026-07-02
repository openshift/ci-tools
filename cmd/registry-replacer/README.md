# registry-replacer

## What
Automated tool that ensures all `FROM` directives in Dockerfiles used by ci-operator image builds reference images through the CI build cluster's internal registry rather than external registries (like `registry.ci.openshift.org`). It scans ci-operator configs, fetches the corresponding Dockerfiles from GitHub, extracts registry references, and adds `inputs[].as` replacement directives to the ci-operator config so that images are pulled from the build cluster during CI.

It can also prune unused replacements, prune unused base images, ensure Dockerfiles match the ocp-build-data repo, and optionally create a PR with the changes.

## How it works -- full flow

### Per-config processing
For each ci-operator config in `--config-dir`, the tool:

1. **Skip check**: Skip if the repo is in `--ignore-repos` or the org is in `--ignore-orgs`
2. **Dockerfile alignment** (if `--ensure-correct-promotion-dockerfile`): Update `contextDir` and `dockerfilePath` in image build steps to match what's defined in the [ocp-build-data](https://github.com/openshift/ocp-build-data) repo
3. **Fetch Dockerfiles**: For each image build step, fetch the Dockerfile from GitHub (using HTTP, not git clone, for performance)
4. **Apply existing replacements**: Simulate what the build tools would do -- apply `from` replacements and `inputs[].as` replacements to the Dockerfile
5. **Extract registry references**: Parse the Dockerfile to find all `FROM` directives referencing external registries (e.g., `registry.ci.openshift.org/ocp/builder:...`)
6. **Ensure replacements**: For each external registry reference found, add an `inputs` entry mapping the image to a `base_images` entry so the build cluster pulls it locally
7. **Prune unused replacements** (if `--prune-unused-replacements`): Remove `inputs[].as` entries that don't match any `FROM` directive in the Dockerfile
8. **Prune ocp/builder replacements** (if `--prune-ocp-builder-replacements`): Remove replacements targeting `ocp/builder` for configs that promote to `ocp`
9. **Prune unused base images** (if `--prune-unused-base-images`): Resolve the full config (including step registry) and remove `base_images` entries not referenced by any image build, test step, or operator substitution
10. **Write changes**: If the config changed, write it back to disk

### PR creation (if `--create-pr`)
After processing all configs:
1. Check if any files changed
2. Commit and push to a `registry-replacer` branch on the user's fork
3. Create or update a PR against `openshift/release` with a description of what was changed

### Concurrency
Processing is parallelized with `--concurrency` (default 500) goroutines via a semaphore, since each config independently fetches its Dockerfile over HTTP.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--config-dir` | (required) | Path to ci-operator config directory |
| `--create-pr` | `false` | Automatically create/update a PR with changes |
| `--github-user-name` | `openshift-bot` | GitHub username for PR creation |
| `--self-approve` | `false` | Add `approved` and `lgtm` labels to the PR |
| `--ensure-correct-promotion-dockerfile` | `false` | Align Dockerfiles with ocp-build-data |
| `--ensure-correct-promotion-dockerfile-ignored-repos` | (none) | Repos to skip for Dockerfile alignment (can repeat) |
| `--ignore-repos` | (none) | Repos to skip entirely (can repeat) |
| `--ignore-orgs` | (none) | Orgs to skip entirely (can repeat) |
| `--concurrency` | `500` | Max concurrent goroutines |
| `--ocp-build-data-repo-dir` | `../ocp-build-data` | Path to ocp-build-data repo |
| `--current-release-major` | `4` | Current release major version |
| `--current-release-minor` | `6` | Current release minor version |
| `--prune-unused-replacements` | `false` | Remove replacements that don't match any FROM directive |
| `--prune-ocp-builder-replacements` | `false` | Remove all ocp/builder-targeting replacements |
| `--prune-unused-base-images` | `false` | Remove base_images not referenced anywhere |
| `--apply-replacements` | `true` | Whether to apply Dockerfile replacements (false also disables pruning) |
| `--registry` | (empty) | Path to step registry (needed for `--prune-unused-base-images`) |

## Key files
- `cmd/registry-replacer/main.go` -- entry point, the `replacer()` function, Dockerfile parsing, replacement logic, PR creation
- `pkg/dockerfile/` -- Dockerfile parsing utilities, registry reference extraction
- `pkg/api/ocpbuilddata/` -- ocp-build-data config loading

## Deployment
Runs as a periodic CronJob. Fetches Dockerfiles via GitHub HTTP API (not git), so it needs a GitHub token. When creating PRs, it pushes to a fork and uses the GitHub API to create/update the PR.

**Core flow** (rewrites ci-operator configs, not Dockerfiles):
* Finds all ci-operator configs with at least one `images` directive
* Downloads the corresponding Dockerfile and scans for external registry references
* Adds `inputs[].as` replacement directives to the ci-operator config so images are pulled from the build cluster
* Prunes replacements that no longer match any `FROM` directive in the Dockerfile
* Removes all replacements targeting `ocp/builder` images

**Optional** (`--ensure-correct-promotion-dockerfile`):
* Aligns `contextDir` and `dockerfilePath` in the ci-operator config's image build steps to match what is defined in the ocp-build-data repository
