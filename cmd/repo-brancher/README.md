# repo-brancher

## What it does

The `repo-brancher` automatically fast forwards the git content of future release branches (which
are [closed for merges](https://docs.ci.openshift.org/docs/architecture/branching/), see
also [`blocker-issue-creator`](../blocking-issue-creator)) to the content of the main development branch.

## Why it exists

The future release branches are created in advance so that events like branch cuts on code freeze are
easier: when the branches are eventually needed, everything is already set up in place. Keeping the system
close to the desired state reduces an opportunity for drift and mistakes, so the future branches should
be continuously updated with code merged to the development branch.

For more information about OCP branching scheme, see
the [Centralized Branch Management](https://docs.ci.openshift.org/docs/architecture/branching/) document.

## How it works

The tool iterates over all ci-operator config files in openshift/release, and it selects a set of repositories to
operate on by looking at which are actively promoting images into a specific OpenShift release, provided by
`--current-release`. Branches of repos that actively promote to this release are considered to be the dev branches.

After the development branch is detected, its git content is fast-forwarded to all branches for the provided
`--future-release` values. For efficiency, this is done via shallow fetches and pushes with increasing depth.

## How is it deployed

The tool is executed regularly in
the [`periodic-openshift-release-fast-forward`](https://prow.ci.openshift.org/?job=periodic-openshift-release-fast-forward)
job ([definition](https://github.com/openshift/release/blob/43e46bb9555c870bd4d48d18efbddef2b2085019/ci-operator/jobs/infra-periodics.yaml#L1288-L1324))
.
