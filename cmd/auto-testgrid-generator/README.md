# auto-testgrid-generator

## What
Orchestrator that runs `testgrid-config-generator` to produce updated TestGrid dashboard configurations, then creates or updates a pull request against `kubernetes/test-infra` with the changes. This is the automation that keeps TestGrid dashboards in sync with the current set of OpenShift CI periodic jobs.

## How it works -- full flow

1. **Parse flags**: collects paths to the testgrid config directory, release controller config, Prow jobs directory, allow-list file, and git/PR creation options.

2. **Run testgrid-config-generator**: executes `/usr/bin/testgrid-config-generator` as a subprocess with the following arguments:
   - `-testgrid-config {dir}` -- where to write TestGrid YAML
   - `-release-config {dir}` -- release controller configuration
   - `-prow-jobs-dir {dir}` -- Prow periodic job definitions
   - `-allow-list {file}` -- job classification overrides

3. **Create or update PR**: uses `prcreation.PRCreationOptions.UpsertPR()` to:
   - Check the git working directory for changes
   - Create a branch and commit if changes exist
   - Push to the fork and create a PR against `kubernetes/test-infra` (configurable with `--github-org`)
   - If a PR with matching title already exists, force-push to update it
   - PR title format: `Update OpenShift testgrid definitions by auto-testgrid-generator job at {timestamp}`
   - Assigns the PR to `--assign` (default: `openshift/test-platform`)

### PR matching
The tool uses title matching (`matchTitle`) to find existing PRs. If a PR with the prefix "Update OpenShift testgrid definitions by auto-testgrid-generator job" already exists, it updates that PR rather than creating a new one.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--testgrid-config` | (none) | Directory where TestGrid output YAML is stored |
| `--release-config` | (none) | Directory of release controller config files |
| `--prow-jobs-dir` | (none) | Directory of Prow job config files |
| `--allow-list` | (none) | File with release-type overrides |
| `--working-dir` | `.` | Git working directory |
| `--github-login` | `openshift-bot` | GitHub username for PR creation |
| `--github-org` | `kubernetes` | GitHub org (override for testing) |
| `--upstream-branch` | `master` | Target branch for the PR |
| `--assign` | `openshift/test-platform` | GitHub user or team to assign the PR to |
| `--git-name` | (from GitAuthorOptions) | Git author name for commits |
| `--git-email` | (from GitAuthorOptions) | Git author email for commits |
| (PRCreationOptions flags) | | GitHub token path, etc. |

## Key files
- `cmd/auto-testgrid-generator/main.go` -- entry point, subprocess execution of testgrid-config-generator, PR creation
- `cmd/testgrid-config-generator/main.go` -- the actual TestGrid config generation logic (invoked as a binary)
- `pkg/github/prcreation/` -- PR creation/update library

## Deployment
Periodic Prow job. The container image includes both the `auto-testgrid-generator` and `testgrid-config-generator` binaries (the latter at `/usr/bin/testgrid-config-generator`). The job runs with a GitHub token for PR creation against `kubernetes/test-infra`.

## Related
- `cmd/testgrid-config-generator` -- the actual generation logic, invoked as a subprocess
- Target repo: `kubernetes/test-infra` (TestGrid configuration lives there)
- TestGrid dashboards: `https://testgrid.k8s.io/redhat-openshift-*`
