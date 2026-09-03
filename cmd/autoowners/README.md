# autoowners

## What
Batch job that syncs OWNERS files from upstream source repositories into the openshift/release repository. For every org/repo that has CI configuration in openshift/release (under `ci-operator/jobs`, `ci-operator/config`, or `ci-operator/templates`), autoowners fetches the root-level `OWNERS` and `OWNERS_ALIASES` files from the upstream repo on GitHub, resolves aliases, filters out users who are not members of the downstream GitHub organization, and writes the resolved OWNERS files into the corresponding directories in openshift/release. It then commits and opens (or updates) a pull request with the changes.

This ensures that the people who own code upstream also control CI job approvals downstream, without manual synchronization.

## How it works -- full flow

1. **Discover repos**: Walk the `ci-operator/{jobs,config,templates}` subdirectories (and any `--extra-config-dir` paths) under `--target-dir` to build a list of org/repo pairs that have CI configuration. Skip the target repo itself (openshift/release) and any repos or orgs on the blocklist (`--ignore-repo`, `--ignore-org`). Each org/repo maps to one or more directories where an OWNERS file should be written.

2. **Fetch upstream OWNERS**: For each discovered org/repo, use the GitHub API (`GetFile`) to fetch the root `OWNERS` file and `OWNERS_ALIASES` file. If no OWNERS file exists upstream, log a warning and skip the repo. Strip `@` prefixes from usernames (common in upstream OWNERS files). Quote purely numeric GitHub usernames with double quotes so YAML parsers treat them as strings instead of integers.

3. **Resolve aliases**: If an `OWNERS_ALIASES` file was found, expand all alias references in the OWNERS file to their constituent usernames. The plugin supports both simple OWNERS format (flat approvers/reviewers lists) and full OWNERS format (filter-based with path patterns).

4. **Filter to org members**: Query the downstream GitHub org (default: `openshift`) for its complete member list via `ListOrgMembers(org, "all")`. Remove any user from the resolved OWNERS who is not a member of the downstream org. All comparisons are case-insensitive.

5. **Set reviewers fallback**: If the resolved OWNERS has approvers but an empty reviewers list, copy the approvers into reviewers. This matches the behavior Prow expects.

6. **Write OWNERS files**: For each directory associated with the org/repo, delete the existing OWNERS file and write the resolved content. Prepend a header comment with five lines: the "DO NOT EDIT" warning, the source URL, a note about alias expansion, a note about org member filtering, and a link to the OWNERS docs.

7. **Detect changes**: Run `git status --porcelain` to find modified OWNERS files. Verify that only OWNERS files were modified (error out if anything else changed). If nothing changed, exit cleanly.

8. **Commit and push**: Use `bumper.GitCommitSignoffAndPush` to commit changes and push to a remote branch (`autoowners`) on the bot's fork. The commit message includes the PR title with a timestamp.

