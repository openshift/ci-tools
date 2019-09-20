# Populating `OWNERS` and `OWNERS_ALIASES`

This utility updates the OWNERS files from remote Openshift repositories.

```
$ ./autoowners -h
Usage of ./autoowners:
  -assign string
        The github username or group name to assign the created pull request to. (default "openshift/openshift-team-developer-productivity-test-platform")
  -debug-mode
        Enable the DEBUG level of logs if true.
  -dry-run
        Whether to actually create the pull request with github client (default true)
  -git-email string
        The email to use on the git commit. Requires --git-name. If not specified, uses the system default.
  -git-name string
        The name to use on the git commit. Requires --git-email. If not specified, uses the system default.
  -github-endpoint value
        GitHub's API endpoint (may differ for enterprise). (default https://api.github.com)
  -github-graphql-endpoint string
        GitHub GraphQL API endpoint (may differ for enterprise). (default "https://api.github.com/graphql")
  -github-login string
        The GitHub username to use. (default "openshift-bot")
  -github-token-file string
        DEPRECATED: use -github-token-path instead.  -github-token-file may be removed anytime after 2019-01-01.
  -github-token-path string
        Path to the file containing the GitHub OAuth secret.
  -ignore-repo value
        The repo for which syncing OWNERS file is disabled.
  -target-dir string
        The directory containing the target repo.

```

Upstream repositories are calculated from `ci-operator/jobs/{organization}/{repository}`.
For example, the presence of [`ci-operator/jobs/openshift/origin`](../../ci-operator/jobs/openshift/origin) inserts [openshift/origin][] as an upstream repository.

The `HEAD` branch for each upstream repository is pulled to extract its `OWNERS` and `OWNERS_ALIASES`.
If `OWNERS` is missing, the utility will ignore `OWNERS_ALIASES`, even if it is present upstream.

Any aliases present in the upstream `OWNERS` file will be resolved to the set of usernames they represent in the associated
`OWNERS_ALIASES` file.  The local `OWNERS` files will therefore not contain any alias names.  This avoids any conflicts between 
upstream alias names coming from  different repos.

The utility also iterates through the `ci-operator/{type}/{organization}/{repository}` for `{type}` in `config`, `jobs`, and `templates`, writing `OWNERS` to reflect the upstream configuration.
If the upstream does not have an `OWNERS` file, the utility will ignore syncing it for those paths.

Test it locally with existing image:

```
# git clone https://github.com/openshift/release /tmp/release
# cd /tmp/release
# podman run --entrypoint "/bin/bash" -v "${PWD}:/tmp/release" --workdir /tmp/release -it --rm registry.svc.ci.openshift.org/ci/autoowners
# mkdir /etc/github
# echo github_token > /etc/github/oauth
# /usr/bin/autoowners --github-token=/etc/github/oauth --git-name=openshift-bot --git-email=openshift-bot@redhat.com --target-dir=. --ignore-repo="ci-operator/config/openshift/kubernetes-metrics-server" --ignore-repo="ci-operator/jobs/openshift/kubernetes-metrics-server" --ignore-repo="ci-operator/config/openshift/origin-metrics" --ignore-repo="ci-operator/jobs/openshift/origin-metrics" --ignore-repo="ci-operator/config/openshift/origin-web-console" --ignore-repo="ci-operator/jobs/openshift/origin-web-console" --ignore-repo="ci-operator/config/openshift/origin-web-console-server" --ignore-repo="ci-operator/jobs/openshift/origin-web-console-server" --ignore-repo="ci-operator/jobs/openvswitch/ovn-kubernetes" --ignore-repo="ci-operator/config/openshift/cluster-api-provider-azure" --ignore-repo="ci-operator/config/openshift/csi-driver-registrar" --ignore-repo="ci-operator/config/openshift/csi-external-resizer" --ignore-repo="ci-operator/config/openshift/csi-external-snapshotter" --ignore-repo="ci-operator/config/openshift/csi-livenessprobe" --ignore-repo="ci-operator/config/openshift/knative-build" --ignore-repo="ci-operator/config/openshift/knative-client" --ignore-repo="ci-operator/config/openshift/knative-serving" --ignore-repo="ci-operator/config/openshift/kubernetes" --ignore-repo="ci-operator/config/openshift/sig-storage-local-static-provisioner" --ignore-repo="ci-operator/jobs/openshift/cluster-api-provider-azure" --ignore-repo="ci-operator/jobs/openshift/csi-driver-registrar" --ignore-repo="ci-operator/jobs/openshift/csi-external-resizer" --ignore-repo="ci-operator/jobs/openshift/csi-external-snapshotter" --ignore-repo="ci-operator/jobs/openshift/csi-livenessprobe" --ignore-repo="ci-operator/jobs/openshift/knative-build" --ignore-repo="ci-operator/jobs/openshift/knative-client" --ignore-repo="ci-operator/jobs/openshift/knative-serving" --ignore-repo="ci-operator/jobs/openshift/kubernetes" --ignore-repo="ci-operator/jobs/openshift/sig-storage-local-static-provisioner"

```
