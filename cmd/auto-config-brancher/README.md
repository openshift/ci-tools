# auto-config-brancher

## What
Orchestrator that runs a sequence of CI configuration tools in order, commits each tool's changes separately, pushes the result, and creates a PR to openshift/release. This is the periodic automation that keeps release branch configs, job definitions, and private org mirrors up to date.

## How it works — tool sequence

Executes these tools in order (each one commits its own changes if any):

| Step | Tool | What it does |
|---|---|---|
| 1 | `config-brancher` | Branch ci-operator configs for future releases (`--skip-periodics`) |
| 2 | `ci-operator-config-mirror` | Mirror configs to openshift-priv (`--to-org openshift-priv --only-org openshift`) |
| 3 | `determinize-ci-operator` | Normalize ci-operator config YAML formatting |
| 4 | `ci-operator-prowgen` | Generate Prow jobs from ci-operator configs |
| 5 | `private-prow-configs-mirror` | Mirror Prow configs to private org |
| 6 | `determinize-prow-config` | Normalize Prow config YAML |
| 7 | `sanitize-prow-jobs` | Validate and format Prow job configs |
| 8 | `clusterimageset-updater` | Update Hive ClusterImageSet resources |
| 9 | `promoted-image-governor` | Validate image promotion configs (dry-run) |

### Change detection
1. Records git HEAD SHA before running tools
2. Each tool runs, and if `git status --porcelain` shows changes, stages all and commits with message `"{tool} {args}"`
3. After all tools: compares overall `git diff` from start SHA to current HEAD
4. If no overall diff (changes cancelled out): skips push
5. If changes: pushes to remote branch `auto-config-brancher`

### Authentication
- **Token auth** (`--github-token-path`): pushes via `https://{login}:{token}@github.com/...`
- **GitHub App auth** (no token): uses `GitClientFactory` for authenticated push via app installation

### PR creation
- Title: `"Automate config brancher by auto-config-brancher job at {timestamp}"`
- Labels: `tide/merge-method-merge`, `rehearsals-ack`, `priority/ci-critical`
- If `--self-approve`: adds `approved` and `lgtm` labels
- Body: `/cc @{assign}`
- Updates existing PR if one already exists (matched by title prefix)

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--target-dir` | — | Root directory of target repository |
| `--github-login` | `openshift-bot` | GitHub username for PR/push |
| `--assign` | `openshift/test-platform` | PR assignee |
| `--self-approve` | false | Auto-add approved+lgtm labels |
| `--current-release` | — | Current OCP release version |
| `--future-release` | — | Target release versions (repeatable) |
| `--git-name` | `""` | Git committer name |
| `--git-email` | `""` | Git committer email |
| `--whitelist-file` | `""` | Path to whitelist file |

## Deployment
Periodic Prow job ([periodic-prow-auto-config-brancher](https://prow.ci.openshift.org/?job=periodic-prow-auto-config-brancher)) that creates [PRs in openshift/release](https://github.com/openshift/release/pulls?q=is%3Apr+%22Automate+config+brancher%22+is%3Aclosed+sort%3Acreated-desc). The PR is configured to be automatically merged without human approval.

---

## Background

### What it does

`auto-config-brancher` runs a sequence of other tools over a working copy of
the [openshift/release](https://github.com/openshift/release/) repository. Each of these tools maintains some subset of
the CI configuration and can change it to some desired state. If the whole sequence results in changes,
`auto-config-brancher` submits or updates a PR that propagates these changes to the repository. This PR is configured to
be automatically merged (does not need a human approval).

### List of tools

_(subject to bitrot, always consult the code)_

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
- [determinize-prow-config](https://github.com/openshift/ci-tools/tree/master/cmd/determinize-prow-config): loads
  and saves Prow configuration to fix ordering, formatting, proper sharding etc
- [sanitize-prow-jobs](https://github.com/openshift/ci-tools/tree/master/cmd/sanitize-prow-jobs): loads and saves Prow
  job configuration to fix ordering, formatting etc. This tool also assigns jobs to build farm clusters.
- [clusterimageset-updater](https://github.com/openshift/ci-tools/tree/master/cmd/clusterimageset-updater): updates
  cluster pool manifests to use the latest stable OCP releases
- [promoted-image-governor](https://github.com/openshift/ci-tools/tree/master/cmd/promoted-image-governor): validates
  image promotion configs (dry-run)

### Why it exists

Over time, we wrote a number of tools that automatically maintain parts of the CI config
in [openshift/release](https://github.com/openshift/release/) so that we do not need to do so as humans. After some
time, it was annoying to write a PR-creation capability for each tool separately and set up a periodic job for it, so we
started to add new tools as "steps" to the most mature of them (`auto-config-brancher` was originally a tool that simply
ran [config-brancher](https://github.com/openshift/ci-tools/tree/master/cmd/config-brancher), committed the changes and
submitted a PR).

### How it works (detailed)

It iterates over a (hardcoded) sequence of steps that each calls one of the tools that modify some part of the CI
config. After each step, if there are changes in the config, the changes are committed. If there was at least one new
commit, the new series of commits is pushed into a new or existing PR using
the [`bumper` package from test-infra](https://github.com/kubernetes/test-infra/blob/master/prow/cmd/generic-autobumper/bumper/bumper.go)
.

### How is it deployed

The periodic
job [periodic-prow-auto-config-brancher](https://prow.ci.openshift.org/?job=periodic-prow-auto-config-brancher) ([definition](https://github.com/openshift/release/blob/55cd2ebb8a00445fb06789433dfe98e2199b9a97/ci-operator/jobs/infra-periodics.yaml#L828-L875))
uses `auto-config-brancher` to
create [PRs in openshift/release](https://github.com/openshift/release/pulls?q=is%3Apr+%22Automate+config+brancher%22+is%3Aclosed+sort%3Acreated-desc)
.
