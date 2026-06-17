# config-change-trigger

## What
Prow job that detects ci-operator configuration changes in an openshift/release pull request and triggers the affected image-building postsubmit jobs. When a PR modifies ci-operator configs that affect image builds, this tool ensures the corresponding postsubmit jobs run immediately so that updated images are available without waiting for the next natural trigger (e.g., a merge to the affected repo).

This is the postsubmit counterpart to `pj-rehearse` (which handles presubmit rehearsals). While pj-rehearse validates that config changes do not break jobs, config-change-trigger ensures the side effects of those changes (new images) are realized promptly.

## How it works -- full flow

1. **Read job context**: Resolve the Prow `JOB_SPEC` environment variable to determine the PR under test (org, repo, base SHA, PR refs).

2. **Load configurations**: Load two complete snapshots of all CI configuration from the openshift/release checkout:
   - **PR version** (`prConfig`): the current working copy at `--candidate-path` (includes the PR's changes)
   - **Base version** (`masterConfig`): the configuration as it existed at the parent commit of the base SHA (`baseSHA^1`)

3. **Diff ci-operator configs**: Use `diffs.GetChangedCiopConfigs()` to compare the base and PR versions of all ci-operator configuration files. This returns a set of changed configs keyed by org/repo/branch metadata.

4. **Find affected postsubmits**: Call `diffs.GetImagesPostsubmitsForCiopConfigs()` to find all postsubmit jobs in the PR's Prow config that build images and are associated with the changed ci-operator configs. These are the jobs whose output images would be affected by the config changes.

5. **Resolve current SHAs**: For each affected postsubmit, call the GitHub API (`GetRef`) to get the current HEAD SHA of the target branch (e.g., `heads/main` for openshift/some-repo). This ensures the triggered postsubmit runs against the latest code.

6. **Create ProwJobs**: For each affected postsubmit (up to `--limit`):
   - Build a `Refs` struct with the org, repo, branch, and current HEAD SHA
   - Create a ProwJob using `pjutil.NewProwJob(pjutil.PostsubmitSpec(...))` with the postsubmit's labels and annotations
   - Set the namespace to the Prow job namespace from the PR's config
   - Submit the ProwJob to the Kubernetes API

7. **Truncate if needed**: If more postsubmits are affected than the limit, only trigger the first N and log that truncation occurred.

### Error handling
- If the PR config cannot be loaded, it logs a warning but continues (the tool still needs the base config)
- If the base config cannot be loaded, it fatals (both versions are required for diffing)
- Individual job creation failures are collected and reported as a fatal aggregate error at the end

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--dry-run` | `true` | When true, use a fake Kubernetes client (prints but does not create ProwJobs) |
| `--limit` | `30` | Maximum number of postsubmit jobs to trigger |
| `--candidate-path` | (required) | Path to the openshift/release working copy with the PR's changes |

Standard Prow GitHub flags (`--github-token-path`, etc.) are also supported. Anonymous GitHub access is allowed.

## Key files
- `cmd/config-change-trigger/main.go` -- entire implementation: JOB_SPEC parsing, config loading, diffing, SHA resolution, ProwJob creation

## Deployment
Runs as a Prow job (not a long-lived service). Reads the `JOB_SPEC` environment variable set by Prow. Typically configured as a postsubmit on openshift/release that runs after config PRs merge. Requires a checkout of openshift/release at `--candidate-path`.

When `--dry-run=false`, requires in-cluster kubeconfig for ProwJob creation.

## Related
- `cmd/pj-rehearse` -- similar concept but for presubmit rehearsals of config changes
- `pkg/diffs/diffs.go` -- shared diff detection logic (`GetChangedCiopConfigs`, `GetImagesPostsubmitsForCiopConfigs`)
- `pkg/config/load.go` -- config loading (`GetAllConfigs`, `GetAllConfigsFromSHA`)
