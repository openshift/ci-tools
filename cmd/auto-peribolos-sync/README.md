# auto-peribolos-sync

## What
Automation wrapper that runs `private-org-peribolos-sync`, detects changes, commits them, and creates or updates a pull request on the `openshift/config` repository. This is the outer orchestration layer; the actual Peribolos config generation is delegated to `private-org-peribolos-sync`.

## How it works -- full flow

1. Initialize GitHub client and secrets
2. Shell out to `/usr/bin/private-org-peribolos-sync` with the provided arguments:
   - `--destination-org openshift-priv`
   - `--peribolos-config {path}`
   - `--release-repo-path {path}`
   - `--github-token-path {path}`
   - Optionally `--whitelist-file`, `--only-org`, `--flatten-org`
3. After the subprocess completes, check for git changes using `bumper.HasChanges()`
4. If no changes, exit cleanly
5. If changes exist:
   - Create a commit with title: `"Automate peribolos configuration sync {RFC1123 timestamp}"`
   - Push to the `auto-peribolos-sync` branch. With token auth, pushes to the bot's fork (`github.com/{github-login}/config`); with GitHub App auth, pushes directly to the `openshift/config` repo
   - Create or update a pull request via `bumper.UpdatePullRequestWithLabels()`:
     - Target: `openshift/config` default branch
     - Source: `{github-login}:auto-peribolos-sync`
     - Description: "Updates the repositories of the openshift-priv organization"
   - If `--self-approve` is set, add `approved` and `lgtm` labels to the PR

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--dry-run` | `true` | When true, uses API tokens but does not create the PR |
| `--self-approve` | `false` | Add `approved` and `lgtm` labels to the PR |
| `--github-login` | `openshift-bot` | GitHub username for push and PR creation |
| `--git-name` | `""` | Git commit author name (must pair with `--git-email`) |
| `--git-email` | `""` | Git commit author email (must pair with `--git-name`) |
| `--peribolos-config` | (required) | Path to the Peribolos config file to update |
| `--release-repo-path` | (required) | Path to openshift/release repository directory |
| `--whitelist-file` | `""` | Path to whitelist file, passed through to `private-org-peribolos-sync` |
| `--only-org` | `""` | Only sync repos from this org, passed through |
| `--flatten-org` | (repeatable) | Additional flatten orgs, passed through |
| GitHub flags | | Standard Prow GitHub options |

## Key files
- `cmd/auto-peribolos-sync/main.go` -- all logic in this single file
- `cmd/private-org-peribolos-sync/main.go` -- the actual config generation tool it shells out to

## Deployment
The periodic job [periodic-auto-private-org-peribolos-sync](https://deck-internal-ci.apps.ci.l2s4.p1.openshiftapps.com/?job=periodic-auto-private-org-peribolos-sync) ([definition](https://github.com/openshift/release/blob/18cc2328d72e34afc97cbb544618600c5c7fb656/ci-operator/jobs/infra-periodics.yaml#L1398-L1449))
uses `auto-peribolos-sync` to create [PRs in openshift/config](https://github.com/openshift/config/pulls?q=is%3Apr+is%3Aclosed+Automate+peribolos+configuration%22) (the repository is private).

The container image (`ci_auto-peribolos-sync_latest`) bundles both `auto-peribolos-sync` and `private-org-peribolos-sync` binaries.

Together with [private-org-peribolos-sync](../private-org-peribolos-sync) it manages [Peribolos](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/peribolos) configuration of the GitHub repositories in the [openshift-priv](https://docs.ci.openshift.org/architecture/private-repositories/#openshift-priv-organization) organization.

## Related
- `cmd/private-org-peribolos-sync` -- the tool this wrapper executes
- The PR targets `openshift/config` which holds the [Peribolos](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/peribolos) config for all OpenShift GitHub orgs
