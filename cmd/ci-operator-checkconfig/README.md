# ci-operator-checkconfig

## What
Validates ci-operator configuration files for correctness. This is the primary static analysis gate that catches configuration mistakes before they can break CI jobs. It runs all structural, semantic, and cross-config validations in parallel, including checking for duplicate promotion targets across the entire configuration corpus.

Used in presubmit checks on openshift/release to prevent invalid ci-operator configs from merging.

## How it works -- full flow

### Startup
1. Parse flags: `--config-dir` (ci-operator configs), `--registry` (step registry), `--cluster-profiles-config`, `--cluster-claim-owners-config`, `--cluster-profile-set-details`, plus filtering flags `--org` and `--repo`
2. If `--registry` is provided, load the full step registry (references, chains, workflows, observers) and build a `Resolver` from it
3. Load cluster profiles config and cluster claim owners config from their respective paths
4. Create a `ConfigAgent` that loads all ci-operator configs from `--config-dir`, optionally filtered by `--org`/`--repo`
5. Optionally load cluster profile set details (JSON file mapping profiles to available sets)

### Validation (parallel produce-map-reduce)
The validation runs as a concurrent pipeline using `ProduceMapReduce`:

**Produce phase**: Iterates over all loaded configs from the ConfigAgent and sends them to worker goroutines.

**Map phase** (per config, concurrent): Each configuration is validated through multiple layers:

1. **Registry resolution validation** (if `--registry` is set): Resolves all multi-stage test references through the step registry, then validates the fully-resolved configuration via `IsValidResolvedConfiguration()`. This catches references to nonexistent steps, chains, or workflows.

2. **Config agent matching**: Verifies the config can be matched by the ConfigAgent (catches filename/metadata mismatches where the YAML content disagrees with the filesystem path).

3. **Graph configuration validation**: Converts the config to a static graph via `FromConfigStatic()` and validates it with `IsValidGraphConfiguration()`, which checks:
   - No duplicate build targets across the entire pipeline
   - Container test `from` images exist in the pipeline
   - Multi-stage test step `from` images reference known pipeline images
   - Shard counts are valid (>1, not allowed on postsubmits)

4. **Promoted tag collection**: Extracts all promoted image tags and sends them to the reduce phase for cross-config duplicate detection.

5. **Registry override check**: Rejects any config that sets `promotion.registry_override` (this field is not allowed).

**Reduce phase**: Collects all promoted tags across all configs and checks for duplicates -- the same `ImageStreamTag` being promoted from multiple org/repo/branch combinations is an error.

### Specific validations performed
The `Validator` validates (non-exhaustive highlights):

- **`build_root`**: exactly one of `image_stream_tag`, `project_image`, or `from_repository` must be set; `image_stream_tag` requires namespace/name/tag
- **`base_images`/`base_rpm_images`**: tags must be set, names cannot be `root` or reserved bundle prefixes (`src-bundle-*`, `ci-index-*`)
- **`images`**: `to` must be set, no duplicate pipeline image names, `dockerfile_literal` is mutually exclusive with `context_dir`/`dockerfile_path`, valid architectures only (amd64, arm64, ppc64le, s390x), `run_if_changed`/`skip_if_only_changed`/`pipeline_run_if_changed`/`pipeline_skip_if_only_changed` are mutually exclusive
- **`promotion`**: namespace required, cannot promote to `kube*`/`openshift*`/`default`/`redhat*` namespaces (with exceptions), no duplicate targets, official image promoters must import a release stream
- **`releases`**: exactly one of integration/candidate/prerelease/release per entry, valid products/streams/architectures/versions (minor version format X.Y), `latest`/`initial` cannot coexist with `tag_specification`
- **`resources`**: must have a `*` blanket policy, quantities must be positive and parseable, valid resource keys only (cpu, memory, ephemeral-storage, devices.kubevirt.io/kvm, ci-operator.openshift.io/shm, nvidia.com/gpu)
- **`tests`**: names must be valid DNS subdomains with length limits (61 chars general, 42 for claim tests), cron expressions must parse, `run_if_changed` and friends are mutually exclusive
- **`operator`**: bundle `base_index` and `skip_building_index` require `as`, substitution `with` must resolve to a known image, valid `update_graph` values
- **`external_images`**: registry must be `quay.io/*`, cannot collide with `base_images` keys
- **General**: must define at least one test or image, `rpm_build_location` requires `rpm_build_commands`, `canonical_go_repository` must not duplicate the default value, step dependencies must resolve

