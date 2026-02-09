# Updating TestGrid configuration

This utility updates the TestGrid configuration based on our job annotations and [release-controller configuration][release-controller].

Blocking jobs are those that signal widespread failure of the platform. These are traditionally the core end-to-end test runs on our major platforms and upgrades from previous versions. Informing jobs are a broader suite that test the variety of enviroments and configurations our customers expect. Broken jobs are those that have a known, triaged failure that prevents their function for a sustained period of time (more than a week).

The release config and the job annotation combine to determine the dashboard. If a job in the release definition is an upgrade job it goes into
the overall informing dashboard (because upgrades cross dashboards), if it is optional it is considered informing, and is otherwise considered
blocking. If the job has an entry in `/release/core-services/testgrid-config-generator/_allow-list.yaml` that will override the default on the job (unless the job is blocking
on the release controller and the annotation is informing). The allowed values in _allow-list are `informing`, `broken` and `generic-informing`. 
Note: `blocking` is not a valid entry in the _allow-list since blocking jobs must be in the release controller configuration.

The name of jobs are used to determine which dashboard tab they are grouped with. If they have `-okd-` in their name they are grouped as an
OKD tab, and if they have `-ocp-` or `-origin-` they are considered OCP tabs. The job must have an `-X.Y` identifier to be associated to a
release version.

New jobs should start in `broken` until they have successive runs, then they can graduate to `informing` or `blocking`. A job does not have
to be referenced by the release controller to be informing - the release controller simply ensures it is run once per release build.

PRs are generated automatically for runs of the testgrid-config-generator tool which result in changes in `github.com/kubernetes/test-infra/config/testgrids/openshift`. This is done by the periodic-prow-auto-testgrid-generator job which is run once a day.

Optionally users can run the testgrid-config-generator tool manually to check the results of their changes locally.  Instructions for manual runs are given below.

Note: Go 1.13 is required.

First build testgrid-config-generator:
```console
$ pwd
path/to/github.com/openshift/ci-tools/cmd/testgrid-config-generator
$ ls
main.go  README.md
$ go version
go version go1.13 linux/amd64
$ go build .
go: downloading ...
...
$ ls
main.go  README.md  testgrid-config-generator
```
Ensure you have cloned and updated https://github.com/kubernetes/test-infra locally, along with https://github.com/openshift/release

Assuming you have all your repos rooted at the same toplevel dir, you can run the following command from the `github.com/openshift/ci-tools/cmd/testgrid-config-generator` directory, otherwise you will need to specify the correct paths to the repos/subdirs:
```console
$ ./testgrid-config-generator -testgrid-config ../../../../kubernetes/test-infra/config/testgrids/openshift -release-config ../../../release/core-services/release-controller/_releases -prow-jobs-dir ../../../release/ci-operator/jobs -allow-list=../../../release/core-services/testgrid-config-generator/_allow-list.yaml
````
Verify that changes were made by checking your local `test-infra` repo. For example:
```console
$ cd path/to/github.com/kubernetes/test-infra/config/testgrids/openshift
$ git status
modified:   groups.yaml
new file:   redhat-openshift-...
```

Commit the  changes and file a PR in https://github.com/kubernetes/test-infra/ to land them if you cannot wait for the daily run of periodic-prow-auto-testgrid-generator job.

[generic-informing]: https://testgrid.k8s.io/redhat-openshift-informing
[release-controller-config]: https://github.com/openshift/release/tree/main/core-services/release-controller
[release-controller]: https://github.com/openshift/release-controller/
