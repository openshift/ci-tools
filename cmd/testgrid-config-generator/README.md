# testgrid-config-generator

## What
CLI tool that generates TestGrid dashboard configuration files from Prow periodic job definitions and release controller configuration. It determines which jobs appear on which dashboards (blocking, informing, broken) and for which OpenShift version, producing YAML files consumable by the TestGrid service.

## How it works -- full flow

### 1. Load release controller configuration
Walks the `--release-config` directory for JSON files. For each release controller config, extracts the `verify` map to determine job classifications:
- Non-optional, non-upgrade jobs are `blocking`
- Optional jobs are `informing`
- Upgrade jobs are `informing`
- Jobs with `AggregatedProwJob` settings generate aggregate dashboard entries (prefixed with the verify name)

### 2. Load and validate the allow-list
Reads the `--allow-list` YAML file, which maps job names to override classifications. Valid values: `informing`, `broken`, `generic-informing`, `osde2e`, `olm`. The value `blocking` is forbidden in the allow-list (blocking status must come from the release controller config). Jobs present in both the allow-list and the release controller config as blocking cause a fatal error.

### 3. Load Prow periodic jobs
Reads all Prow job configs from `--prow-jobs-dir` via `jobconfig.ReadFromDir()`.

### 4. Assign jobs to dashboards
For each periodic job, `addDashboardTab()` determines dashboard placement:

**Classification priority:**
1. Allow-list override (if present)
2. Release controller config (blocking/informing)
3. Special informing prefixes (hardcoded list in `pkg/util/testgrid.go`)
4. Layered product interop patterns (`-lp-interop`, `-lp-rosa-hypershift`, `-lp-rosa-classic`, `CSPI-QE-MSI`)
5. If none match, the job is excluded from TestGrid

**Dashboard naming:** `redhat-openshift-{stream}-release-{version}-{role}` where:
- Stream is determined by job name patterns: `ocp`, `okd`, `lp-interop`, `lp-rosa-hypershift`, `lp-rosa-classic`, `CSPI-QE-MSI`
- Version comes from the `job-release` Prow label, or is extracted from the job name via `-X.Y-` regex
- Role is `blocking`, `informing`, or `broken`

**Generic dashboards** (no version): `redhat-openshift-informing`, `redhat-openshift-osd`, `redhat-openshift-olm`

**Retention tuning:** for jobs running at 12h+ intervals, `daysOfResults` is calculated to show ~100 entries, capped at 7-60 days.

### 5. Write output files
- Updates the `groups.yaml` file in `--testgrid-config` to add/remove dashboard names from the `redhat` dashboard group
- Writes one YAML file per dashboard: `{dashboard-name}.yaml` containing `TestGroup` and `Dashboard` definitions
- Removes stale dashboard YAML files that are no longer generated

### Dashboard tab defaults
Each dashboard tab includes:
- Open test template linking to Prow
- File bug template linking to Bugzilla with pre-filled fields (classification: Red Hat, product: OpenShift Container Platform)
- Results URL template linking to Prow job history
- Code search linking to github.com/openshift/origin
- Base filter options excluding Monitor and operator template test noise

### Validation-only mode
With `--validate`, the tool only validates the allow-list entries (no Prow jobs dir or TestGrid output needed) and exits.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--prow-jobs-dir` | (required unless `--validate`) | Path to Prow job config directory (`ci-operator/jobs` in openshift/release) |
| `--release-config` | (required) | Path to release controller configuration directory |
| `--testgrid-config` | (required unless `--validate`) | Path to TestGrid configuration output directory |
| `--allow-list` | (required) | Path to YAML file with job classification overrides |
| `--validate` | false | Only validate the allow-list, skip generation |
| `--google-storage-bucket` | `test-platform-results` | GCS bucket for test artifact links |

## Key files
- `cmd/testgrid-config-generator/main.go` -- all logic: flag parsing, release config loading, allow-list validation, dashboard generation, file output
- `pkg/util/testgrid.go` -- `IsSpecialInformingJobOnTestGrid()` with hardcoded prefix list
- `pkg/release/config/` -- release controller config types
- `pkg/jobconfig/files.go` -- `ReadFromDir()` for loading Prow job configs

## Deployment
CLI tool. Not run directly in production -- instead invoked by `auto-testgrid-generator` which wraps it and creates PRs against kubernetes/test-infra.

## Related
- `cmd/auto-testgrid-generator` -- orchestrates this tool and creates PRs
- TestGrid dashboards: `https://testgrid.k8s.io/redhat-openshift-*`

