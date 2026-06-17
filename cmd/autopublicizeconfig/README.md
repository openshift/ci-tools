# autopublicizeconfig

## What
Automation tool that generates the configuration file for the `publicize` plugin by discovering all repos that need private-to-public mirroring. It scans ci-operator configs and the whitelist to find repos building official images, computes the `openshift-priv/{repo}` to `{org}/{repo}` mapping, writes the config, and creates a PR on `openshift/release`.

It keeps the publicize config in sync as repos are added to or removed from the private org.

## How it works -- full flow

1. Initialize GitHub client and secrets
2. Scan ci-operator configs at `{release-repo-path}/ci-operator/config/` for repos that build official images (`api.BuildsAnyOfficialImages` with `WithoutOKD`)
3. Add all repos from the whitelist file
4. For each discovered `{org}/{repo}`:
   - Compute the private repo name using `MirroredRepoName()` with the flattened orgs set
   - Create the mapping: `openshift-priv/{mirroredName}` -> `{org}/{repo}`
5. Marshal the config as YAML and write to `--publicize-config` path, creating directories as needed
6. Check for git changes using `bumper.HasChanges()`
7. If no changes, exit cleanly
8. If running in dry-run mode, log and exit
9. If changes exist and not dry-run:
   - Create a commit with title: `"Automate publicize configuration sync {RFC1123 timestamp}"`
   - Push to the `auto-publicize-sync` branch on `openshift/release`
   - Create or update a PR via `bumper.UpdatePullRequestWithLabels()`:
     - Target: `openshift/release` default branch
     - Source: `{github-login}:auto-publicize-sync`
     - Description: "Updates the publicize plugin configuration"
   - If `--self-approve` is set, add `approved` and `lgtm` labels

## Generated config format
```yaml
repositories:
  openshift-priv/installer: openshift/installer
  openshift-priv/cluster-version-operator: openshift/cluster-version-operator
  openshift-priv/stolostron-multicloud-operators-subscription: stolostron/multicloud-operators-subscription
```

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--dry-run` | `true` | When true, writes config but does not create PR |
| `--self-approve` | `false` | Add `approved` and `lgtm` labels to the PR |
| `--github-login` | `openshift-bot` | GitHub username for push and PR creation |
| `--git-name` | `""` | Git commit author name (must pair with `--git-email`) |
| `--git-email` | `""` | Git commit author email (must pair with `--git-name`) |
| `--publicize-config` | (required) | Path where the generated publicize config will be written |
| `--release-repo-path` | (required) | Path to openshift/release repository directory |
| `--flatten-org` | (repeatable) | Additional orgs whose repos should not have org prefix |
| `--whitelist-file` | `""` | Path to YAML file listing repos to include |
| GitHub flags | | Standard Prow GitHub options |

## Key files
- `cmd/autopublicizeconfig/main.go` -- all logic in this single file
- `pkg/privateorg/flatten.go` -- `MirroredRepoName()` naming logic

## Deployment
Periodic Prow job. Creates PRs against the `openshift/release` repository on branch `auto-publicize-sync`.

## Related
- `cmd/ci-operator-config-mirror` -- uses the same repo discovery logic for a different purpose
