# branchingconfigmanagers

## What
A family of config managers that reconcile CI configuration in the openshift/release repository based on the current phase of the OCP product lifecycle. Each sub-manager owns a specific, independent area of CI config (Bugzilla plugin settings, Tide merge criteria, fast-forward job args, release controller configs, RPM mirror repos, test frequencies, and release gating job configs). They are the automation backbone of OpenShift's branching and release lifecycle.

All sub-managers follow a common contract: given the current CI configuration (openshift/release working copy) and product lifecycle data, update the configuration to match the policy expected at the current point in time. They never modify git state -- committing and PR creation is handled separately by `prcreator`.

## Shared concepts

### OCP lifecycle data
All managers consume a lifecycle YAML file describing events per OCP version:

```yaml
ocp:
  "4.17":
  - event: end-of-life
    when: "2025-11-15T16:00:00Z"
  - event: generally-available
    when: "2024-10-15T16:00:00Z"
  - event: code-freeze
    when: "2024-09-20T02:00:00Z"
  - event: feature-freeze
    when: "2024-07-19T02:00:00Z"
  - event: open
    when: "2024-05-13T00:00:00Z"
```

Events are sorted descending by time. An event without a `when` field is assumed to be in the future unless provably in the past (a later event already occurred). The lifecycle config is confidential -- managers must avoid leaking dates.

### `--overwrite-time`
Most managers accept `--overwrite-time` (RFC3339) to simulate running at a different point in time, useful for testing lifecycle transitions without waiting for them.

---

## Sub-managers

### tide-config-manager

#### What
Adjusts Prow Tide merge query labels and branch targeting to enforce merge criteria appropriate for each OCP lifecycle phase. This is how labels like `staff-eng-approved`, `backport-risk-assessed`, `acknowledge-critical-fixes-only`, and `verified` get added to or removed from Tide queries at the right time.

#### How it works
1. Loads the main Prow config and sharded Prow configs.
2. Based on `--lifecycle-phase`, creates the appropriate event handler and calls `shardprowconfig.ShardProwConfig()` which iterates over all Tide queries per org/repo and invokes `ModifyQuery()` on each.
3. Writes the modified sharded configs back to disk.

#### Lifecycle phases and their effects

| Phase | Effect on Tide queries |
|---|---|
| `branching` | On `release-X.Y` / `openshift-X.Y` branches: replace `staff-eng-approved` with `backport-risk-assessed` |
| `pre-general-availability` | On `release-X.Y` / `openshift-X.Y` branches: if `backport-risk-assessed` is present, also add `staff-eng-approved` |
| `general-availability` | Remove `backport-risk-assessed` from current release branch queries. Move `staff-eng-approved` queries from current to future release branch. Update excluded branches to include current and future versions. |
| `acknowledge-critical-fixes-only` | Add `acknowledge-critical-fixes-only` label to `main`/`master` branch queries for repos listed in the guard file |
| `revert-critical-fixes-only` | Remove `acknowledge-critical-fixes-only` label from `main`/`master` branch queries |
| `verified` | Add `verified` label to `main`/`master` and versioned branch queries for repos that promote to `ocp` namespace (auto-discovered from ci-operator configs), plus explicit opt-in repos, minus opt-out repos |

#### Flags
| Flag | Default | What it controls |
|---|---|---|
| `--prow-config-dir` | (required) | Path to Prow configuration directory |
| `--sharded-prow-config-base-dir` | (required) | Base dir for sharded prow config output |
| `--lifecycle-phase` | (required) | One of: `branching`, `pre-general-availability`, `general-availability`, `acknowledge-critical-fixes-only`, `revert-critical-fixes-only`, `verified` |
| `--current-release` | (required for branching/pre-GA/GA) | Current OCP version, e.g. `4.17` |
| `--excluded-repos-config` | (empty) | Path to GA excluded repos config (repos allowed to skip future-branch exclusion checks) |
| `--repos-guarded-by-ack-critical-fixes` | (required for ack-critical-fixes) | Path to newline-separated list of repos |
| `--verified-opt-in` | (required for verified) | YAML file of repos to opt into verified label (`org: [repo1, repo2]`) |
| `--verified-opt-out` | (required for verified) | YAML file of repos to opt out of verified label |
| `--ci-operator-config-dir` | (required for verified) | Path to ci-operator config dir for auto-discovering OCP-promoting repos |

