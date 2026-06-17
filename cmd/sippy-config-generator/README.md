# sippy-config-generator

## What
CLI tool that generates Sippy monitoring configuration by combining release controller job definitions with Prow periodic job metadata. Sippy uses this config to know which jobs belong to which OpenShift release and whether they are blocking or informing. Output is YAML written to stdout.

## How it works -- full flow

### 1. Load customization file (optional)
If `--customization-file` is provided, reads a YAML file containing a pre-populated `SippyConfig` struct. This allows manually specified releases, regexp patterns, or job overrides to be preserved and merged with the generated output.

### 2. Parse release controller configuration
Walks the `--release-config` directory for JSON files. For each release controller config, extracts the `verify` map:
- Optional jobs go into the `informingJobs` set
- Non-optional jobs go into the `blockingJobs` set
- Jobs with `AggregatedProwJob` settings generate aggregate job names in the format `{verifyName}-{aggregateProwJobName}` (defaulting to `release-openshift-release-analysis-aggregator`)

### 3. Load and sort Prow periodic jobs
Reads all Prow job configs from `--prow-jobs-dir` and sorts them alphabetically by name for deterministic output.

### 4. Build release config
For each periodic job that has a `job-release` label:
- Determines the release version from the label value
- If the job name contains `-okd`, appends `-okd` to the release name (e.g. `4.14-okd`)
- Adds the job to `releases[version].jobs` map (value `true`)
- If the job has aggregate jobs, adds those too
- If the job is in the `blockingJobs` set, appends it to `releases[version].blockingJobs`
- If the job is in the `informingJobs` set or matches `IsSpecialInformingJobOnTestGrid()`, appends it to `releases[version].informingJobs`

### 5. Output
Prints a YAML header comment with the generation timestamp, then marshals and prints the complete `SippyConfig` struct to stdout.

### SippyConfig structure
```yaml
prow:
  url: <prow URL>
releases:
  "4.16":
    jobs:
      periodic-ci-openshift-release-master-nightly-4.16-e2e-aws: true
      ...
    regexp: []
    blockingJobs:
      - periodic-ci-openshift-release-master-nightly-4.16-e2e-aws
    informingJobs:
      - periodic-ci-openshift-release-master-nightly-4.16-e2e-aws-upgrade
```

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--prow-jobs-dir` | (required) | Path to Prow job config directory (`ci-operator/jobs` in openshift/release) |
| `--release-config` | (required) | Path to release controller configuration directory |
| `--customization-file` | (none) | Path to YAML file with additional config to merge (e.g. manually maintained regexp patterns) |

## Key files
- `cmd/sippy-config-generator/main.go` -- all logic: flag parsing, release config loading, Prow job iteration, YAML output
- `pkg/api/sippy/v1/types.go` -- `SippyConfig`, `ReleaseConfig`, `ProwConfig` type definitions
- `pkg/util/testgrid.go` -- `IsSpecialInformingJobOnTestGrid()` shared with testgrid-config-generator
- `pkg/jobconfig/files.go` -- `ReadFromDir()` for loading Prow job configs
- `pkg/release/config/` -- release controller config types

## Deployment
CLI tool. Run as part of a periodic Prow job or manually. Output is piped/redirected to a config file consumed by the Sippy service.

## Related
- Sippy service: monitors CI job health and payload readiness
- `cmd/testgrid-config-generator` -- similar input processing but different output format
- The `IsSpecialInformingJobOnTestGrid()` function is shared between sippy-config-generator and testgrid-config-generator

## Example invocation

```
./sippy-config-generator --prow-jobs-dir ~/git/release/ci-operator/jobs --release-config ~/git/release/core-services/release-controller/_releases --customization-file ~/go/src/github.com/openshift/sippy/config/openshift-customizations.yaml
```

Commit the output to the sippy's repo config/openshift.yaml file.

## Customization

The customization file contains overrides or additional releases not
present (i.e., to create a pseudo-release of selected jobs).


Example:

```yaml
  prow:
    url: https://prow.ci.openshift.org/prowjobs.js
  releases:
    "Presubmits":
      regexp:
        - "^pull-ci-openshift-.*-(master|main)-e2e-.*"
    "4.11":
      jobs:
        aggregated-aws-ovn-upgrade-4.11-micro-release-openshift-release-analysis-aggregator: false
        periodic-ci-openshift-release-master-nightly-4.11-e2e-ovirt-csi: false
```
