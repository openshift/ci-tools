# sync-rover-groups

## What it does

`sync-rover-groups` resolves the members of [Rover](https://rover.redhat.com/groups/) groups referenced
in the [cluster manifests](https://github.com/openshift/release/tree/main/clusters) of the
[openshift/release](https://github.com/openshift/release) repository.

It produces two output files:
- **`groups.yaml`** — a mapping of Rover Group names to their resolved members (Kerberos IDs)
- **`users.yaml`** — a YAML sequence of user records (GitHub username, Kerberos ID, etc.) for every
  user who has set up a GitHub URL in their [Rover profile](https://rover.redhat.com/)

These files are used downstream by
[github-ldap-user-group-creator](../github-ldap-user-group-creator) to create OpenShift `Group`
resources on CI clusters.

## Why it exists

We want to avoid maintaining lists of individual logins in our CI cluster manifests and rely on
[Rover Groups](https://rover.redhat.com/groups/) instead. The tool `sync-rover-groups` discovers the
groups that are expected to exist on OpenShift CI clusters and resolves their members so they can be
applied to the clusters.

## How it works

1. Scans the `--manifest-dir` (typically the `clusters/` tree in `openshift/release`) to discover
   which Rover groups are referenced in cluster RBAC manifests.
2. Loads the `--config-file`
   ([`core-services/sync-rover-groups/_config.yaml`](https://github.com/openshift/release/blob/main/core-services/sync-rover-groups/_config.yaml)
   in the release repo) which controls group renaming, extra groups, cluster targeting, and
   secret-collection definitions.
3. Queries the Red Hat corporate LDAP server (`ldap.corp.redhat.com`) using an authenticated bind
   to resolve each group's members.
4. Writes the resolved `groups.yaml` and `users.yaml` files to disk.

### LDAP authentication

LDAP bind credentials are required. They can be provided via CLI flags (`--ldap-bind-dn` /
`--ldap-bind-password-file`) or environment variables (`LDAP_BIND_DN` / `LDAP_BIND_PASSWORD`).

The `--validate-subjects` and `--print-config` modes do not use LDAP.

## How it is deployed

The tool runs via a
[CronJob](https://github.com/openshift/release/blob/main/clusters/core-ci/sync-rover-groups/cronjob.yaml)
on the **`core-ci`** cluster. The CronJob runs the tool and then stores the output as
`ConfigMap/sync-rover-groups` in `namespace/ci` on the `app.ci` cluster.

Thirty minutes later, the Prow periodic job
[`periodic-github-ldap-user-group-creator`](https://github.com/openshift/release/blob/main/ci-operator/jobs/infra-periodics.yaml)
reads that ConfigMap and creates/updates the corresponding OpenShift `Group` resources on all CI
build clusters. See [github-ldap-user-group-creator](../github-ldap-user-group-creator) for details.
