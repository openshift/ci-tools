# Branching Config Managers

This directory contains individual components of the Automated Branch Cuts project, specifically the so-called "Config
Managers". Config Managers are programs that reconcile CI configuration based on the current state of the OCP lifecycle.
This document describes the contract and conventions for individual Config Managers, so they fit into the wider
Automated Branch Cuts system. In other words, Config Manager authors should refer to this document for guidance about
what are Config Managers expected to do.

## Config Manager Contract

Given the current state of the CI configuration (openshift/release repository working copy) and product lifecycle data,
update the CI configuration to match the state expected by the product policy at given time.

### Product Lifecycle Data

The product lifecycle data will be supplied as a YAML file with the following structure (
see https://github.com/openshift/ci-tools/blob/master/pkg/api/ocplifecycle for more details):

```yaml
ocp:
  "4.1":
  - event: end-of-life
    when: "2020-05-05T16:00:00Z"
  - event: generally-available
    when: "2019-06-04T16:00:00Z"
  - event: code-freeze
    when: "2019-05-01T02:00:00Z"
  - event: feature-freeze
    when: "2019-04-27T02:00:00Z"
  - event: open
    when: "2019-04-27T00:00:00Z"
  "4.2":
  - event: end-of-life
  - event: generally-available
    when: "2019-10-16T16:00:00Z"
  - event: code-freeze
    when: "2019-09-20T02:00:00Z"
  - event: feature-freeze
    when: "2019-07-19T02:00:00Z"
  - event: open
    when: "2019-05-13T00:00:00Z"
  "4.3":
  - event: end-of-life
  - event: generally-available
  - event: code-freeze
    when: "2020-12-13T02:00:00Z"
  - event: feature-freeze
    when: "2019-11-01T02:00:00Z"
  - event: open
    when: "2019-09-14T00:00:00Z"
```

- Events for each version is sorted descending timewise
- The `when` stanza in individual events is optional
- Any event with missing time (`when` stanza) is assumed to be in the future, unless it is **provably** in the past (the
  event is provably in the past if there is an event with a past date/time above it in the sorted sequence).
- There are no assumptions about what are the names of the events, nor what is their expected order

### Config Managers Must

- Manage a well-defined, independent area of CI configuration. Smaller Config Managers are better.
- Do not assume presence/absence of certain lifecycle phases. If the current phase of the product version is unknown to
  the Config Manager, it should iterate the events to the past until it finds a known phase and enforce the state in
  that phase.
- Never modify git state. Config Managers should only change the CI configuration content. Committing the changes and
  submitting Pull Requests for them is handled by a separate `prcreator` tool.
- Never change any CI configuration other than the area the Config Manager "owns". Assume that all changes done,
  including newly created files, will be committed to the repository.
- If the Config Manager ends with zero exit code, all CI configuration must match the policy.
- If the CI configuration does not match the policy after a Config Manager is executed, it must end with non-zero exit
  code.
- Avoid leaking confidential information. The OCP lifecycle data is provided to Config Managers via a
  [ConfigMap](#configmap-with-ocp-lifecycle-data) because its content is confidential. Avoid logging dates and saving
  artifacts that may contain them. Avoid even indirect information leaks, for example by doing actions like "10 days
  before GA, enforce certain state": this leaks GA date 10 days in advance if Config Manager source code is available.

### Config Managers Should

- Avoid complex configuration as much as possible. Ideally, the Config Manager should have no configuration file at all.
  Configuration file with an allow/denylist of repositories whose config is (not) managed is acceptable. Anything more
  complicated should be avoided.
- Be structured in a way that decisions about certain OCP version config are centralized. Sometimes these decisions are
  not independent. For example, if your Config Manager needs to enforce certain state of the config for OCP version that
  precedes the latest GA version, then the Config Manager should be structured as _"when processing a version X.Y,
  determine which version is the latest GA, and if version X.Y is the preceding version, enforce certain state"_. It
  should **not** be structured as _"when processing a version X.Y, determine if it is the GA version and if yes, enforce
  certain state of the config belonging to the preceding version"_.
- Change the CI state to conform the policy to the date and time of the execution, and allow to override this default
  with a command line option.

## Deploying New Config Managers

**NOTE: This section is incomplete and details about deploying Config Manager will be added after we pilot deploying the first one**

Any new config manager should be deployed to run in a periodic Prow job which mounts a ConfigMap `TBD` that contains the
YAML file with [OCP lifecycle data](#product-lifecycle-data). The Prowjob should first run the Config Manager, then if
it ended with zero exit code, it should use the `prcreator` tool to commit the changes and submit a PR to propagate the
changes.

TODO: What period

