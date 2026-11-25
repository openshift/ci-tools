# prow-job-runner

`prow-job-runner` previews CI jobs for a GitHub pull request. The branch in each test plan entry selects the test definition; the pull request selects the code to test. Only dry runs are supported.

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

With `--dry-run`, the command prints one ProwJob YAML document per test, in the order requested. Progress messages go to stderr, so you can redirect stdout to save the manifests. No jobs are submitted.
