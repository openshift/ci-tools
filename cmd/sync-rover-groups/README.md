# sync-rover-groups

## What it does

`sync-rover-groups` is a tool to resolve the groups in [the manifests](https://github.com/openshift/release/tree/main/clusters) of CI clusters
in the release repo. Its result is a configuration file consumed by [github-ldap-user-group-creator](../github-ldap-user-group-creator).

It can also generate the mapping file in yaml format: `m(GitHubID)=KerberosID` for each user
that set up GitHub URL at Rover.


## Why it exists

For various reasons we decided that we want to avoid maintaining lists of logins in our manifests,
and rely on Rover Groups instead. The tool `sync-rover-groups` discovers the groups that we expect
to exist in OpenShift CI clusters and resolves their members so that they can be applied to the
clusters.


## How it works

`sync-rover-groups` collects the groups in the manifests and resolves their members by querying the Red Hat LDAP server, 
and saves the resolved groups in a file.

## How is it deployed

The cronjob [sync-rover-groups-update](https://console-openshift-console.apps.ocp-c1.prod.psi.redhat.com/k8s/ns/ocp-test-platform/batch~v1~CronJob/sync-rover-groups-update) ([definition](https://github.com/openshift/release/blob/main/ci-operator/jobs/infra-periodics.yaml))
uses `sync-rover-groups` to generate the groups file which is used to form `configMap/sync-rover-groups` in `project/ci` on the `app.ci` cluster.
