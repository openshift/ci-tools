# Updating TestGrid configuration

This utility updates the TestGrid configuration based on our job annotations and [release-controller configuration][release-controller].

Blocking jobs are those that signal widespread failure of the platform. These are traditionally the core end-to-end test runs on our major platforms and upgrades from previous versions. Informing jobs are a broader suite that test the variety of enviroments and configurations our customers expect. Broken jobs are those that have a known, triaged failure that prevents their function for a sustained period of time (more than a week).

The release config and the job annotation combine to determine the dashboard. If a job in the release definition is an upgrade job it goes into
the overall informing dashboard (because upgrades cross dashboards), if it is optional it is considered informing, and is otherwise considered
blocking. If the job has the `ci.openshift.io/release-type` annotation that will override the default on the job (unless the job is blocking
on the release controller and the annotation is informing).

The name of jobs are used to determine which dashboard tab they are grouped with. If they have `-okd-` in their name they are grouped as an
OKD tab, and if they have `-ocp-` or `-origin-` they are considered OCP tabs. The job must have an `-X.Y` identifier to be associated to a
release version.

New jobs should start in `broken` until they have successive runs, then they can graduate to `informing` or `blocking`. A job does not have
to be referenced by the release controller to be informing - the release controller simply ensures it is run once per release build.

```console
$ testgrid-config-generator -testgrid-config path/to/k8s.io/test-infra/config/testgrids/openshift -release-config path/to/openshift/release/core-services/release-controller/_releases -prow-jobs-dir path/to/openshift/release/ci-operator/jobs
```

[release-controller-config]: https://github.com/openshift/release/tree/master/core-services/release-controller
[release-controller]: https://github.com/openshift/release-controller/
