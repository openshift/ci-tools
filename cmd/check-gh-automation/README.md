# check-gh-automation

## What
Validates that GitHub automation (bots, apps, branch protection) is correctly configured for repositories that use OpenShift CI. Checks bot collaborator access, CI app installation, branch protection admin requirements, cherrypick robot permissions, and automated branching prerequisites. Can run against all Prow-configured repos, a specific list, or only repos modified in a PR.

## How it works -- full flow

### Repository selection
The tool determines which repos to check using three strategies (in priority order):
1. **Explicit list** (`--repo org/repo`): check only the specified repos
2. **PR-scoped** (`--candidate-path`): resolve the PR's `JobSpec` from environment, diff against base SHA to find added/copied/renamed ci-operator configs and prow configs, extract unique `org/repo` pairs. If more than 10 repos are found, skip all checks (likely a bulk config update, not a new repo).
3. **All Prow repos** (default): check every repo referenced in the Prow config's `AllRepos` set

### Checks performed for each repo

#### Bot access (`--bot`, repeatable)
For each specified bot username:
- Check if the bot is an org member (`IsMember`)
- If not an org member, check if it is a repository collaborator (`IsCollaborator`)
- Fail the repo if any bot has neither org membership nor collaborator access

#### App installation (`--app`, default `openshift-ci`)
Two modes controlled by `--app-check-mode`:
- **`standard`** (default): always check if the app is installed on the repo
- **`tide`**: only check if at least one Tide query exists for the repo; skip otherwise

Calls `IsAppInstalled()` on the GitHub client.

#### Branch protection (`--check-branch-protection`, default true)
If branch protection is configured for the repo (not explicitly set to `unmanaged`):
- Verify the repo is public (or the org has a paid GitHub plan; free plan orgs cannot use branch protection on private repos)
- Verify `openshift-merge-robot` has `admin` permission on the repo (required to manage branch protection rules)

#### Cherrypick robot (when plugin config is loaded)
If the `cherrypick` external plugin is configured for the repo (at org or repo level):
- Check that `openshift-cherrypick-robot` is either an org member or has read/write/admin access on the repo

#### Automated branching (when `--candidate-path` is set)
If a ci-operator config is found for the repo and it promotes to the `ocp` namespace:
- Verify that GitHub Issues are enabled on the repository (required for automated branching notifications)

### Output
Collects all failing repos into a set. If any repos fail, exits with a fatal log listing them all.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--bot` | (none, repeatable) | Bot username to check for collaborator/member access |
| `--app` | `openshift-ci` | GitHub App name to check for installation |
| `--app-check-mode` | `standard` | `standard`: always check app; `tide`: only check if Tide queries exist for the repo |
| `--check-branch-protection` | `true` | Verify `openshift-merge-robot` has admin access where branch protection is enabled |
| `--ignore` | (none, repeatable) | Org or org/repo to skip. Formatted as `org` or `org/repo` |
| `--repo` | (none, repeatable) | Specific org/repo to check (overrides auto-detection) |
| `--candidate-path` | `""` | Path to openshift/release working copy; enables PR-scoped repo detection |
| Prow config flags | -- | `--config-path`, `--job-config-path`, `--supplemental-prow-config-dir` via `ConfigOptions` |
| Plugin config flags | -- | `--plugin-config` via `PluginOptions`; if not set, cherrypick checks are skipped |
| GitHub flags | -- | `--github-token-path`, `--github-endpoint`, etc. via `GitHubOptions` |

## Key files

- `cmd/check-gh-automation/main.go` -- all logic: repo determination (`determineRepos`, `gatherModifiedRepos`), checks (`checkRepos`), GitHub API interactions

## Deployment
CLI tool. Typically run as a presubmit job on openshift/release PRs (with `--candidate-path` pointing to the PR checkout) and as a periodic job (checking all Prow-configured repos). Requires a GitHub token with read access to check org membership, collaborator status, app installation, and permissions.
This can be run in multiple modes:

## Pass Prow Config Options
The standard Prow config options can be supplied, and the tool will check _every_ repo with configurations:
```bash
check-gh-automation \
--bot=openshift-merge-robot \
--bot=openshift-ci-robot \
--config-path=/release/core-services/prow/02_config/_config.yaml \
--supplemental-prow-config-dir=/release/core-services/prow/02_config \
--job-config-path=/release/ci-operator/jobs/ \
--plugin-config=/release/core-services/prow/02_config/ViaQ/_pluginconfig.yaml \
--github-app-id={APP_ID} \
--github-app-private-key-path={CERT_PATH}
```

## Determine Modified Repos from Candidate and JobSpec
If a `candidate-path` to the modified `openshift/release` repo is provided, then the tool will determine which repos have modified/added configurations and _only_ check those.
It is able to determine this by utilizing the `$JOB_SPEC` environment variable that is available in the test pods.
```bash
check-gh-automation \
--bot=openshift-merge-robot \
--bot=openshift-ci-robot \
--candidate-path=/release \
--plugin-config=/path/to/plugin/config.yaml \
--github-app-id={APP_ID} \
--github-app-private-key-path={CERT_PATH}
```

## Pass specific Repo(s) to check
Use the `--repo` parameter for specific repos. Do not supply prow config options or `candidate-path` when using this mode.

## Local Development
Test out the tool locally using the provided script:
```bash
hack/local-check-gh-automation.sh some-org/repo
```
This script will pull necessary secrets from the `app.ci` cluster and run the tool locally, checking the provided repo.