#### Key files
- `cmd/branchingconfigmanagers/tide-config-manager/main.go`
- `pkg/api/shardprowconfig/shardprowconfig.go` -- `ShardProwConfig()` and `ShardProwConfigFunctors` interface

---

### fast-forwarding-config-manager

#### What
Updates the `periodic-openshift-release-fast-forward` periodic Prow job's `--current-release` and `--future-release` arguments based on which OCP version is currently in the "open" development phase. This keeps the fast-forward job (run by `repo-brancher`) pointing at the correct versions automatically.

#### How it works
1. Loads the lifecycle config and reads the infra periodics job config file.
2. Finds the `periodic-openshift-release-fast-forward` job by name.
3. Builds a timeline of `open` and `feature-freeze` events using `lifecycleConfig.GetTimeline()`.
4. Determines where "now" falls in the timeline:
   - If the next event is `open` for version X.Y: append `--future-release=X.Y` to the job args (a new version is about to open).
   - If the current/previous event is `open` or `feature-freeze`: replace both `--current-release` and `--future-release` with the version from that event.
5. Writes the updated job config back to disk.

#### Flags
| Flag | Default | What it controls |
|---|---|---|
| `--lifecycle-config` | (required) | Path to lifecycle YAML |
| `--infra-periodics-path` | (empty) | Path to the infra periodic jobs config file |
| `--overwrite-time` | (now) | Simulate a different current time (RFC3339) |

#### Key files
- `cmd/branchingconfigmanagers/fast-forwarding-config-manager/main.go`
- `pkg/api/ocplifecycle/` -- `GetTimeline()`, `DeterminePlaceInTime()`

---

### release-controller-config-manager

#### What
Bumps release controller configuration files for a new OCP version. Takes existing release controller configs for the current version and creates configs for the next version by replacing version references throughout the file (name, message, mirror prefix, CLI image, check/publish/verification sections).

#### How it works
1. Parses the current release version from `--current-release`.
2. Scans `core-services/release-controller/_releases/` in the release repo for config files matching the current version pattern.
3. Uses the generic `bumper.Bump()` pipeline: finds matching files, reads each one, bumps version references from X.Y to X.(Y+1), and writes the result (with an updated filename) back to disk.
4. Version bumping is done via `ReplaceWithNextVersionInPlace()` which finds all `Major.Minor` patterns and increments the minor version.

#### Flags
| Flag | Default | What it controls |
|---|---|---|
| `--current-release` | (required) | Current OCP version, e.g. `4.17` |
| `--release-repo` | (required) | Absolute path to `openshift/release` working copy |
| `--log-level` | 5 (debug) | Log verbosity level |

#### Key files
- `cmd/branchingconfigmanagers/release-controller-config-manager/main.go`
- `pkg/branchcuts/bumper/release-controller-config-bumper.go`
- `pkg/branchcuts/bumper/bumper.go` -- generic `Bump()` pipeline

---

### rpm-deps-mirroring-services

#### What
Bumps RPM mirror `.repo` files for a new OCP version. These files define RHEL RPM repositories used during OCP builds. When a new version is cut, the mirror configs need corresponding entries for the next version.

#### How it works
1. Parses the current release version.
2. Scans `core-services/release-controller/_repos/` for `.repo` files matching the glob `ocp-X.Y*.repo`.
3. For each file, parses it as an INI file and replaces version strings (e.g. `4.17` to `4.18`) in section names and `baseurl` values.
4. Writes updated files with bumped filenames.

#### Flags
| Flag | Default | What it controls |
|---|---|---|
| `--current-release` | (required) | Current OCP version |
| `--release-repo` | (required) | Absolute path to `openshift/release` working copy |
| `--log-level` | 5 (debug) | Log verbosity level |

#### Key files
- `cmd/branchingconfigmanagers/rpm-deps-mirroring-services/main.go`
- `pkg/branchcuts/bumper/repo/repo-bumper.go`

---

### generated-release-gating-jobs

