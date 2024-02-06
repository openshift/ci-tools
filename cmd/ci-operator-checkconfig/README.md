`ci-operator-checkconfig`
=========================

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

Testing locally
---------------

To validate a local copy of `openshift/release`, simply execute:

```console
ci-operator-checkconfig \
    --config-dir path/to/release/ci-operator/config \
    --registry path/to/release/ci-operator/step-registry \
    --cluster-profiles-config path/to/release/core-services/cluster-profiles/_config.yaml 
    …
```

[openshift_release]: https://github.com/openshift/release.git
[pkg_validation]: https://github.com/openshift/ci-tools/tree/master/pkg/validation
[presubmit_job]: https://prow.ci.openshift.org/job-history/gs/test-platform-results/pr-logs/directory/pull-ci-openshift-release-master-ci-operator-config
