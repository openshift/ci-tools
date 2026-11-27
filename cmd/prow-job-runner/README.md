# prow-job-runner

`prow-job-runner` runs CI jobs for a GitHub pull request. The branch in each test plan entry selects the test definition; the pull request selects the code to test.

```sh
prow-job-runner --dry-run \
  --release-repo /path/to/release \
  --test-plan ./test-plan.yaml \
  --pull-request 'openshift/installer#123'
```

`--test-plan` is required. Pass a local file or a `gs://bucket/path/test-plan.yaml` URL. List at least one test; `variant` is optional:

```yaml
tests:
- organization: openshift
  repository: release
  branch: main
  variant: dynamic-tests-5.1
  test: e2e-aws-ovn-techpreview
- organization: openshift
  repository: installer
  branch: main
  test: unit
```

- Use `--pull-request 'org/repo#number'` for an anonymous GitHub lookup. For a private PR or offline use, pass `--pull-request-data pr.json` instead, with a saved GitHub pull request response. When running inside a Prow presubmit, omit both flags to use its PR.
- Use `--release-repo` to select a local `openshift/release` checkout. It defaults to `/home/prow/go/src/github.com/openshift/release`.
- Use `--pj-namespace ci-staging` to override the namespace for ProwJob resources in both dry-run output and live submission. By default, the runner uses `prowjob_namespace` from the release checkout.

Both native and imported presubmits report to the target PR. For example, a presubmit with context `ci/prow/unit` reports as `ci/prow/dynamic-second-stage/unit`. Tests with the same source context share a second-stage status on GitHub; the runner still tracks each ProwJob separately.

Imported tests use `--with-test-from` to select the test from the source configuration. Existing ci-operator arguments are preserved.

With `--dry-run`, the command prints one ProwJob YAML document per test, in the order requested. Progress and errors are logged as plain text on stderr, so you can redirect stdout to save the manifests. No jobs are submitted.

Omit `--dry-run` to submit jobs to the Prow infrastructure cluster and wait for their results. Inside a cluster, Prow uses the service account for infrastructure access. Outside a cluster, use `--kubeconfig /path/to/config` with the Prow cluster as its current context. With no explicit file or directory, Prow uses the standard kubeconfig loading rules (including `KUBECONFIG`). Credentials need permission to create and get ProwJobs in the selected namespace. The jobs retain their configured build clusters and use the scheduler when enabled.

The command accepts Prow's full Kubernetes flag group: `--kubeconfig`, `--kubeconfig-dir`, `--kubeconfig-suffix` (filters directory entries), `--projected-token-file` (for in-cluster authentication), and `--no-in-cluster-config`. The last flag omits the infrastructure context needed for live submission. Dry runs do not create a Kubernetes client.

If a create response is lost, the runner checks for the job by name. It retries transient status reads and waits for Prow to mark each job complete, including aborted jobs. A rejected submission, a permanent read error, or any completed job that did not succeed causes an unsuccessful exit after the remaining jobs finish. Interrupting the command stops waiting but does not cancel or delete submitted jobs.

## Container image

Build the Linux binary and image from the repository root:

```sh
mkdir -p _output/bin
GOOS=linux GOARCH=amd64 go build -o _output/bin/prow-job-runner ./cmd/prow-job-runner
podman build --platform linux/amd64 -f images/prow-job-runner/Dockerfile \
  -t prow-job-runner:latest _output/bin
```

The image includes the runner as its entrypoint, but not the release checkout or test plan. Mount those inputs when running it:

```sh
podman run --rm \
  -v /path/to/release:/home/prow/go/src/github.com/openshift/release:ro,z \
  -v /path/to/test-plan.yaml:/etc/prow-job-runner/test-plan.yaml:ro,Z \
  prow-job-runner:latest --dry-run \
  --test-plan /etc/prow-job-runner/test-plan.yaml \
  --pull-request 'openshift/installer#123'
```

For live submission outside a cluster, also mount a kubeconfig and pass its container path with `--kubeconfig`.

## Code organization

`cmd/prow-job-runner` owns flag parsing, signal handling, and plain-text stderr logging. `pkg/prow-job-runner` validates options and coordinates the workflow through focused subpackages:

- `testplan` reads local or GCS test plans and validates their ordered test coordinates.
- `pullrequest` resolves GitHub references, saved API responses, or `JOB_SPEC` into pinned PR refs.
- `presubmits` loads release configuration and configures jobs for those refs.
- `execution` creates the infrastructure-cluster client, submits jobs, and tracks their results.

Both modes resolve the entire plan before producing output or submitting work. Dry runs write the manifests without creating a Kubernetes client. For live runs, `execution` tracks each job by namespace and name, then returns the batch's accumulated errors once tracking finishes.