#### What
Bumps ci-operator config files that define release gating jobs (e.g. `openshift/release` configs for aggregated jobs) from the current OCP version to the next. Updates base images, release references, test definitions, metadata variants, branch references, step environment variables, and the `interval` field.

#### How it works
1. Parses the current release version.
2. Finds ci-operator config files that provide gating signals for the current version. Two file-finding strategies:
   - `signal` (default): discovers files from any repo that provide a signal for the given OCP version via ci-operator config metadata.
   - `regexp`: regex-matches files in `ci-operator/config/openshift/release/` by version pattern.
3. For each file, bumps all version references from X.Y to X.(Y+1) in: base images, releases, tests, metadata variant, metadata branch, and step environment variables.
4. Sets the test `interval` to the specified value (default 168 hours = 1 week).
5. Writes updated configs with bumped filenames.

#### Flags
| Flag | Default | What it controls |
|---|---|---|
| `--current-release` | (required) | Current OCP version |
| `--release-repo` | (required) | Absolute path to `openshift/release` working copy |
| `--interval` | 168 | New interval value (hours) to set on gating jobs |
| `--log-level` | 5 (debug) | Log verbosity level |
| `--file-finder` | `signal` | Method to find gating job files: `regexp` or `signal` |

#### Key files
- `cmd/branchingconfigmanagers/generated-release-gating-jobs/main.go`
- `pkg/branchcuts/bumper/gen-release-jobs-bumper.go`

---

### frequency-reducer

#### What
Reduces the execution frequency of periodic CI tests for older OCP versions. As versions age, their tests run less frequently to save CI resources while maintaining coverage. Only affects `openshift` and `openshift-priv` org repos.

#### How it works
1. Iterates over all ci-operator config files in the config directory.
2. For each test with a `cron` or `interval` field (excluding `mirror-nightly-image` and `promote-` tests), determines the test's OCP version from the branch name.
3. Applies frequency reduction based on age relative to the current version:

| Version age | Target frequency | Cron threshold |
|---|---|---|
| Older than current-2 | Monthly (1x/month) | Reduced only if currently >1x/month |
| current-2 (past-past) | Bi-weekly (2x/month) | Reduced only if currently >2x/month |
| current-1 (past) | Weekly (1x/week, weekends) | Reduced only if currently >4x/month |
| Current or newer | No change | -- |

4. When reducing frequency, `interval`-based schedules are converted to `cron` expressions. Generated cron times are randomized to spread load.
5. Writes updated configs back to disk.

#### Flags
| Flag | Default | What it controls |
|---|---|---|
| `--current-release` | (required) | Current OCP version |
| `--config-dir` | (required, via `ConfirmableOptions`) | Path to ci-operator config directory |
| `--confirm` | false | Actually write changes (dry-run by default) |

#### Key files
- `cmd/branchingconfigmanagers/frequency-reducer/main.go`

---

## Deployment
All sub-managers run as periodic Prow jobs. The typical pattern is:
1. Periodic job runs the config manager against a checkout of openshift/release
2. If it exits 0 (config now matches policy), the `prcreator` tool commits changes and opens a PR

All accept `--overwrite-time` (or equivalent) for testing lifecycle transitions.

## Related
- `cmd/repo-brancher` -- fast-forwards branches, configured by `fast-forwarding-config-manager`
- `cmd/blocking-issue-creator` -- creates merge-blocker issues for frozen branches
- `pkg/api/ocplifecycle/` -- lifecycle config types, timeline computation
- `pkg/branchcuts/bumper/` -- generic bumper framework used by release-controller, rpm-deps, and gating-jobs managers

---

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
  ConfigMap because its content is confidential. Avoid logging dates and saving
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

### Deploying New Config Managers

**NOTE: This section is incomplete and details about deploying Config Manager will be added after we pilot deploying the first one**

Any new config manager should be deployed to run in a periodic Prow job which mounts a ConfigMap `TBD` that contains the
YAML file with [OCP lifecycle data](#product-lifecycle-data). The Prowjob should first run the Config Manager, then if
it ended with zero exit code, it should use the `prcreator` tool to commit the changes and submit a PR to propagate the
changes.

TODO: What period