### Exit
If any validation errors are found, each is logged individually and the process exits with a fatal error. Exit code 0 means all configs are valid.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--config-dir` | (required) | Path to ci-operator configuration directory |
| `--registry` | `""` | Path to step registry directory; enables registry resolution validation |
| `--cluster-profiles-config` | `""` | Path to cluster profiles config file for profile validation |
| `--cluster-claim-owners-config` | `""` | Path to cluster claim owners config file |
| `--cluster-profile-set-details` | `""` | Path to JSON file with cluster profile set details |
| `--org` | `""` | Limit validation to configs in this org |
| `--repo` | `""` | Limit validation to configs in this repo |
| `--log-level` | `info` | Log verbosity level |
| `--only-process-changes` | `false` | Only validate files modified vs. the upstream branch |

## Key files
- `cmd/ci-operator-checkconfig/main.go` -- entry point, flag parsing, produce-map-reduce orchestration, promoted tag deduplication
- `pkg/validation/config.go` -- core configuration validation (build root, images, promotion, resources, releases, operator, base images)
- `pkg/validation/test.go` -- test step validation (names, cron, multi-stage steps, parameters, leases, cluster profiles)
- `pkg/validation/release.go` -- release specification validation (candidate, prerelease, release, integration)
- `pkg/validation/graph.go` -- graph-level validation (duplicate targets, from-image resolution in container and multi-stage tests)
- `pkg/defaults/defaults.go` -- `FromConfigStatic()` converts config to graph representation for graph validation
- `pkg/registry/resolver.go` -- resolves multi-stage test references through chains/workflows
- `pkg/config/options.go` -- shared `Options` struct providing `--config-dir`, `--org`, `--repo`, `--only-process-changes` filtering

## Deployment
Runs as a presubmit check on openshift/release PRs that modify `ci-operator/config/` or the step registry. Also used in local `make validate` targets.

---

## Background

This program can be used to perform validation over a set of `ci-operator`
configuration files.  It is used in [`openshift/release`][openshift_release] to
enforce the correctness of all configuration files present there, via a
[pre-submit job][presubmit_job].

It acts mostly as a front-end for the validation code in
[`pkg/validation`][pkg_validation], which is also used by other components,
guaranteeing the configuration files will be usable by them.  Since it operates
on several thousands of files, the validation code must be efficient and work at
scale.  Files are validated in parallel and work is reused between them as much
as possible.

Validation is performed after loading information from `openshift/release` and
is based on the resolved contents of the configuration files (meaning
multi-stage tests are fully expanded), so the same checks done just prior to the
actual execution of the test can also be done here.  Since all configuration
files are loaded, cross-configuration validation can also be performed.

### Testing locally

To validate a local copy of `openshift/release`, simply execute:

```console
ci-operator-checkconfig \
    --config-dir path/to/release/ci-operator/config \
    --registry path/to/release/ci-operator/step-registry \
    --cluster-profiles-config path/to/release/ci-operator/step-registry/cluster-profiles/cluster-profiles-config.yaml 
    ...
```

[openshift_release]: https://github.com/openshift/release.git
[pkg_validation]: https://github.com/openshift/ci-tools/tree/master/pkg/validation
[presubmit_job]: https://prow.ci.openshift.org/job-history/gs/test-platform-results/pr-logs/directory/pull-ci-openshift-release-master-ci-operator-config
