# github-ldap-user-group-creator

## What
Batch job that synchronizes OpenShift `Group` resources across all CI clusters to match the canonical set of Rover/LDAP group memberships and GitHub-to-Kerberos identity mappings. It creates three categories of groups:

1. **Per-GitHub-user groups** (`<login>-group`) -- one member, the user's Kerberos ID, on all clusters except `hive`
2. **Rover groups** (e.g. `test-platform-ci-admins`, team groups) -- resolved memberships from LDAP, with optional cluster targeting and renaming
3. **openshift-priv admins group** (`openshift-priv-admins`) -- admins/members of the openshift-priv GitHub org, mapped to Kerberos IDs, on `app.ci` only

It also optionally deletes OpenShift `User` and `Identity` objects for people no longer present in Rover, and uploads user records to BigQuery for analytics.

This is the second half of a two-stage pipeline: `sync-rover-groups` resolves LDAP memberships and writes YAML files, then this tool consumes those files and applies the resulting groups to clusters.

## How it works -- full flow

1. **Load inputs**:
   - Read the GitHub-to-Kerberos user mapping from `--github-users-file` (YAML array of `rover.User` with `uid`, `github_username`, `cost_center`)
   - Read the resolved Rover group memberships from `--groups-file` (YAML map of group name to Kerberos ID list)
   - Optionally load peribolos config to extract openshift-priv org admins/members
   - Optionally load group config for cluster targeting and renaming

2. **BigQuery upload**: Insert all user records into the `ci_analysis_us.users` table in the `openshift-gce-devel` GCP project with the current timestamp. This enables analytics on CI user populations over time.

3. **Build GitHub-to-Kerberos mapping**: Call `rover.MapGithubToKerberos()` to create a `map[string]string` from GitHub login to Kerberos ID.

4. **Safety checks**:
   - Verify the `test-platform-ci-admins` group exists in the groups file and has at least 3 members; fatal if not
   - Warn if no openshift-priv admins were found

5. **Optionally delete invalid users** (`--delete-invalid-users`): For each cluster, list all OpenShift `User` objects. Delete any whose name is not a known Kerberos ID, along with their associated `Identity` objects. Hard-coded exceptions: `backplane-cluster-admin` is always skipped, ci-admins members are never deleted (safety valve).

6. **Build group map** (`makeGroups`):
   - Per-GitHub-user groups on all clusters except `hive`
   - openshift-priv admins group on `app.ci` only (requires `--peribolos-config`). Logs errors for admins with no GitHub-to-Kerberos mapping unless they are in `--skip-ocp-priv-admin`.
   - Rover groups on their configured clusters (all clusters by default, overridable via config). Groups can be renamed via `rename_to` in the config, with the original name stored in a `rover-group-name` label.

7. **Ensure groups on clusters** (`ensureGroups`):
   - First pass: list all groups on each cluster labeled `dptp.openshift.io/requester: github-ldap-user-group-creator`. Delete any that are no longer in the desired set or no longer targeted to that cluster. Never deletes `test-platform-ci-admins`.
   - Second pass: concurrently (up to `--concurrency` goroutines via semaphore), upsert each group on its target clusters. Validates group names are non-empty and members have no duplicates or blanks before upserting.
   - Upsert logic: attempt `Create`; if `AlreadyExists` and members differ, `Delete` then `Create` (OpenShift Group objects cannot be updated for the `Users` field). If members match, no-op.
   - Retries with exponential backoff (4 steps, factor 2, starting at 1 second).
   - Skips clusters disabled by Prow config.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--github-users-file` | (required) | YAML file with GitHub-to-Kerberos user mappings (produced by `sync-rover-groups`) |
| `--groups-file` | (required) | YAML file with resolved Rover group memberships (produced by `sync-rover-groups`) |
| `--gcp-credentials-file` | (required) | GCP service account JSON for BigQuery writes |
| `--config-file` | `""` | Group config YAML for per-group cluster targeting and renaming |
| `--peribolos-config` | `""` | Peribolos org config file; used to extract openshift-priv admins |
| `--org-from-peribolos-config` | `openshift-priv` | Org to read admins/members from in the peribolos config |
| `--skip-ocp-priv-admin` | (empty) | GitHub logins to exclude from the openshift-priv-admins group (repeatable) |
| `--dry-run` | `true` | When true, log intended changes without modifying clusters |
| `--delete-invalid-users` | `false` | Delete OpenShift User/Identity objects for users not in Rover |
| `--concurrency` | `60` | Max concurrent goroutines for group upsert operations |
| `--log-level` | `info` | Log verbosity |
| Kubernetes flags | (in-cluster) | `--kubeconfig`, `--context`, etc. via Prow's `KubernetesOptions` |

## Key files

- `cmd/github-ldap-user-group-creator/main.go` -- all logic: option parsing, group construction (`makeGroups`), cluster synchronization (`ensureGroups`, `upsertGroup`), BigQuery upload, user deletion
- `pkg/group/config.go` -- `Config` type with `ClusterGroups` and per-group `Target` (cluster targeting via `ResolveClusters`, `RenameTo`)
- `pkg/rover/types.go` -- `User` type (`UID`, `GitHubUsername`, `CostCenter`), `MapGithubToKerberos()` helper
- `pkg/rover/bigquery.go` -- `UserItem` type with `Save()` for BigQuery insertion

## Deployment
Runs as a periodic Prow job (CronJob) on app.ci. Consumes output files from `sync-rover-groups` which runs earlier in the pipeline. Requires kubeconfigs for all CI build clusters and GCP credentials for BigQuery.

All groups it creates are labeled `dptp.openshift.io/requester: github-ldap-user-group-creator` for ownership tracking and cleanup.

## Related
- `cmd/sync-rover-groups` -- upstream: produces the `--groups-file` and `--github-users-file` this tool consumes
- `pkg/api` -- constants: `CIAdminsGroupName`, `DPTPRequesterLabel`, `GitHubUserGroup()`, cluster names

---

## Additional details

### Groups

`github-ldap-user-group-creator` reads
- the mapping files generated by [sync-rover-groups](../sync-rover-groups)
that stores the mapping from `github-id` to its Red Hat `kerberos-id` and for each `github-id`, creates a group `github-id-group`
on each cluster.

- the groups file generated by [sync-rover-groups](../sync-rover-groups) that stores the group names and their members from
the Red Hat LDAP server and for each group creates a group on each cluster.

### Deleting users

This tool is also responsible for deleting the users and their identities on all clusters when they no longer exist in Rover.
> __Note__
> Users that are not part of any group or don't have their github account linked in their Rover profile are deleted as well.

### How is it deployed

The periodic
job [periodic-github-ldap-user-group-creator](https://deck-internal-ci.apps.ci.l2s4.p1.openshiftapps.com/?job=periodic-github-ldap-user-group-creator) ([definition](https://github.com/openshift/release/blob/main/ci-operator/jobs/infra-periodics.yaml))
uses `github-ldap-user-group-creator` to create the groups.
The service account RBACs are defined in [admin_github-ldap-user-group-creator_rbac.yaml](https://github.com/openshift/release/blob/main/clusters/build-clusters/common/assets/admin_github-ldap-user-group-creator_rbac.yaml)
