# blocking-issue-creator

## What it does

The `blocking-issue-creator` tool maintains
Tide [blocker issues](https://github.com/kubernetes/test-infra/blob/master/prow/cmd/tide/config.md#merge-blocker-issues)
that prevent code from being merged into OCP repositories' branches that are not open to development.

## Why it exists

During
the [OCP development lifecycle](https://docs.ci.openshift.org/docs/architecture/branching/#normal-development-for-4x-release)
, some branches are blocked for merges, because their content is automatically fast-forwarded to the content of another
branch. To prevent mistakes, we use Tide's merge blocker issue feature to block all merges to these branches, and use
the `blocking-issue-creator` tool to ensure that all affected repositories have a correct merge blocker issue at all
times.

## How it works

The tool takes current and future OCP versions an input, and then iterates over ci-operator configuration directory to
find all configurations that promote images to OCP of the given versions to discover repositories and branches that
are not open for merges. For all repositories discovered, it ensures that the corresponding Tide merge blocker issue
exists, by either creating it or updating it if it already exists.

## How is it deployed

The periodic
job [periodic-openshift-release-merge-blockers](https://prow.ci.openshift.org/?job=periodic-openshift-release-merge-blockers) ([definition](https://github.com/openshift/release/blob/6e850667c1c9d933f4071734611ae68608deba8c/ci-operator/jobs/infra-periodics.yaml#L1365-L1402))
uses `blocking-issue-creator` to
create [merge blocking issues](https://github.com/issues?q=is%3Aopen+is%3Aissue+archived%3Afalse+label%3Atide%2Fmerge-blocker+org%3Aopenshift)
.
