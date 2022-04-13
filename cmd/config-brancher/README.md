# config-brancher

This tool is intended to make the process of branching and duplicating configuration for the CI Operator easy across
many repositories.

## What it does

The tool has two main modes of operations. The first mode is mirroring the CI Operator configuration from the branch
where the active development occurs (such as `main`) to future release branches.

In the second mode, the tool is used to "bump" the configuration file versions, retargeting the configuration for
the current development branch to the next version during the code freeze.

In both modes, it also enforces the basic promotion destination consistency.

## Why it exists

The tool allows the users to only maintain a single CI Operator configuration file: the one for main development branch.
The changes done in this file will then be automatically propagated to future release branches, reducing the surface for
omissions and mistakes.

In the bump mode, the tool allows Test Platform to automatically perform the CI configuration changes on code freeze (
a.k.a "branch cut").

## How it works

The tool iterates over all ci-operator config files in openshift/release, and it selects a set of repositories to
operate on by looking at which are actively promoting images into a specific OpenShift release, provided
by `--current-release`. Branches of repos that actively promote to this release are considered to be the dev branches.

Once the tool selects the set of configurations to operate on, it does one of two actions: Mirror the configuration out,
or bump the configuration files.

### Mirroring configuration

In the mirror configuration mode, it copies the development branch configuration to all branches for the provided
`--future-release` values. The development branch version is not changed. Since this results in both the development
branch and one of the release branches promoting to the same release ImageStream, promotion is disabled in the release
branch for the version which matches that in the promotion stanza of the development branch. This ensures only one
branch feeds that release ImageStream.

### Bumping configuration

In the bumping mode, it moves the development branch to promote to the version in the `--bump` flag, enabling the
promotion in the release branch that used to match the dev branch version and disabling promotion in the release branch
that now matches the dev branch version.

## How is it deployed

In the mirroring mode, the tool is executed regularly in
the [`periodic-prow-auto-config-brancher`](https://prow.ci.openshift.org/?job=periodic-prow-auto-config-brancher)
job ([definition](https://github.com/openshift/release/blob/b499e5ffbe38d07f43587b9100d774cb338a7127/ci-operator/jobs/infra-periodics.yaml#L877-L924))
as a part of the [`autoconfigbrancher`](../autoconfigbrancher) tool.

During the [code freeze event](https://docs.google.com/document/d/19kxmzXFnXrbLChZXBfxy68mCYg06j3qT93VF23wepog/edit#heading=h.gc58v1ksasfp), the tool is used manually in the bump mode,

## Example

During the development of the OCP version 4.X, the components are expected to have the following configuration files in
the openshift/release repository:

```
ci-operator/config/org/repo/org-repo-master.yaml         # Main development branch, promotes to OCP 4.X
ci-operator/config/org/repo/org-repo-release-4.X.yaml    # Release branch for OCP 4.X, promotes nowhere
ci-operator/config/org/repo/org-repo-release-4.X+1.yaml  # Release branch for OCP 4.X+1, promotes to OCP 4.X+1
```

The users are expected to only maintain the `org-repo-master.yaml` file. In the mirroring mode, the tool propagates
any changes done in that file to both `org-repo-release-4.X.yaml` and `org-repo-release-4.X+1.yaml` files, making sure
that these branches will eventually get the same CI treatment after they become actively used.

At 4.X code freeze, the tool will be executed in the bump mode. It will "retarget" the `org-repo-master.yaml` file to
promote to OCP 4.X+1, make `org-repo-release-4.X.yaml` promote to OCP 4.X and disable promotion in
the `org-repo-release-4.X+1.yaml` file.