9. **Create or update PR**: Call `bumper.UpdatePullRequestWithLabels` to open or update a pull request against the target repo's base branch. The PR body lists all directories that had OWNERS changes and `/cc`s the assignee. The `rehearsals-ack` label is always added (to skip pj-rehearse). If `--self-approve` is set, `approved` and `lgtm` labels are also added for auto-merge. The PR body is truncated to 65535 characters if it exceeds GitHub's limit.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--dry-run` | `true` | When true, do not actually create the PR |
| `--github-login` | `openshift-bot` | GitHub username for push and PR creation |
| `--org` | `openshift` | Downstream GitHub org name (used for member filtering) |
| `--repo` | `release` | Downstream GitHub repository name |
| `--git-name` | (system default) | Git author name for commits |
| `--git-email` | (system default) | Git author email for commits |
| `--git-signoff` | `false` | Whether to add `Signed-off-by` to commits |
| `--assign` | `openshift/test-platform` | GitHub user or team to `/cc` on the PR |
| `--target-dir` | (required) | Path to the local clone of the target repo |
| `--target-subdir` | `ci-operator` | Subdirectory under target-dir where configs live |
| `--config-subdir` | `jobs,config,templates` | Subdirectories to scan for org/repo dirs (repeatable) |
| `--extra-config-dir` | (none) | Additional directories to scan (repeatable) |
| `--ignore-repo` | (none) | Repos to skip, in `org/repo` format (repeatable) |
| `--ignore-org` | (none) | Entire orgs to skip (repeatable) |
| `--debug-mode` | `false` | Enable DEBUG-level logging |
| `--self-approve` | `false` | Add `approved` and `lgtm` labels to the PR |
| `--pr-base-branch` | `master` | Base branch for the PR |
| `--plugin-config` | (none) | Path to Prow plugin config (for custom OWNERS filenames per repo) |

## Key files
- `cmd/autoowners/main.go` -- entire implementation: repo discovery, OWNERS fetching, alias resolution, member filtering, PR creation

## Deployment
Runs as a periodic Prow job ([recent runs](https://prow.ci.openshift.org/?job=periodic-prow-auto-owners)), not a long-lived service. Typically scheduled to run on a regular cadence (e.g., daily) against a checkout of openshift/release.
```console
$ autoowners -h
Usage of autoowners:
  -assign string
    	The github username or group name to assign the created pull request to. (default "openshift/test-platform")
  -config-subdir value
    	The sub-directory where configuration is stored. (Default list of directories: jobs,config,templates)
  -debug-mode
    	Enable the DEBUG level of logs if true.
  -dry-run
    	Whether to actually create the pull request with github client (default true)
  -extra-config-dir value
    	The directory path from the repo root where extra configuration is stored.
  -git-email string
    	The email to use on the git commit. Requires --git-name. If not specified, uses the system default.
  -git-name string
    	The name to use on the git commit. Requires --git-email. If not specified, uses the system default.
  -git-signoff
    	Whether to signoff the commit. (https://git-scm.com/docs/git-commit#Documentation/git-commit.txt---signoff)
  -github-allowed-burst int
    	Size of token consumption bursts. If set, --github-hourly-tokens must be positive too and set to a higher or equal number.
  -github-app-id string
    	ID of the GitHub app. If set, requires --github-app-private-key-path to be set and --github-token-path to be unset.
  -github-app-private-key-path string
    	Path to the private key of the github app. If set, requires --github-app-id to bet set and --github-token-path to be unset
  -github-endpoint value
    	GitHub's API endpoint (may differ for enterprise). (default https://api.github.com)
  -github-graphql-endpoint string
    	GitHub GraphQL API endpoint (may differ for enterprise). (default "https://api.github.com/graphql")
  -github-host string
    	GitHub's default host (may differ for enterprise) (default "github.com")
  -github-hourly-tokens int
    	If set to a value larger than zero, enable client-side throttling to limit hourly token consumption. If set, --github-allowed-burst must be positive too.
  -github-login string
    	The GitHub username to use. (default "openshift-bot")
  -github-throttle-org value
    	Throttler settings for a specific org in org:hourlyTokens:burst format. Can be passed multiple times. Only valid when using github apps auth.
  -github-token-path string
    	Path to the file containing the GitHub OAuth secret.
  -ignore-org value
    	The orgs for which syncing OWNERS file is disabled.
  -ignore-repo value
    	The repo for which syncing OWNERS file is disabled.
  -org string
    	The downstream GitHub org name. (default "openshift")
  -plugin-config string
    	Path to plugin config file.
  -pr-base-branch string
    	The base branch to use for the pull request. (default "master")
  -repo string
    	The downstream GitHub repository name. (default "release")
  -self-approve
    	Self-approve the PR by adding the approved and `lgtm` labels. Requires write permissions on the repo.
  -supplemental-plugin-config-dir value
    	An additional directory from which to load plugin configs. Can be used for config sharding but only supports a subset of the config. The flag can be passed multiple times.
  -supplemental-plugin-configs-filename-suffix string
    	Suffix for additional plugin configs. Only files with this name will be considered (default "_pluginconfig.yaml")
  -target-dir string
    	The directory containing the target repo.
  -target-subdir string
    	The sub-directory of the target repo where the configurations are stored. (default "ci-operator")
```

Upstream repositories are calculated from `{target-subdir}/{config-subdir[0]}/{organization}/{repository}`.
For example, given  `... -target-subdir=ci-operator -config-subdir=jobs,... ...` the presence of [`ci-operator/jobs/openshift/origin`][openshift/origin-jobs] inserts [openshift/origin][] as an upstream repository.

The `HEAD` branch for each upstream repository is pulled to extract its `OWNERS` and `OWNERS_ALIASES`.
If `OWNERS` is missing, the utility will ignore `OWNERS_ALIASES`, even if it is present upstream.

Any aliases present in the upstream `OWNERS` file will be resolved to the set of usernames they represent in the associated
`OWNERS_ALIASES` file.  The local `OWNERS` files will therefore not contain any alias names.  This avoids any conflicts between 
upstream alias names coming from  different repos.

The utility also iterates through the `{target-subdir}/{type}/{organization}/{repository}` for `{type}` in `config`, `jobs`, and `templates`, writing `OWNERS` to reflect the upstream configuration.
If the upstream does not have an `OWNERS` file, the utility will ignore syncing it for those paths.

Test it locally with existing image:

```console
$ git clone https://github.com/openshift/release /tmp/release
$ cd /tmp/release
$ podman run --entrypoint "/bin/bash" -v "${PWD}:/tmp/release":z --workdir /tmp/release -it --rm registry.ci.openshift.org/ci/autoowners
$ mkdir /etc/github
$ echo github_token > /etc/github/oauth
$ /usr/bin/autoowners --github-token-path=/etc/github/oauth --git-name=openshift-bot --git-email=openshift-bot@redhat.com --target-dir=. --ignore-repo="ci-operator/config/openshift/kubernetes-metrics-server" --ignore-repo="ci-operator/jobs/openshift/kubernetes-metrics-server" --ignore-repo="ci-operator/config/openshift/origin-metrics" --ignore-repo="ci-operator/jobs/openshift/origin-metrics" --ignore-repo="ci-operator/config/openshift/origin-web-console" --ignore-repo="ci-operator/jobs/openshift/origin-web-console" --ignore-repo="ci-operator/config/openshift/origin-web-console-server" --ignore-repo="ci-operator/jobs/openshift/origin-web-console-server" --ignore-repo="ci-operator/jobs/openvswitch/ovn-kubernetes" --ignore-repo="ci-operator/config/openshift/cluster-api-provider-azure" --ignore-repo="ci-operator/config/openshift/csi-driver-registrar" --ignore-repo="ci-operator/config/openshift/csi-external-resizer" --ignore-repo="ci-operator/config/openshift/csi-external-snapshotter" --ignore-repo="ci-operator/config/openshift/csi-livenessprobe" --ignore-repo="ci-operator/config/openshift/knative-build" --ignore-repo="ci-operator/config/openshift/knative-client" --ignore-repo="ci-operator/config/openshift/knative-serving" --ignore-repo="ci-operator/config/openshift/kubernetes" --ignore-repo="ci-operator/config/openshift/sig-storage-local-static-provisioner" --ignore-repo="ci-operator/jobs/openshift/cluster-api-provider-azure" --ignore-repo="ci-operator/jobs/openshift/csi-driver-registrar" --ignore-repo="ci-operator/jobs/openshift/csi-external-resizer" --ignore-repo="ci-operator/jobs/openshift/csi-external-snapshotter" --ignore-repo="ci-operator/jobs/openshift/csi-livenessprobe" --ignore-repo="ci-operator/jobs/openshift/knative-build" --ignore-repo="ci-operator/jobs/openshift/knative-client" --ignore-repo="ci-operator/jobs/openshift/knative-serving" --ignore-repo="ci-operator/jobs/openshift/kubernetes" --ignore-repo="ci-operator/jobs/openshift/sig-storage-local-static-provisioner"
```

[openshift/origin]: https://github.com/openshift/origin
[openshift/origin-jobs]: https://github.com/openshift/release/tree/main/ci-operator/jobs/openshift/origin
