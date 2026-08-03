# sync-rover-groups

## What
Resolves Red Hat LDAP Rover group memberships by scanning Kubernetes manifest directories for group references and querying the corporate LDAP server to resolve each group's member list. Outputs a YAML file with resolved group memberships consumed by `github-ldap-user-group-creator`, and optionally (when `--github-users-file` is set) a second YAML file with GitHub-to-Kerberos user mappings.

This is the first half of the group sync pipeline. It runs on the Red Hat intranet (PSI cluster) because it needs direct LDAP access.

## How it works -- full flow

### Group name collection
1. Walk each `--manifest-dir` directory recursively, parsing every `.yaml` file (skipping symlinks, `_`-prefixed dirs/files).
2. Decode each YAML document using the Kubernetes codec. Supported resource types:
   - **RoleBinding / ClusterRoleBinding**: extract group names from `subjects` where `kind: Group` (ignoring `system:` prefixed groups; template variables `${...}` cause a fatal error)
   - **List**: recurse into list items
   - **Template**: recurse into template objects (tolerates template processing errors like `${{REPLICAS}}`)
   - **Group** (userv1): detected but only used for validation mode
3. Always include `test-platform-ci-admins` in the group set regardless of what was found in manifests.
4. If a group config file is provided, add any groups defined there and remove groups that have been renamed (the original name is resolved instead).

### Validation mode (`--validate-subjects`)
When run as a presubmit (no intranet access), the tool validates manifests without connecting to LDAP:
- Ensures no `User` subjects appear in RoleBindings/ClusterRoleBindings
- Ensures no `Group` resources are created
- Does not resolve groups or generate output files

### Group resolution
For each collected group name, query LDAP:
- Filter: `(&(objectClass=rhatRoverGroup)(cn=<name>))`
- Base DN: `dc=redhat,dc=com`
- Extract `uniqueMember` attributes, parse UIDs from DNs (`uid=<id>,ou=users,...`)
- Groups not found in LDAP are logged as warnings and skipped (except `test-platform-ci-admins`, which is fatal)
- `test-platform-ci-admins` must have at least 3 members

### GitHub user collection (`--github-users-file`)
When this flag is set, additionally query LDAP for all users with a GitHub social URL:
- Filter: `(rhatSocialURL=GitHub*)`
- Base DN: `ou=users,dc=redhat,dc=com`
- Extract `uid`, `rhatSocialURL` (parsed to get GitHub username), `rhatCostCenter`
- Write the result as a YAML array of `rover.User` objects

### Config printing (`--print-config`)
If `--print-config` is set with `--config-file`, print the normalized config (sorted, no comments) and exit.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--manifest-dir` | (required, repeatable) | Directories containing Kubernetes manifests to scan for group references |
| `--groups-file` | `/tmp/groups.yaml` | Output path for resolved group memberships YAML |
| `--github-users-file` | `""` | Output path for GitHub-to-Kerberos user mapping YAML |
| `--config-file` | `""` | Group config YAML for cluster targeting and renaming |
| `--ldap-server` | `ldap.corp.redhat.com` | LDAP server hostname |
| `--validate-subjects` | `false` | Run in validation mode (no LDAP, check manifests only) |
| `--print-config` | `false` | Print normalized config and exit |
| `--log-level` | `info` | Log verbosity |

## Key files

- `cmd/sync-rover-groups/main.go` -- entry point, option parsing, orchestration (`roverGroups()`)
- `cmd/sync-rover-groups/ldap.go` -- `ldapGroupResolver`: LDAP queries for group resolution and GitHub user collection, `getGitHubID()` parser
- `cmd/sync-rover-groups/yamlgroupcollector.go` -- `yamlGroupCollector`: walks manifest directories, decodes YAML, extracts group names from RBAC subjects
- `pkg/group/config.go` -- `Config`, `GroupConfig` types

## Deployment
The cronjob [sync-rover-groups-update](https://console-openshift-console.apps.ocp-c1.prod.psi.redhat.com/k8s/ns/ocp-test-platform/batch~v1~CronJob/sync-rover-groups-update) ([definition](https://github.com/openshift/release/blob/main/ci-operator/jobs/infra-periodics.yaml))
runs on the PSI cluster (`ocp-test-platform--runtime-int` namespace) because it requires Red Hat intranet access to reach the LDAP server. Its output (`groups.yaml`, GitHub users file) is used to form `configMap/sync-rover-groups` in `project/ci` on the `app.ci` cluster, consumed by `github-ldap-user-group-creator`.

When run as a presubmit with `--validate-subjects`, it checks that manifests don't create Groups or use User subjects directly (since the presubmit environment has no intranet access).

## Related
- `cmd/github-ldap-user-group-creator` -- downstream: consumes the output files
- `pkg/rover/types.go` -- `User` type used for the GitHub users file
