# Updating TestGrid configuration

This utility updates the TestGrid configuration to include promotion gates and informers [defined][release-controller-config] for [the OpenShift release-controller][release-controller].

```console
$ testgrid-config-generator -testgrid-config path/to/k8s.io/test-infra/config/testgrids/openshift -release-config path/to/openshift/release/core-services/release-controller/_releases -prow-jobs-dir path/to/openshift/release/ci-operator/jobs
```

This will walk `-release-config` looking for release files that end with `.json`.
For each of those release files, it will:

* Select a stream based on the `name`.
    `name` that match `*.ci`, `*.nightly`, or `stable-4.*` are added to the `ocp` stream.
    `name` that match `*.okd` are added to the `okd` stream.
* Select a X.Y version based on the `name` (e.g. a `name` of `4.3.0` would select the 4.3 version).

For each version and stream, blocking and informing dashboards will be created.
The blocking dashboard will contain jobs from the release file's `verify` map which have neither `upgrade` nor `optional` true.
Each informing dashboard will contain jobs from the release file's `verify` map which have `optional` true but not `upgrade`.

The informing dashboard will also contain jobs from `-prow-jobs-dir` with:

* the `ci.openshift.io/release-type` set to `informing`,
* the `job-release` label that matches the X.Y version extracted from the release file `name`,
* a job name that matches `*-openshift-okd-*` (for the `okd` stream) or one of `*-openshift-ci-*`, `*-openshift-ocp-*`, and `*-openshift-origin-*` (for the `ocp` stream).

Jobs from the release files with `upgrade` are collected and posted to [the generic informing dashboard][generic-informing].

After running the tool, commit the `test-infra` changes and file a PR to land them.

[generic-informing]: https://testgrid.k8s.io/redhat-openshift-informing
[release-controller-config]: https://github.com/openshift/release/tree/master/core-services/release-controller
[release-controller]: https://github.com/openshift/release-controller/
