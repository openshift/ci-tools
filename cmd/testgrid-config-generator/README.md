# Updating TestGrid configuration

This utility updates the TestGrid configuration to include promotion gates and informers [defined][release-controller-config] for [the OpenShift release-controller][release-controller].

```console
$ testgrid-config-generator -testgrid-config path/to/k8s.io/test-infra/config/testgrids/openshift -release-config path/to/openshift/release/core-services/release-controller/_releases -prow-jobs-dir path/to/openshift/release/ci-operator/jobs
```

[release-controller-config]: https://github.com/openshift/release/tree/master/core-services/release-controller
[release-controller]: https://github.com/openshift/release-controller/
