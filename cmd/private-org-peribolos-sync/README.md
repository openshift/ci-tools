# private-org-peribolos-sync

## What
CLI tool that generates the repository section of a Peribolos configuration file for the `openshift-priv` GitHub organization. It discovers which repos need private mirrors (by scanning ci-operator configs for repos that build official images, plus a whitelist), fetches their GitHub metadata, and writes the resulting repo definitions into the Peribolos config.

Peribolos is the tool that manages GitHub organization membership and repository settings declaratively. This tool ensures that every repo with a private mirror has correct settings (description, merge methods, archive status, etc.) in the `openshift-priv` org.

## How it works -- full flow

1. Read the existing Peribolos config file (`--peribolos-config`), which may be gzip-compressed
2. Initialize a GitHub client for API calls
3. Walk the ci-operator config directory (`{release-repo-path}/ci-operator/config/`) to find all repos that build official images (`api.BuildsAnyOfficialImages` with `WithoutOKD`)
4. Add all repos from the whitelist file (these bypass the `--only-org` filter)
5. For each discovered `{org}/{repo}`, call `gc.GetRepo(org, repo)` to fetch the current GitHub repository metadata:
   - Description, Homepage, HasIssues, HasProjects, HasWiki
   - AllowMergeCommit, AllowSquashMerge, AllowRebaseMerge
   - Archived, DefaultBranch
6. Compute the private repo name using `MirroredRepoName()`:
   - Flattened orgs: repo name unchanged
   - Non-flattened orgs: `{org}-{repo}` prefix
7. Build an `org.Repo` for each, always setting `Private: true`
8. Apply `org.PruneRepoDefaults()` to remove values that match Peribolos defaults
9. Replace the `Repos` map in the destination org's config with the generated repos
10. Marshal and write the updated Peribolos config back to the same file

### Important behavior
- The tool completely replaces the `Repos` section of the destination org. It does not merge with existing repo entries.
- The GitHub API call for each repo can be slow for large numbers of repos, as they are fetched sequentially.
- If a repo fails to fetch (e.g. deleted or renamed), the tool fatals.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--peribolos-config` | (required) | Path to the Peribolos YAML config file to update |
| `--release-repo-path` | (required) | Path to openshift/release repository directory |
| `--destination-org` | (required) | Name of the Peribolos org to populate (typically `openshift-priv`) |
| `--only-org` | `""` | Only include repos from this source organization |
| `--flatten-org` | (repeatable) | Additional orgs whose repos should not have org prefix |
| `--whitelist-file` | `""` | Path to YAML file listing repos to include regardless of official image status |
| GitHub flags | | Standard Prow GitHub options (`--github-token-path`, etc.) |

## Key files
- `cmd/private-org-peribolos-sync/main.go` -- all logic in this single file
- `pkg/privateorg/flatten.go` -- `MirroredRepoName()` and `DefaultFlattenOrgs`
- `pkg/config/whitelist.go` -- whitelist configuration

## Deployment
CLI tool. Not a long-running service. Called by `auto-peribolos-sync` which wraps it in a PR workflow. Packaged in the `ci_auto-peribolos-sync_latest` container image alongside `auto-peribolos-sync`.
## Repository Naming Convention

To prevent collisions when multiple organizations have repositories with the same name, this tool uses a special naming convention:
- Repositories from the organization specified by `--only-org` keep their original names
- Repositories from organizations specified by `--flatten-org` keep their original names (can be specified multiple times)
- Repositories from the following default organizations always keep their original names for backwards compatibility:
  - `openshift`
  - `openshift-eng`
  - `operator-framework`
  - `redhat-cne`
  - `openshift-assisted`
  - `ViaQ`
- All other repositories are named as `<org>-<repo>`

For example, with `--only-org=openshift --flatten-org=migtools`:
- `openshift/must-gather` → `openshift-priv/must-gather` (from --only-org and default)
- `openshift-eng/ocp-build-data` → `openshift-priv/ocp-build-data` (from default)
- `migtools/crane` → `openshift-priv/crane` (from --flatten-org)
- `redhat-cne/cloud-event-proxy` → `openshift-priv/cloud-event-proxy` (from default)
- `custom-org/some-repo` → `openshift-priv/custom-org-some-repo` (not in flatten list)
