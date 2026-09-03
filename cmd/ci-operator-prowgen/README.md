# ci-operator-prowgen

## What
Generates Prow job YAML definitions (presubmits, postsubmits, periodics) from ci-operator configs and `.config.prowgen` files. This is the `make jobs` engine in the openshift/release repo — every Prow job definition is produced by this tool.

## How it works — full flow

### Config loading
- Reads ci-operator configs from `--from-dir` or `--from-release-repo` (resolves to `$GOPATH/.../release/ci-operator/config`)
- For each config, loads `.config.prowgen` from two levels:
  - **Org-level**: `{configDir}/{org}/.config.prowgen`
  - **Repo-level**: `{configDir}/{org}/{repo}/.config.prowgen`
  - Repo merges onto org via `MergeDefaults()`: booleans OR'd, lists concatenated
- Output written to `--to-dir` or `--to-release-repo` (resolves to `$GOPATH/.../release/ci-operator/jobs`)
- Uses `--known-infra-file` to preserve hand-maintained infra job files

### Job generation (`GenerateJobs()` in pkg/prowgen/prowgen.go)
For each test in config:

1. **Periodic detection**: `IsPeriodic()` returns true if ANY of Interval, MinimumInterval, Cron, or ReleaseController is set
   - Periodic tests get `GeneratePeriodicForTest()`
   - If test also has `Presubmit: true`, a presubmit is generated too
2. **Postsubmit**: If `Postsubmit: true`, generates postsubmit with `MaxConcurrency=1`
3. **Presubmit**: Default for everything else

### Image test generation
- Checks `ImageTargets(configSpec)` for `[images]` target
- Creates presubmit with targets `[images]` plus any additional presubmit targets
- Propagates `Images.RunIfChanged`, `Images.SkipIfOnlyChanged`, `Images.PipelineRunIfChanged`, `Images.PipelineSkipIfOnlyChanged`
- If `PromotionConfiguration` exists: creates postsubmit for actual image targets, periodic if `PromotionConfiguration.Cron` set

### Operator bundle handling
- If `configSpec.Operator` defined: creates presubmit for each bundle build/index

### Presubmit details
- `AlwaysRun`: true if no run_if_changed, no skip_if_only_changed, not defaultDisable, no pipeline conditions
- Trigger: default regex `(?m)^/test( | .* )(shortName|remaining-required),?($|\s.*)` or explicit `/test variant-name` for disabled defaults
- Branches: exact match + feature branch patterns
- Context: `ci/prow/{shortName}`

### Periodic details
- `@daily` cron: deterministic hash from job name (minutes 0-59, hours 22-4 UTC)
- `ReleaseController=true`: overrides to `@yearly`, adds `release-controller=true` label
- Adds `ExtraRefs` with repo/branch info

### Slack reporter config matching (`GetSlackReporterConfigForJobName()` in pkg/config/load.go)
Matching order for each config entry:
1. Skip if variant in `excluded_variants`
2. Skip if full job name matches any `excluded_job_patterns` regex
3. Match if test name in `job_names` (exact match against testName, not full job name)
4. Match if test name matches any `job_name_patterns` regex
5. First match wins

### Variant handling
- If variant is set: label `ci-operator.openshift.io/variant = variant` added to job base
- `SkipPresubmits()` checks branch+variant against `SkipOperatorPresubmits` list

### .config.prowgen structure
```yaml
private: false           # Hide jobs in Deck
expose: false            # Override private to show in Deck
rehearsals:
  disable_all: false
  disabled_rehearsals: [] # Test names to skip rehearsal
slack_reporter_configs:
  - channel: "#channel"
    job_states_to_report: [failure, error]
    job_names: [test-name]
    job_name_patterns: ["regex.*"]
    excluded_variants: [variant1]
    excluded_job_patterns: ["^periodic-ci-org-repo-branch-"]
skip_operator_presubmits:
  - branch: release-4.14
    variant: some-variant
enable_secrets_store_csi_driver: false
```

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--from-dir` | — | Source ci-operator config directory |
| `--from-release-repo` | false | Use release repo config path |
| `--to-dir` | — | Target Prow job config directory |
| `--to-release-repo` | false | Use release repo jobs path |
| `--registry` | — | Step registry path for workflow/chain resolution |
| `--known-infra-file` | — | Infra filenames to skip (repeatable) |

## Key files
- `cmd/ci-operator-prowgen/main.go` — entry point, config loading, org->repo merge
- `pkg/prowgen/prowgen.go` — `GenerateJobs()`, presubmit/postsubmit/periodic generation
- `pkg/prowgen/jobbase.go` — `NewProwJobBaseBuilder()`, variant labels, Private/Expose
- `pkg/config/load.go` — `.config.prowgen` loading, `GetSlackReporterConfigForJobName()`

## Deployment
CLI tool. Run via `make jobs` in openshift/release. Also called by `auto-config-brancher` during automated branch cutting.

---

## Background

Prowgen is a tool that generates [job configurations](https://docs.prow.k8s.io/docs/jobs/) based on
[ci-operator configuration](https://docs.ci.openshift.org/architecture/ci-operator/) and its own
configuration file named `.config.prowgen`.

The contents of `.config.prowgen` will be appended to every job configuration during Prowgen execution:

**Example:**

```yaml
slack_reporter:
- channel: "#ops-testplatform"
  job_states_to_report:
  - failure
  - error
  report_template: ':failed: Job *{{.Spec.Job}}* ended with *{{.Status.State}}*. <{{.Status.URL}}|View logs> {{end}}'
  job_names:
  - images
  # job_name_patterns supports regex patterns for matching job names
  job_name_patterns:
  - "^unit-.*"
  - "^e2e-.*-serial$"
  # excluded_job_patterns excludes jobs matching these patterns (similar to excluded_variants)
  excluded_job_patterns:
  - ".*-skip$"
  - "^nightly-.*"
skip_operator_presubmits:
- branch: release-4.19
  variant: periodics
```

Most of the time, Prowgen will overwrite configurations on `openshift/ci-operator/jobs/` with the ones
defined in `openshift/ci-operator/jobs/`.

Prowgen is typically run using `make update` or `make jobs` from within `openshift/release` directory.

### Testing

`Prowgen` is hardcoded to use `GOPATH` + `src/github.com/openshift/release`, if you want to
test it on your machine you can run the tool directly from the `openshift/release` repository
root path or use a symbolic link pointing to your `openshift/release` clone:

```bash
# generally GOPATH=~/go
ln -s ~/cloned-repos/openshift/release ~/go/src/github.com/openshift/release
```

Then you can execute `ci-operator-prowgen`:

```bash
ci-operator-prowgen \
    --from-release-repo \
    --to-release-repo \
    --known-infra-file infra-build-farm-periodics.yaml \
    --known-infra-file infra-periodics.yaml \
    --known-infra-file infra-image-mirroring.yaml \
    --known-infra-file infra-periodics-origin-release-images.yaml
```
