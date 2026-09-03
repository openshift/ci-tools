# prcreator

## What
Generic CLI utility that creates or updates a GitHub pull request from uncommitted changes in the current working directory. It is the final step used by many CI automation tools that generate changes locally and need to open a PR.

The tool handles the full lifecycle: checking for changes, committing, pushing (to a fork or directly to the upstream repo), and creating or updating the PR via the GitHub API. If no changes are detected, it exits cleanly without creating a PR.

## How it works -- full flow

1. **Gather options**: Parse required flags (`--pr-title`, `--organization`, `--repo`, `--branch`) and PR creation options (GitHub auth, self-approve, source mode).

2. **Delegate to `PRCreationOptions.UpsertPR()`**: Pass the current directory (`.`), org, repo, branch, and PR title along with optional body, assignee, and git commit message.

3. **Inside UpsertPR** (from `pkg/github/prcreation`):
   - Check for uncommitted changes via `bumper.HasChanges()`. If none, log and return.
   - Determine the bot username from the GitHub token.
   - Configure local git user/email.
   - Derive a branch name from the PR match title (lowercased, spaces/colons replaced with hyphens).

4. **Push strategy** (controlled by `--pr-source-mode`):
   - **`fork`** (default, requires `--github-token-path`):
     - Check if a fork exists for the bot user; create one if not (waits up to 6 minutes for GitHub to provision it)
     - Commit and push to the fork via `bumper.GitCommitAndPush()` using the PAT
     - Create a cross-repo PR (head: `<bot-user>:<branch>`)
   - **`branch`** (requires `--github-app-id` and `--github-app-private-key-path`):
     - Create a local branch, stage all changes, commit
     - Push directly to the upstream repo using Prow's `GitClientFactory` with App auth
     - Create a same-repo PR (head: `<branch>`)

5. **PR creation/update** (`ensurePR`):
   - Use `bumper.UpdatePullRequestWithLabels()` to create a new PR or update an existing one matching the title
   - If `--self-approve`, add `lgtm` and `approved` labels
   - If `--pr-assignee` is set, append `/cc @assignee` to the PR body

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--pr-title` | (required) | Title of the PR to create |
| `--pr-message` | `""` | Body/description of the PR |
| `--git-message` | `""` | Git commit message (if different from PR body) |
| `--pr-assignee` | `""` | Comma-separated list of GitHub usernames to assign/cc on the PR |
| `--organization` | `openshift` | GitHub organization for the PR |
| `--repo` | `release` | GitHub repository for the PR |
| `--branch` | `main` | Target branch for the PR |
| `--self-approve` | `false` | Add `lgtm` and `approved` labels to the PR |
| `--pr-source-mode` | `fork` | How to push: `fork` (cross-repo via PAT) or `branch` (same-repo via App auth) |
| `--github-token-path` | `""` | Path to GitHub PAT (required for fork mode) |
| `--github-app-id` | `""` | GitHub App ID (required for branch mode) |
| `--github-app-private-key-path` | `""` | Path to GitHub App private key (required for branch mode) |
| `--github-endpoint` | `https://api.github.com` | GitHub API endpoint |

## Key files

- `cmd/prcreator/main.go` -- entry point: option parsing, delegates to `UpsertPR()`
- `pkg/github/prcreation/prcreation.go` -- `PRCreationOptions`, `UpsertPR()`, fork/branch push strategies, PR creation/update logic

## Deployment
CLI tool. Not deployed as a service. Invoked by other automation jobs (e.g. `auto-config-brancher`, `private-org-sync`, `ci-operator-yaml-creator`) as the final step to turn local changes into a GitHub PR.
