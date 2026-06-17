# cvp-trigger

## What
CLI tool that triggers Container Verification Pipeline (CVP) operator verification jobs in Prow. Given a bundle image, index image, OCP version, and other operator metadata, it finds the corresponding periodic ProwJob definition, injects the operator-specific parameters, submits the job to the cluster, and watches it until completion. Outputs a JSON result file with the job status and artifacts URL.

CVP is the system that validates ISV (Independent Software Vendor) operators for Red Hat certification.

## How it works -- full flow

1. **Parse and validate options**: All required parameters are validated -- bundle image ref, channel, index image ref, operator package name, OCP version (must be `X.Y` where X >= 4), job name, and Prow config paths. The output path directory must exist (unless `--dry-run`).

2. **Load Prow config**: Use the Prow config agent to load job definitions from the specified config and job-config paths.

3. **Find the periodic job**: Search all periodic jobs in the loaded Prow config for one matching `--job-name`. Fatal if not found.

4. **Construct a ProwJob**: Convert the periodic job spec into a ProwJob resource using `pjutil.NewProwJob()`.

5. **Inject parameters**:
   - **Multi-stage params** (`--multi-stage-param=KEY=VALUE`): `OO_CHANNEL`, `OO_PACKAGE`, and optionally `OO_INSTALL_NAMESPACE`, `OO_TARGET_NAMESPACES`, `CUSTOM_SCORECARD_TESTCASE`
   - **Dependency overrides** (`--dependency-override-param=KEY=VALUE`): `BUNDLE_IMAGE`, `OO_INDEX`, `INDEX_IMAGE`
   - **Environment variables**: `CLUSTER_TYPE=aws`, `OCP_VERSION`, and optionally `RELEASE_IMAGE_LATEST`
   - **Input hash**: Computed from sorted env vars, appended as `--input-hash=...` for deduplication

6. **Dry-run mode**: If `--dry-run`, marshal the ProwJob to YAML, print it, and exit.

7. **Submit and watch**:
   - Create a ProwJob client from in-cluster config
   - Submit the ProwJob via `Create()`
   - Watch the ProwJob using a field selector on its name, with exponential backoff for watch creation (10 steps, factor 2, starting at 1s)
   - On terminal state (`Success`, `Failure`, `Aborted`, `Error`):
     - Compute the GCS artifacts URL using `gcsupload.PathsForJob()` and `Spyglass.GCSBrowserPrefix`
     - Write a JSON result to `--output-path`:
       ```json
       {
         "status": "<state>",
         "prowjob_artifacts_url": "<url>",
         "prowjob_url": "<url>"
       }
       ```
     - Exit 0 on success, fatal on failure

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--bundle-image-ref` | (required) | URL for the operator bundle image |
| `--channel` | (required) | Operator channel to test |
| `--index-image-ref` | (required) | URL for the operator index image |
| `--operator-package-name` | (required) | Operator package name |
| `--ocp-version` | (required) | OCP version in X.Y format (X >= 4) |
| `--job-name` | (required) | Name of the periodic ProwJob to trigger |
| `--prow-config-path` | (required) | Path to Prow config YAML |
| `--job-config-path` | (required) | Path to Prow job config directory |
| `--output-path` | (required unless dry-run) | File path to write the JSON result |
| `--release-image-ref` | `""` | Pull spec of a specific release payload for OCP deployment |
| `--install-namespace` | `""` | Namespace for operator/catalog installation |
| `--target-namespaces` | `""` | Comma-separated list of target namespaces for the operator |
| `--custom-scorecard-testcase` | `""` | Custom scorecard test case name |
| `--enable-hybrid-overlay` | `false` | Enable hybrid overlay feature on the test cluster |
| `--dry-run` | `false` | Print ProwJob YAML without submitting |

## Key files

- `cmd/cvp-trigger/main.go` -- all logic: option parsing, ProwJob construction, parameter injection, submission, watch loop, result output

## Deployment
CLI tool. Not deployed as a service. Invoked by external systems (e.g. CVP pipeline) that need to trigger operator verification jobs in Prow. Requires in-cluster access to the Prow API server for ProwJob creation.
## High-level CVP ↔ Prow Job Architecture

To test the optional operator images built internally in Red Hat, CVP triggers
testing jobs via the `cvp-trigger` tool. Currently, the only implemented test
workflow is the one executing the common operator tests (component-agnostic).
For common operator tests, `cvp-trigger` creates a parameterized instance of the
`cpaas-cvp-optional-operator-common-tests` job, living in [openshift/release](https://github.com/openshift/release/blob/main/ci-operator/jobs/openshift/release/openshift-release-infra-periodics.yaml).
See the [Triggered Job Interface](#triggered-job-interface) section for details
 about the parametrization.

The `cpaas-cvp-optional-operator-common-tests` job runs ci-operator using
a config stored in [redhat-openshift-ecosystem](https://github.com/redhat-openshift-ecosystem/release/tree/master/ci-operator/config/redhat-openshift-ecosystem/playground).
The exact configuration to use is inferred from the desired OCP version. For
example, for OCP version 4.5, ci-operator uses the config for the
artificial `cvp-ocp-4.5` branch of the `redhat-openshift-ecosystem/playground`
repository. The ci-operator targets a test with a name inferred from the desired
cloud platform. For example, when `aws` is requested, ci-operator targets the
`cvp-common-aws` test from the config. This example shows how the job calls
ci-operator:

```console
$  ci-operator --org=redhat-openshift-ecosystem \
               --repo=playground \
               --branch=cvp-ocp-$(OCP_VERSION) \
               --target=cvp-common-$(CLOUD_PLATFORM) \
               < ...more irrelevant args... >
