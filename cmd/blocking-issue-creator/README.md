# blocking-issue-creator

## What
Creates and maintains GitHub issues labeled `tide/merge-blocker` on OCP repositories whose future release branches are frozen for merging. Tide treats these issues as hard merge blockers -- no PRs can merge into the affected branches while the issue exists. This prevents accidental merges into branches that are being fast-forwarded from the development branch.

## How it works -- full flow

1. **Discover repos and branches**: Iterates over all ci-operator config files in the config directory (via `promotion.FutureOptions.OperateOnCIOperatorConfigDir`). For each config that promotes images (excluding OKD), determines which branches correspond to `--future-release` versions using `promotion.DetermineReleaseBranch()`. Skips branches that are the same as the current development branch (those are open for merges).

2. **Search for existing blocker issues**: For each repo, queries GitHub for open issues labeled `tide/merge-blocker` authored by the bot user. The query: `is:issue state:open label:"tide/merge-blocker" repo:{org}/{repo} author:{botLogin}`.

3. **Reconcile issue state**:
   - **No frozen branches**: close all existing blocker issues for the repo.
   - **Frozen branches exist, >1 blocker issue**: close all but the most recently updated one, then update or leave it.
   - **Frozen branches exist, 1 blocker issue**: compare title and body. Update if they changed, otherwise do nothing.
   - **Frozen branches exist, 0 blocker issues**: create a new issue with the `tide/merge-blocker` label.

4. **Rate limiting**: Sleeps 5 seconds between repos to stay within GitHub's 30 requests/minute secondary rate limit.

### Issue format
- **Title**: `Future Release Branches Frozen For Merging | branch:release-4.18 branch:release-4.19`
- **Body**: Lists all frozen branches with a link to the branching documentation.
- **Labels**: `tide/merge-blocker`

### How branch selection works
The tool uses the same branch-naming logic as `repo-brancher`:
- `master`/`main` branches map to `release-{futureVersion}`
- `openshift-{currentVersion}` branches map to `openshift-{futureVersion}`
- Other branch naming patterns are handled by `promotion.DetermineReleaseBranch()`

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--current-release` | (required) | Current OCP development version, e.g. `4.17` |
| `--future-release` | (required, repeatable) | Future release versions to create blockers for |
| `--config-dir` | (required, via `FutureOptions`) | Path to ci-operator configuration directory |
| `--current-promotion-namespace` | (empty) | Promotion namespace filter |
| `--confirm` | false | Actually write changes to disk (dry-run by default) |
| `--dry-run` | true | Use dry-run mode for GitHub API (creates client but does not mutate) |
| `--github-token-path` | (Prow default) | Path to GitHub token |
| `--github-endpoint` | (Prow default) | GitHub API endpoint |

## Key files
- `cmd/blocking-issue-creator/main.go` -- full implementation (single file)
- `pkg/promotion/promotion.go` -- `FutureOptions`, `DetermineReleaseBranch()`
- `pkg/config/options.go` -- `ConfirmableOptions` base

## Deployment
Runs as the periodic Prow job [`periodic-openshift-release-merge-blockers`](https://prow.ci.openshift.org/?job=periodic-openshift-release-merge-blockers). Defined in `ci-operator/jobs/infra-periodics.yaml` in the openshift/release repo.

GitHub API throttle: 300 requests/hour primary rate limit. Additionally sleeps 5 seconds between repos to stay within the 30 requests/minute secondary rate limit.

## Related
- `cmd/repo-brancher` -- the tool that actually fast-forwards the frozen branches
- `cmd/branchingconfigmanagers/fast-forwarding-config-manager` -- updates repo-brancher's job args

---

## Background

### What it does

The `blocking-issue-creator` tool maintains
Tide [blocker issues](https://github.com/kubernetes/test-infra/blob/master/prow/cmd/tide/config.md#merge-blocker-issues)
that prevent code from being merged into OCP repositories' branches that are not open to development.

### Why it exists

During
the [OCP development lifecycle](https://docs.ci.openshift.org/architecture/branching/#normal-development-for-4x-release)
, some branches are blocked for merges, because their content is automatically fast-forwarded to the content of another
branch. To prevent mistakes, we use Tide's merge blocker issue feature to block all merges to these branches, and use
the `blocking-issue-creator` tool to ensure that all affected repositories have a correct merge blocker issue at all
times.

### How it works (detailed)

The tool takes current and future OCP versions as input, and then iterates over ci-operator configuration directory to
find all configurations that promote images to OCP of the given versions to discover repositories and branches that
are not open for merges. For all repositories discovered, it ensures that the corresponding Tide merge blocker issue
exists, by either creating it or updating it if it already exists.

### How is it deployed

The periodic
job [periodic-openshift-release-merge-blockers](https://prow.ci.openshift.org/?job=periodic-openshift-release-merge-blockers) ([definition](https://github.com/openshift/release/blob/6e850667c1c9d933f4071734611ae68608deba8c/ci-operator/jobs/infra-periodics.yaml#L1365-L1402))
uses `blocking-issue-creator` to
create [merge blocking issues](https://github.com/issues?q=is%3Aopen+is%3Aissue+archived%3Afalse+label%3Atide%2Fmerge-blocker+org%3Aopenshift)
.
