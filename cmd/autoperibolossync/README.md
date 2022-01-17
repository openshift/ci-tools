# autoperibolossync

`autoperibolossync` is a like wrapper over the [private-org-peribolos-sync](../private-org-peribolos-sync) tool.

## What it does

`autoperibolossync` maintains the Peribolos configuration stored in a GitHub repository by executing
the [private-org-peribolos-sync](../private-org-peribolos-sync) over a working copy of a repository containing Peribolos
config and filing a PR to that repository if the underlying tool changes the config.

## Why it exists

Together with [private-org-peribolos-sync](../private-org-peribolos-sync) it
manages [Peribolos](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/peribolos) configuration of the
GitHub repositories in
the [openshift-priv](https://docs.ci.openshift.org/docs/architecture/private-repositories/#openshift-priv-organization) organization. 

At the time when [private-org-peribolos-sync](../private-org-peribolos-sync) was written, there was no shared bump PR
package available, so we tended to write `config-changing-tool | auto-config-changing-tool` pairs when we needed to
maintain certain configuration automatically in GitHub repositories rather than include the PR filing logic in each
configuration changing binary.

## How it works

`autoperibolossync` runs [private-org-peribolos-sync](../private-org-peribolos-sync) over a working copy of a repository
containing Peribolos config. If the execution results in Peribolos changes, the tool submits or updates a pull request
in that repository that updates the mainline with the changes, using
Prow's [generic-autobumper](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/generic-autobumper) package.

## How is it deployed

The periodic
job [periodic-auto-private-org-peribolos-sync](https://deck-internal-ci.apps.ci.l2s4.p1.openshiftapps.com/?job=periodic-auto-private-org-peribolos-sync) ([definition](https://github.com/openshift/release/blob/18cc2328d72e34afc97cbb544618600c5c7fb656/ci-operator/jobs/infra-periodics.yaml#L1398-L1449))
uses `autoperibolossync` to
create [PRs in openshift/config](https://github.com/openshift/config/pulls?q=is%3Apr+is%3Aclosed+Automate+peribolos+configuration%22) (
the repository is private).
