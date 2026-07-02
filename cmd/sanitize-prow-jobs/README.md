# sanitize-prow-jobs

## What
Deterministically formats Prow job configuration files and assigns clusters to jobs based on dispatcher rules. Unlike the other `determinize-*` tools which only normalize formatting, this tool actively modifies job configurations: it sets the `cluster` field on every job according to the dispatcher's routing logic and normalizes branch regexes on presubmits and postsubmits.

This is the tool that decides **where** each Prow job runs (which build farm cluster).

## How it works -- full flow

### Startup
1. Parse flags: `--prow-jobs-dir` (root of Prow job configs), `--config-path` (dispatcher config), `--cluster-config-path` (cluster metadata)
2. Load the dispatcher config from `--config-path` and validate it. This config defines job routing rules: default cluster, SSH bastion cluster, KVM clusters, build farm mappings, job groups, cloud mappings.
3. Load cluster metadata from `--cluster-config-path`, which provides per-cluster info (provider, capacity, capabilities) and returns a set of **blocked** clusters (clusters temporarily unable to accept jobs).

### Per-file processing
4. If positional arguments are given, they are treated as subdirectories under `--prow-jobs-dir` to process. If none are given, the entire `--prow-jobs-dir` is processed.
5. For each subdirectory, call `sanitizer.DeterminizeJobs()`:
   - Walk all `.yaml` files in the directory tree concurrently (producer-consumer pattern)
   - For each file:
     a. Read and unmarshal the Prow `JobConfig`
     b. Apply `defaultJobConfig()` which processes every job in the file
     c. Marshal and write the normalized YAML back

### Cluster assignment logic (`determineCluster`)
For each job (presubmit, postsubmit, periodic), the cluster is determined by this priority chain:

1. **Non-kubernetes agents**: Jobs with agent != `kubernetes` (or empty) get no cluster assignment
2. **vSphere jobs**: Jobs with "vsphere" in the name go to `vsphere02`, unless they have the `vsphere-elastic-poc` cluster profile (those can be relocated)
3. **SSH bastion jobs**: Jobs requiring an SSH bastion go to the configured `sshBastion` cluster
4. **Explicit cluster label**: Jobs with `ci-operator.openshift.io/cluster` label get that cluster directly
5. **Capability-based routing**: Jobs with `capability/*` labels are matched to clusters that have all required capabilities. If `DetermineE2EByJob` is enabled and a cloud mapping exists, prefer clusters from the matching cloud provider. Distribution across matching clusters is deterministic based on `len(filepath.Base(path)) % len(clusters)`.
6. **KVM jobs**: Jobs with the KVM device label go to configured KVM clusters (deterministic distribution)
7. **E2E cloud matching**: If `DetermineE2EByJob` is true, route to build farm clusters matching the job's cloud provider (detected from `ci-operator.openshift.io/cloud` label or `CLUSTER_TYPE` env var, with cloud mapping applied)
8. **No-builds jobs**: Jobs with the no-builds label go to configured `noBuilds` clusters
9. **Job name match**: Explicit job-name-to-cluster mappings in `config.Groups[cluster].Jobs`
10. **Path regex match**: File path regex patterns in `config.Groups[cluster].Paths`
11. **Build farm filename match**: Exact filename matches in `BuildFarm` config (these jobs can be relocated)
12. **Default**: Falls back to `config.Default`

If a job's determined cluster is in the **blocked** set, the job is relocated to the most-used cluster in the same file (or the default cluster), provided it is marked as relocatable.

If a job already has a valid, non-blocked build farm cluster assigned and no dispatcher data overrides exist, the existing assignment is preserved to avoid churn.

### Branch regex normalization
- **Presubmits**: Each branch pattern gets two regexes -- an exact match (`^branch$`) and a feature branch match (`^branch-`) -- ensuring presubmits also trigger on feature branches. Patterns that already look like regexes are left unchanged.
- **Postsubmits**: Each branch pattern gets only the exact match regex (`^branch$`), since postsubmits should not trigger on feature branches.

### ARM64 image substitution
If a job is assigned to the `arm01` cluster and uses the standard `ci-operator:latest` container image (or the quay proxy variant), the image is replaced with `ci-operator-arm64:latest`.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--prow-jobs-dir` | (required) | Root directory of Prow job config files (`ci-operator/jobs` in openshift/release) |
| `--config-path` | (required) | Path to the dispatcher config (`core-services/sanitize-prow-jobs/_config.yaml` in openshift/release) |
| `--cluster-config-path` | `core-services/sanitize-prow-jobs/_clusters.yaml` | Path to cluster metadata config with provider info, capacity, capabilities, and blocked clusters |
| `-h` | `false` | Show help |

Positional arguments after flags are treated as subdirectories of `--prow-jobs-dir` to process. If none given, all of `--prow-jobs-dir` is processed.

## Key files
- `cmd/sanitize-prow-jobs/main.go` -- entry point, flag parsing, subdirectory iteration
- `pkg/sanitizer/determinize.go` -- `DeterminizeJobs()` walks files concurrently, `defaultJobConfig()` applies cluster assignment and branch normalization per job
- `pkg/dispatcher/config.go` -- `Config` struct and `DetermineClusterForJob()` with the full cluster routing priority chain
- `pkg/dispatcher/helpers.go` -- `LoadClusterConfig()`, `FindMostUsedCluster()`, `DetermineTargetCluster()` for blocked cluster relocation
- `pkg/jobconfig/files.go` -- `ExactlyBranch()` and `FeatureBranch()` for branch regex generation

## Deployment
CLI tool. Run as part of the config generation pipeline in openshift/release (via `make jobs` or `auto-config-brancher`). Ensures all generated Prow jobs have deterministic cluster assignments and formatting.
* Makes sure all jobs are formatted the same way to keep diffs small
* Applies defaults to them
