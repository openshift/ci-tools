# autoconfigbrancher

`autoconfigbrancher` is a tool that reconciles various parts of CI config
in [openshift/release](https://github.com/openshift/release/) repository.

## What it does

`autoconfigbrancher` runs a sequence of other tools over a working copy of
the [openshift/release](https://github.com/openshift/release/) repository. Each of these tools maintains some subset of
the CI configuration and can change it to some desired state. If the whole sequence results in changes,
`autoconfigbrancher` submits or updates a PR that propagates these changes to the repository. This PR is configured to
be automatically merged (does not need a human approval).

### List of tools

_(subject to bitrot, always consult the code)_

- [ci-operator-yaml-creator](https://github.com/openshift/ci-tools/tree/master/cmd/ci-operator-yaml-creator): ensures
  that repositories with in-repo `.ci-operator.yaml` file use `build_root.from_repository: true` in their ci-operator
  configs
- [registry-replacer](https://github.com/openshift/ci-tools/tree/master/cmd/registry-replacer): ensures that all builds
  specified in ci-operator configs use a local cluster registry (replaces central registry pullspecs with local
  ImageStreamTag references)
- [config-brancher](https://github.com/openshift/ci-tools/tree/master/cmd/config-brancher): propagates ci-operator
  config changes from `master`/`main` configs to future release branches
- [ci-operator-config-mirror](https://github.com/openshift/ci-tools/tree/master/cmd/ci-operator-config-mirror):
  propagates ci-operator config changes to private forks in `openshift-priv` organization
- [determinize-ci-operator](https://github.com/openshift/ci-tools/tree/master/cmd/determinize-ci-operator): loads and
  saves ci-operator config to fix ordering, formatting etc
- [ci-operator-prowgen](https://github.com/openshift/ci-tools/tree/master/cmd/ci-operator-prowgen): generates Prow job
  configuration from ci-operator configuration
- [private-prow-configs-mirror](https://github.com/openshift/ci-tools/tree/master/cmd/private-prow-configs-mirror):
  propagates Prow configuration changes to private forks in `openshift-priv` organization
- [determinize-prow-config](https://github.com/openshift/ci-tools/tree/master/cmd/private-prow-configs-mirror): loads
  and saves Prow configuration to fix ordering, formatting, proper sharding etc
- [sanitize-prow-jobs](https://github.com/openshift/ci-tools/tree/master/cmd/sanitize-prow-jobs): loads and saves Prow
  job configuration to fix ordering, formatting etc. This tool also assigns jobs to build farm clusters.
- [clusterimageset-updater](https://github.com/openshift/ci-tools/tree/master/cmd/clusterimageset-updater): updates
  cluster pool manifests to use the latest stable OCP releases

## Why it exists

Over time, we wrote a number of tools that automatically maintain parts of the CI config
in [openshift/release](https://github.com/openshift/release/) so that we do not need to do so as humans. After some
time, it was annoying to write a PR-creation capability for each tool separately and set up a periodic job for it, so we
started to add new tools as "steps" to the most mature of them (`auto-config-brancher` was originally a tool that simply
ran [config-brancher](https://github.com/openshift/ci-tools/tree/master/cmd/config-brancher), committed the changes and
submitted a PR).

## How it works

It iterates over a (hardcoded) sequence of steps that each calls one of the tools that modify some part of the CI
config. After each step, if there are changes in the config, the changes are committed. If there was at least one new
commit, the new series of commits is pushed into a new or existing PR using
the [`bumper` package from test-infra](https://github.com/kubernetes/test-infra/blob/master/prow/cmd/generic-autobumper/bumper/bumper.go)
.

## How is it deployed

The periodic
job [periodic-prow-auto-config-brancher](https://prow.ci.openshift.org/?job=periodic-prow-auto-config-brancher) ([definition](https://github.com/openshift/release/blob/55cd2ebb8a00445fb06789433dfe98e2199b9a97/ci-operator/jobs/infra-periodics.yaml#L828-L875))
uses `autoconfigbrancher` to
create [PRs in openshift/release](https://github.com/openshift/release/pulls?q=is%3Apr+%22Automate+config+brancher%22+is%3Aclosed+sort%3Acreated-desc)
.