```

The `cvp-common-$CLOUD_PLATFORM` tests are using the [`optional-operators-cvp-workflow-$CLOUD_PLATFORM`](https://steps.svc.ci.openshift.org/registry/optional-operators-cvp-common-aws)
workflows. These workflows install an OCP cluster of a given version and
install the requested optional operator in it. In its `test` section, the
workflow executes the shared CVP tests against the installed cluster.

## Triggered Job Interface

The tool should work by loading Prow and Job configuration, finding specific job
configuration structs and modifying those structs so that their specified
containers are executed with the specified environmental variables having the
desired values.

### Common CVP Tests for Optional Operators

CVP Trigger must load the configuration for the
`cpaas-cvp-optional-operator-common-tests` Prowjob and modify its
`.Spec.Containers[0].Env` list to have the following environmental variables set.

#### Parameters specifying OCP cluster

- `OCP_VERSION`: Required. A string specifying the desired minor OCP version
  which will be used when provisioning the ephemeral testing cluster. If
  `RELEASE_IMAGE_LATEST` is not specified, an ephemeral release payload will be
  created, corresponding to the collection of OCP images built from HEADs of
  appropriate release branches of OCP components (that is, latest development
  version of that minor release).
- `CLUSTER_TYPE`: Required. One of `aws` strings, specifying the cloud platform
  where the ephemeral cluster will be created.
- `RELEASE_IMAGE_LATEST`: Optional. A full pullspec of a release payload image
  which will be used when provisioning the ephemeral testing cluster.
- `ENABLE_HYBRID_OVERLAY`: Optional. Enables the hybrid overlay feature on a running cluster.

#### Parameters specifying optional operator to be installed on OCP cluster

- `OO_INDEX`: Required. A pullspec of the the index image.
- `OO_PACKAGE`: Required. The name of the operator package to be installed. Must
   be present in the index image referenced by `$OO_INDEX`.
- `OO_CHANNEL`: Required. The name of the operator channel to track.
- `OO_INSTALL_NAMESPACE`: Optional. The namespace into which the operator and
   catalog will be installed. If empty, a new namespace will be created.
- `OO_TARGET_NAMESPACES`: Optional. A comma-separated list of namespaces the
   operator will target. If empty, all namespaces will be targeted.
   If no OperatorGroup exists in `$OO_INSTALL_NAMESPACE`, a new one will be
   created with its target namespaces set to `$OO_TARGET_NAMESPACES`, otherwise
   the existing OperatorGroup's target namespace set will be replaced. The special
   value "!install" will set the target namespace to the operator's installation
   namespace.
- `CUSTOM_SCORECARD_TESTCASE`: Optional. Name of the custom scorecard test which is to be run.
- `PYXIS_URL`: Optional. URL that contains specific cvp product package name for specific ISV
   with unique pid.