## Job classification details

Blocking jobs are those that signal widespread failure of the platform. These are traditionally the core end-to-end test runs on our major platforms and upgrades from previous versions. Informing jobs are a broader suite that test the variety of enviroments and configurations our customers expect. Broken jobs are those that have a known, triaged failure that prevents their function for a sustained period of time (more than a week).

The release config and the job annotation combine to determine the dashboard. If a job in the release definition is an upgrade job it goes into
the overall informing dashboard (because upgrades cross dashboards), if it is optional it is considered informing, and is otherwise considered
blocking. If the job has an entry in `/release/core-services/testgrid-config-generator/_allow-list.yaml` that will override the default on the job (unless the job is blocking
on the release controller and the annotation is informing). The allowed values in _allow-list are `informing`, `broken`, `generic-informing`, `osde2e`, and `olm`. 
Note: `blocking` is not a valid entry in the _allow-list since blocking jobs must be in the release controller configuration.

The name of jobs are used to determine which dashboard tab they are grouped with. If they have `-okd-` in their name they are grouped as an
OKD tab, and if they have `-ocp-` or `-origin-` they are considered OCP tabs. The job must have an `-X.Y` identifier to be associated to a
release version.

New jobs should start in `broken` until they have successive runs, then they can graduate to `informing` or `blocking`. A job does not have
to be referenced by the release controller to be informing - the release controller simply ensures it is run once per release build.

PRs are generated automatically for runs of the testgrid-config-generator tool which result in changes in `github.com/kubernetes/test-infra/config/testgrids/openshift`. This is done by the periodic-prow-auto-testgrid-generator job which is run once a day.

## Manual run instructions

Optionally users can run the testgrid-config-generator tool manually to check the results of their changes locally. Instructions for manual runs are given below.

First build testgrid-config-generator:
```console
$ pwd
path/to/github.com/openshift/ci-tools/cmd/testgrid-config-generator
$ ls
main.go  README.md
$ go version
go version go1.25 linux/amd64
$ go build .
go: downloading ...
...
$ ls
main.go  README.md  testgrid-config-generator
```
Ensure you have cloned and updated https://github.com/kubernetes/test-infra locally, along with https://github.com/openshift/release

Assuming you have all your repos rooted at the same toplevel dir, you can run the following command from the `github.com/openshift/ci-tools/cmd/testgrid-config-generator` directory, otherwise you will need to specify the correct paths to the repos/subdirs:
```console
$ ./testgrid-config-generator -testgrid-config ../../../../kubernetes/test-infra/config/testgrids/openshift -release-config ../../../release/core-services/release-controller/_releases -prow-jobs-dir ../../../release/ci-operator/jobs -allow-list=../../../release/core-services/testgrid-config-generator/_allow-list.yaml
````
Verify that changes were made by checking your local `test-infra` repo. For example:
```console
$ cd path/to/github.com/kubernetes/test-infra/config/testgrids/openshift
$ git status
modified:   groups.yaml
new file:   redhat-openshift-...
```

Commit the  changes and file a PR in https://github.com/kubernetes/test-infra/ to land them if you cannot wait for the daily run of periodic-prow-auto-testgrid-generator job.

[generic-informing]: https://testgrid.k8s.io/redhat-openshift-informing
[release-controller-config]: https://github.com/openshift/release/tree/main/core-services/release-controller
[release-controller]: https://github.com/openshift/release-controller/
