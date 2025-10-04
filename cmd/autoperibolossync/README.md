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

The tool uses a special naming convention to prevent repository name collisions:
- Repositories from the organization specified by `--only-org` keep their original names
- Repositories from organizations specified by `--flatten-org` keep their original names (can be specified multiple times)
- Repositories from the following default organizations always keep their original names for backwards compatibility:
  - `openshift`
  - `openshift-eng`
  - `operator-framework`
  - `redhat-cne`
  - `openshift-assisted`
  - `ViaQ`
- Repositories from other organizations are named as `<org>-<repo>` in the private organization

For example, with `--only-org=openshift --flatten-org=migtools`:
- `openshift/must-gather` becomes `openshift-priv/must-gather` (from --only-org and default)
- `openshift-eng/ocp-build-data` becomes `openshift-priv/ocp-build-data` (from default)
- `migtools/crane` becomes `openshift-priv/crane` (from --flatten-org)
- `redhat-cne/cloud-event-proxy` becomes `openshift-priv/cloud-event-proxy` (from default)
- `custom-org/some-repo` becomes `openshift-priv/custom-org-some-repo` (not in flatten list)

This ensures that repositories from different organizations can coexist in the private organization without naming conflicts.

## How is it deployed

The periodic
job [periodic-auto-private-org-peribolos-sync](https://deck-internal-ci.apps.ci.l2s4.p1.openshiftapps.com/?job=periodic-auto-private-org-peribolos-sync) ([definition](https://github.com/openshift/release/blob/18cc2328d72e34afc97cbb544618600c5c7fb656/ci-operator/jobs/infra-periodics.yaml#L1398-L1449))
uses `autoperibolossync` to
create [PRs in openshift/config](https://github.com/openshift/config/pulls?q=is%3Apr+is%3Aclosed+Automate+peribolos+configuration%22) (
the repository is private).
