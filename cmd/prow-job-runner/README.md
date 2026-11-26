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

Both native and imported presubmits report to the target PR. For example, a presubmit with context `ci/prow/unit` reports as `ci/prow/dynamic-second-stage/unit`. Tests with the same source context share a second-stage status on GitHub; the runner still tracks each ProwJob separately.

Imported tests use `--with-test-from` to select the test from the source configuration. Existing ci-operator arguments are preserved.

With `--dry-run`, the command prints one ProwJob YAML document per test, in the order requested. Progress messages go to stderr, so you can redirect stdout to save the manifests. No jobs are submitted.

Omit `--dry-run` to submit jobs to the Prow infrastructure cluster and wait for their results. Inside a cluster, Prow uses the service account for infrastructure access. Outside a cluster, use `--kubeconfig /path/to/config` with the Prow cluster as its current context. With no explicit file or directory, Prow uses the standard kubeconfig loading rules (including `KUBECONFIG`). Credentials need permission to create and get ProwJobs in the release checkout's `prowjob_namespace`. The jobs retain their configured build clusters and use the scheduler when enabled.

The command accepts Prow's full Kubernetes flag group: `--kubeconfig`, `--kubeconfig-dir`, `--kubeconfig-suffix` (filters directory entries), `--projected-token-file` (for in-cluster authentication), and `--no-in-cluster-config`. The last flag omits the infrastructure context needed for live submission. Dry runs do not create a Kubernetes client.

If a create response is lost, the runner checks for the job by name. It retries transient status reads and waits for Prow to mark each job complete, including aborted jobs. A rejected submission, a permanent read error, or any completed job that did not succeed causes an unsuccessful exit after the remaining jobs finish. Interrupting the command stops waiting but does not cancel or delete submitted jobs.
