# Populating `OWNERS` and `OWNERS_ALIASES`

This utility updates the `OWNERS` files from remote OpenShift repositories.

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
  -self-approve approved
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
