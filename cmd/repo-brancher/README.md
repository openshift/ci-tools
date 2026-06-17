# repo-brancher

## What
Fast-forwards future OCP release branches to match the current development branch HEAD. During the OCP lifecycle, future release branches exist as placeholders that are kept in sync with the development branch until code freeze, when they diverge. This tool performs that continuous synchronization via `git push` of the dev branch content to the future branch.

## How it works -- full flow

1. **Discover repos**: Iterates over all ci-operator config files in the config directory (via `promotion.FutureOptions.OperateOnCIOperatorConfigDir`, excluding OKD). Each config that promotes images to the current release identifies a repo and its development branch.

2. **Skip ignored repos**: Repos or orgs listed in `--ignore` are skipped entirely.

3. **Determine target branches**: For each repo, calls `promotion.DetermineReleaseBranch()` to map development branches to future release branches:
   - `master`/`main` maps to `release-{futureVersion}`
   - `openshift-{currentVersion}` maps to `openshift-{futureVersion}`
   - If the future branch would be the same as the current branch, it is skipped.

4. **Clone and push** (per repo):
   - Creates a directory under `--git-dir` (or a temp dir).
   - Runs `git init` and `git fetch --depth 1` to shallow-clone the development branch.
   - For each future branch, runs `git ls-remote` to check if the branch exists.
   - If `--confirm` is set, pushes `FETCH_HEAD` to `refs/heads/{futureBranch}`.

5. **Progressive deepening on push failure**: If a push fails because the remote has diverged (non-fast-forward), the tool progressively deepens the clone:
   - Depths increase exponentially: 1, 2, 4, 8, 16, 32, 64, 128 additional commits (via `git fetch --deepen`).
   - After 8 attempts, falls back to `git fetch --unshallow` (full history).
   - Maximum 9 retry iterations. If all fail, the config is recorded as failed.

6. **Error handling**: Failed configs are tracked in a set. If any config fails, the tool exits with code 1 after processing all repos.

7. **Retry logic**: Every `git` command is retried up to 3 times with exponential backoff (1s, 2s, 4s) to handle transient network errors.

8. **Token censoring**: When `--confirm` is set and a token is loaded, log output is censored to prevent token leakage.

### Authentication
When `--confirm` is set, the tool constructs HTTPS remote URLs with embedded credentials: `https://{username}:{token}@github.com/{org}/{repo}`. The token is read from `--token-path`.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--current-release` | (required) | Current OCP development version |
| `--future-release` | (required, repeatable) | Future release versions whose branches should be fast-forwarded |
| `--config-dir` | (required, via `FutureOptions`) | Path to ci-operator config directory |
| `--confirm` | false | Actually push branches (dry-run by default, logs "Would create new branch") |
| `--username` | (required with `--confirm`) | GitHub username for push authentication |
| `--token-path` | (required with `--confirm`) | Path to file containing GitHub token |
| `--git-dir` | (temp dir) | Directory for git operations. If unset, creates and cleans up a temp dir. |
| `--ignore` | (empty, repeatable) | Org or org/repo to skip. Can be passed multiple times. |
| `--current-promotion-namespace` | (empty) | Promotion namespace filter |

## Key files
- `cmd/repo-brancher/main.go` -- full implementation (single file)
- `pkg/promotion/promotion.go` -- `FutureOptions`, `DetermineReleaseBranch()`

## Deployment
Runs as the periodic Prow job [`periodic-openshift-release-fast-forward`](https://prow.ci.openshift.org/?job=periodic-openshift-release-fast-forward). Defined in `ci-operator/jobs/infra-periodics.yaml` in the openshift/release repo.

The `fast-forwarding-config-manager` (under `branchingconfigmanagers`) automatically updates this job's `--current-release` and `--future-release` arguments as the OCP lifecycle progresses.

## Related
- `cmd/blocking-issue-creator` -- creates merge-blocker issues on the branches this tool fast-forwards
- `cmd/branchingconfigmanagers/fast-forwarding-config-manager` -- manages this job's version arguments
## What it does

The `repo-brancher` automatically fast forwards the git content of future release branches (which
are [closed for merges](https://docs.ci.openshift.org/architecture/branching/), see
also [`blocker-issue-creator`](../blocking-issue-creator)) to the content of the main development branch.

## Why it exists

The future release branches are created in advance so that events like branch cuts on code freeze are
easier: when the branches are eventually needed, everything is already set up in place. Keeping the system
close to the desired state reduces an opportunity for drift and mistakes, so the future branches should
be continuously updated with code merged to the development branch.

For more information about OCP branching scheme, see
the [Centralized Branch Management](https://docs.ci.openshift.org/architecture/branching/) document.

## How it works

The tool iterates over all ci-operator config files in openshift/release, and it selects a set of repositories to
operate on by looking at which are actively promoting images into a specific OpenShift release, provided by
`--current-release`. Branches of repos that actively promote to this release are considered to be the dev branches.

After the development branch is detected, its git content is fast-forwarded to all branches for the provided
`--future-release` values. For efficiency, this is done via shallow fetches and pushes with increasing depth.

## How is it deployed

The tool is executed regularly in
the [`periodic-openshift-release-fast-forward`](https://prow.ci.openshift.org/?job=periodic-openshift-release-fast-forward)
job ([definition](https://github.com/openshift/release/blob/43e46bb9555c870bd4d48d18efbddef2b2085019/ci-operator/jobs/infra-periodics.yaml#L1288-L1324))
.
